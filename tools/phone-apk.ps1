<#
.SYNOPSIS
  校零智选 · 手机装包一条龙：局域网下载站 + adb reverse 端口回接 + 装机。

.DESCRIPTION
  商家端 / 骑手端是 TWA 壳，包内地址是 http://merchant.localhost:5173/merchant.html 与
  http://rider.localhost:5173/rider.html。手机上的 *.localhost 指向手机自己的回环，所以必须用
  adb reverse 把设备 5173 端口接回电脑的 Vite（5173 上的 /api/v1 再由 Vite 代理到 8080）。

      tools\phone-apk.cmd serve     起局域网下载站（手机浏览器打开提示的地址下载 APK）
      tools\phone-apk.cmd link      对已连接设备做 adb reverse（USB 或无线调试都可）
      tools\phone-apk.cmd install   直接 adb install -r 两个包（不用下载站）
      tools\phone-apk.cmd all       link + install + serve
      tools\phone-apk.cmd status    设备 / 端口映射 / 安装包清单

.EXAMPLE
  tools\phone-apk.cmd all -Port 8000 -WebPort 5173
#>
[CmdletBinding()]
param(
    [ValidateSet('serve', 'link', 'install', 'all', 'status', 'pair', 'connect')]
    [string]$Action = 'status',
    [int]$Port = 8000,
    [int]$WebPort = 5173,
    [string]$ApkDir = 'dist/apk',
    [string]$WifiHost,
    [int]$AdbPort = 5555,
    [int]$PairPort,
    [string]$PairCode
)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$Sdk = if ($env:ANDROID_HOME) { $env:ANDROID_HOME } else { Join-Path $env:LOCALAPPDATA 'Android\Sdk' }
$Adb = Join-Path $Sdk 'platform-tools\adb.exe'
$ShareScript = Join-Path $Root 'tools/apk-share.mjs'
$PackDir = Join-Path $Root $ApkDir

function Get-LanIp {
    Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue |
        Where-Object { $_.IPAddress -notlike '127.*' -and $_.IPAddress -notlike '169.254.*' } |
        Sort-Object -Property SkipAsSource |
        Select-Object -First 1 -ExpandProperty IPAddress
}

function Show-Status {
    Write-Host "工程        $Root"
    Write-Host "安装包目录  $PackDir"
    if (Test-Path $PackDir) {
        Get-ChildItem $PackDir -Filter *.apk | ForEach-Object {
            Write-Host ("  {0,-24} {1,8:N2} MB  {2:yyyy-MM-dd HH:mm}" -f $_.Name, ($_.Length / 1MB), $_.LastWriteTime)
        }
    } else {
        Write-Warning "还没有 $PackDir"
    }
    $ip = Get-LanIp
    if ($ip) { Write-Host ("下载站      http://" + $ip + ":" + $Port + "/") }
    if (Test-Path $Adb) {
        $devices = & $Adb devices | Select-Object -Skip 1 | Where-Object { $_ -match '\sdevice$' }
        if ($devices) {
            Write-Host "已连接设备"
            $devices | ForEach-Object { Write-Host "  $_" }
            Write-Host "端口映射"
            & $Adb reverse --list
        } else {
            Write-Host "已连接设备  无（USB 调试或无线调试打开后重跑 link）"
        }
    }
}

function Start-Serve {
    if (-not (Test-Path $ShareScript)) { throw "缺少 $ShareScript" }
    Write-Host ("起下载站：http://" + (Get-LanIp) + ":" + $Port + "/   （Ctrl+C 结束）")
    & node $ShareScript --port $Port --dir $ApkDir
}

function Invoke-Link {
    if (-not (Test-Path $Adb)) { throw "找不到 adb：$Adb（设置 ANDROID_HOME 或安装 platform-tools）" }
    $line = (& $Adb devices | Select-Object -Skip 1 | Where-Object { $_ -match '\sdevice$' } | Select-Object -First 1)
    if (-not $line) { throw "没有已授权设备：先打开 USB 调试 / 无线调试，再 adb devices 确认" }
    $serial = ($line -split '\s+')[0]
    Write-Host "设备 $serial → reverse tcp:$WebPort"
    & $Adb -s $serial reverse "tcp:$WebPort" "tcp:$WebPort"
    & $Adb -s $serial reverse --list
}

function Invoke-Install {
    if (-not (Test-Path $Adb)) { throw "找不到 adb：$Adb" }
    $apks = Get-ChildItem $PackDir -Filter *.apk -ErrorAction SilentlyContinue
    if (-not $apks) { throw "$PackDir 下没有 APK，先跑 tools\build-apk.cmd" }
    foreach ($apk in $apks) {
        Write-Host "安装 $($apk.Name)"
        & $Adb install -r -d $apk.FullName
        if ($LASTEXITCODE -ne 0) { throw "安装失败：$($apk.Name)" }
    }
}

function Invoke-Pair {
    if (-not $WifiHost -or -not $PairPort -or -not $PairCode) {
        throw "用法：phone-apk.cmd pair -WifiHost <手机IP> -PairPort <配对端口> -PairCode <6位码>（手机：开发者选项 / 无线调试 / 使用配对码配对）"
    }
    & $Adb pair "$($WifiHost):$($PairPort)" $PairCode
}

function Invoke-Connect {
    if (-not $WifiHost) { throw "用法：phone-apk.cmd connect -WifiHost <手机IP> [-AdbPort 5555]" }
    & $Adb connect "$($WifiHost):$($AdbPort)"
    & $Adb devices -l
}

switch ($Action) {
    'pair' { Invoke-Pair; Show-Status }
    'connect' { Invoke-Connect; Show-Status }
    'serve' { Start-Serve }
    'link' { Invoke-Link; Show-Status }
    'install' { Invoke-Install; Show-Status }
    'all' { Invoke-Link; Invoke-Install; Show-Status; Start-Serve }
    default { Show-Status }
}
