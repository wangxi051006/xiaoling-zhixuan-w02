<#
.SYNOPSIS
  校零智选 Android 打包器：一条命令打出商家端 / 用户端 / 骑手端的 APK。

.DESCRIPTION
  三个客户端（apps/merchant-app、apps/user-app、apps/rider-app）共用同一套 TWA 壳逻辑，
  origin 的解析顺序是 -P 参数 → 环境变量 → 各自目录的 local.properties。

  本脚本：找 JDK 17 → 决定 origin（参数 → 环境变量 → local.properties → 本机联调默认值）
  → 本地 http 自动补 ALLOW_LOCAL_ORIGIN=1（release 直接拒绝）→ 调各自工程的 gradlew
  → 输出 APK 路径、体积、SHA-256 前 16 位。

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File tools/build-apk.ps1
  打三端 debug 包（origin 取各自 local.properties，缺省用本机联调地址）。

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File tools/build-apk.ps1 -Target user -Mode release -UserOrigin https://user.example.com -VersionName 1.0.0
  用正式域名打用户端 release 包（签名取决于 USER_KEYSTORE 等环境变量是否配置）。
#>
[CmdletBinding()]
param(
    [ValidateSet("all", "merchant", "user", "rider")]
    [string]$Target = "all",
    [ValidateSet("debug", "release")]
    [string]$Mode = "debug",
    [string]$MerchantOrigin,
    [string]$UserOrigin,
    [string]$RiderOrigin,
    [string]$JdkHome,
    [int]$VersionCode,
    [string]$VersionName,
    [switch]$NoDaemon
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot

$apps = [ordered]@{
    merchant = @{ Dir = "apps/merchant-app"; Module = "merchant"; OriginVar = "MERCHANT_ORIGIN"; Local = "http://merchant.localhost:5173/merchant.html"; Given = $MerchantOrigin }
    user     = @{ Dir = "apps/user-app";     Module = "user";     OriginVar = "USER_ORIGIN";     Local = "http://user.localhost:5194/user.html";     Given = $UserOrigin }
    rider    = @{ Dir = "apps/rider-app";    Module = "rider";    OriginVar = "RIDER_ORIGIN";    Local = "http://rider.localhost:5173/rider.html";    Given = $RiderOrigin }
}

function Resolve-Jdk([string]$Explicit) {
    $candidates = @()
    if ($Explicit) { $candidates += $Explicit }
    if ($env:JAVA_HOME) { $candidates += $env:JAVA_HOME }
    $jdks = Join-Path $env:USERPROFILE ".jdks"
    if (Test-Path $jdks) {
        $candidates += Get-ChildItem $jdks -Directory -ErrorAction SilentlyContinue |
            Where-Object { $_.Name -match "^jdk-17" } | Sort-Object Name -Descending | ForEach-Object { $_.FullName }
    }
    $candidates += @(
        "C:/Program Files/Microsoft/jdk-17.0.13.11-hotspot",
        "C:/Program Files/Eclipse Adoptium/jdk-17.0.13.11-hotspot",
        "D:/android studio/jbr"
    )
    $fallback = $null
    foreach ($path in $candidates) {
        if (-not $path) { continue }
        $java = Join-Path $path "bin/java.exe"
        if (-not (Test-Path $java)) { continue }
        $release = Join-Path $path "release"
        $declared = ""
        if (Test-Path $release) { $declared = Get-Content $release -Raw }
        if ($declared -match "17[.]") { return $path }
        if (-not $fallback) { $fallback = $path }
    }
    if ($fallback) {
        Write-Warning "没有找到 JDK 17，改用 $fallback"
        return $fallback
    }
    throw "找不到 JDK 17。用 -JdkHome 指定，或设置 JAVA_HOME。"
}

$jdk = Resolve-Jdk $JdkHome
$env:JAVA_HOME = $jdk
$env:PATH = (Join-Path $jdk "bin") + ";" + $env:PATH
Write-Host "JDK      $jdk"

function Read-LocalProperty([string]$file, [string]$name) {
    if (-not (Test-Path $file)) { return $null }
    $pattern = "^" + [regex]::Escape($name) + "=(.+)$"
    foreach ($line in Get-Content $file) {
        if ($line -match $pattern) { return $matches[1].Trim() }
    }
    return $null
}

function Resolve-Origin([hashtable]$app) {
    if ($app.Given) { return $app.Given }
    $fromEnv = [Environment]::GetEnvironmentVariable($app.OriginVar)
    if ($fromEnv) { return $fromEnv }
    $props = Join-Path $Root (Join-Path $app.Dir "local.properties")
    $fromFile = Read-LocalProperty $props $app.OriginVar
    if ($fromFile) { return $fromFile }
    return $app.Local
}

$selected = if ($Target -eq "all") { @($apps.Keys) } else { @($Target) }
$plan = @()
foreach ($name in $selected) {
    $app = $apps[$name]
    $origin = Resolve-Origin $app
    if ($Mode -eq "release" -and $origin -notmatch "^https://") {
        throw "$name 的 release 构建必须使用 HTTPS origin，当前是 $origin"
    }
    Set-Item -Path "env:$($app.OriginVar)" -Value $origin
    if ($origin -match "^http://") { $env:ALLOW_LOCAL_ORIGIN = "1" }
    $plan += [pscustomobject]@{ Name = $name; Dir = $app.Dir; Module = $app.Module; Origin = $origin }
}

$results = @()
foreach ($item in $plan) {
    $appDir = Join-Path $Root $item.Dir
    $gradlew = Join-Path $appDir "gradlew.bat"
    if (-not (Test-Path $gradlew)) { throw "缺少 $gradlew" }
    $variant = if ($Mode -eq "release") { "Release" } else { "Debug" }
    $task = ":$($item.Module):assemble$variant"
    $gradleArgs = @($task, "--console=plain")
    if ($NoDaemon) { $gradleArgs += "--no-daemon" }
    if ($VersionCode) { $gradleArgs += "-PappVersionCode=$VersionCode" }
    if ($VersionName) { $gradleArgs += "-PappVersionName=$VersionName" }

    Write-Host ""
    Write-Host "==> $($item.Name)  $($item.Origin)"
    Push-Location $appDir
    try {
        & $gradlew @gradleArgs
        if ($LASTEXITCODE -ne 0) { throw "$($item.Name) 构建失败, gradle exit $LASTEXITCODE" }
    } finally {
        Pop-Location
    }

    $outDir = Join-Path $appDir (Join-Path $item.Module "build/outputs/apk/$Mode")
    $apk = Get-ChildItem $outDir -Filter *.apk -ErrorAction SilentlyContinue | Sort-Object LastWriteTime -Descending | Select-Object -First 1
    if (-not $apk) { throw "构建结束但没找到 APK: $outDir" }
    $hash = (Get-FileHash $apk.FullName -Algorithm SHA256).Hash
    $results += [pscustomobject]@{
        Client = $item.Name
        Apk    = $apk.FullName
        SizeMB = [math]::Round($apk.Length / 1MB, 2)
        Sha256 = $hash.Substring(0, 16)
        Origin = $item.Origin
    }
}

Write-Host ""
$results | Format-Table -AutoSize -Wrap
foreach ($row in $results) { Write-Host "APK  $($row.Apk)" }
