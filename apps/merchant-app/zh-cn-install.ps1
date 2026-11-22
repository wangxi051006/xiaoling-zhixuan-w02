# Android Studio 中文语言包一键安装器
# 用法:  把 IDEA 的 zip 拖到 "安装中文包.bat" 上即可
param(
    [Parameter(Position = 0)][string]$Source,
    [string]$Target
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
Add-Type -AssemblyName System.IO.Compression.FileSystem

function Say($m, $c = 'Cyan') { Write-Host "[*] $m" -ForegroundColor $c }
function Ok($m)  { Write-Host "[OK] $m" -ForegroundColor Green }
function Err($m) { Write-Host "[!!] $m" -ForegroundColor Red }

# ---------- 1. 找到 Android Studio ----------
function Find-Studio {
    $cands = @(
        'D:\android studio',
        'D:\Android Studio',
        "$env:ProgramFiles\Android\Android Studio",
        "${env:ProgramFiles(x86)}\Android\Android Studio",
        "$env:LOCALAPPDATA\Programs\Android Studio"
    )
    foreach ($c in $cands) {
        if ($c -and (Test-Path (Join-Path $c 'bin\studio64.exe'))) { return $c }
    }
    foreach ($drive in @('D:\', 'C:\')) {
        $hit = Get-ChildItem -Path $drive -Filter 'studio64.exe' -Recurse -File -Depth 3 -ErrorAction SilentlyContinue |
               Select-Object -First 1
        if ($hit) { return (Split-Path (Split-Path $hit.FullName)) }
    }
    return $null
}

$as = if ($Target) { $Target } else { Find-Studio }
if (-not $as) { Err '找不到 Android Studio 安装目录，请用参数 -Target "D:\android studio" 指定'; exit 1 }
Ok "Android Studio: $as"
$build = ''
if (Test-Path (Join-Path $as 'build.txt')) { $build = (Get-Content (Join-Path $as 'build.txt') -Raw).Trim() }
Say "当前 build: $build"

$pluginsDir = Join-Path $as 'plugins'
if (-not (Test-Path $pluginsDir)) { New-Item -ItemType Directory -Force -Path $pluginsDir | Out-Null }

# ---------- 2. 找到来源 ----------
function Find-Source {
    if ($Source -and (Test-Path $Source)) { return (Resolve-Path $Source).Path }
    $roots = @("$env:USERPROFILE\Downloads", "$env:USERPROFILE\Desktop", 'D:\', 'D:\cxdownload', 'D:\下载', $env:TEMP)
    foreach ($r in $roots) {
        if (-not (Test-Path $r)) { continue }
        $z = Get-ChildItem -Path $r -Filter 'idea*.zip' -File -ErrorAction SilentlyContinue |
             Sort-Object LastWriteTime -Descending | Select-Object -First 1
        if ($z) { return $z.FullName }
    }
    return $null
}

$src = Find-Source
if (-not $src) { Err '没找到来源。请把 IDEA 的 zip 拖到「安装中文包.bat」上，或用 -Source 指定路径。'; exit 1 }
Ok "来源: $src"

$tmp = Join-Path $env:TEMP ('zhcn_' + [Guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null

# ---------- 3. 取出 localization-zh-cn ----------
if ((Get-Item $src) -is [System.IO.DirectoryInfo]) {
    $found = Get-ChildItem -Path $src -Filter 'localization-zh-cn' -Recurse -Directory -Depth 4 -ErrorAction SilentlyContinue |
             Where-Object { $_.FullName -match 'plugins' } | Select-Object -First 1
    if (-not $found) { Err '这个文件夹里没有 plugins\localization-zh-cn'; exit 1 }
    Say "从目录复制: $($found.FullName)"
    Copy-Item $found.FullName -Destination $tmp -Recurse -Force
    $pack = Join-Path $tmp 'localization-zh-cn'
}
elseif ($src -like '*.zip') {
    Say '正在从 zip 中提取插件（不整体解压，几秒钟）...'
    $zip = [IO.Compression.ZipFile]::OpenRead($src)
    try {
        $entries = @($zip.Entries | Where-Object { ($_.FullName -replace '\\', '/') -match '(^|/)localization-zh-cn/' })
        if ($entries.Count -eq 0) { Err '这个 zip 里没有 localization-zh-cn（确认下载的是 IDEA 2026.2 的 zip 包）'; exit 1 }
        $norm = ($entries[0].FullName -replace '\\', '/')
        $idx = $norm.IndexOf('localization-zh-cn/')
        $prefix = $norm.Substring(0, $idx + 'localization-zh-cn/'.Length)
        $n = 0
        foreach ($e in $entries) {
            $rel = ($e.FullName -replace '\\', '/').Substring($prefix.Length)
            if ([string]::IsNullOrEmpty($rel) -or $rel.EndsWith('/')) { continue }
            $out = Join-Path $tmp ($rel -replace '/', '\')
            $dir = Split-Path $out -Parent
            if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Force -Path $dir | Out-Null }
            [IO.Compression.ZipFileExtensions]::ExtractToFile($e, $out, $true)
            $n++
        }
        Ok "已提取 $n 个文件"
    }
    finally { $zip.Dispose() }
    $pack = $tmp
}
elseif ($src -like '*.jar') {
    New-Item -ItemType Directory -Force -Path (Join-Path $tmp 'lib') | Out-Null
    Copy-Item $src (Join-Path $tmp 'lib\localization-zh-cn.jar') -Force
    $pack = $tmp
}
else { Err '来源必须是 zip、解压后的文件夹或 localization-zh-cn.jar'; exit 1 }

# ---------- 4. 改 since-build / until-build ----------
$jars = Get-ChildItem -Path $pack -Filter '*.jar' -Recurse -File | Where-Object { $_.Name -match 'zh' }
if (-not $jars) { Err '插件目录里没找到中文包 jar'; exit 1 }
foreach ($j in $jars) {
    $zipU = [IO.Compression.ZipFile]::Open($j.FullName, 'Update')
    try {
        $entry = $zipU.Entries | Where-Object { ($_.FullName -replace '\\', '/') -ieq 'META-INF/plugin.xml' } | Select-Object -First 1
        if (-not $entry) { Err "跳过（无 plugin.xml）: $($j.Name)"; continue }
        $r = New-Object IO.StreamReader($entry.Open(), [Text.Encoding]::UTF8)
        $xml = $r.ReadToEnd(); $r.Close()
        $new = [regex]::Replace($xml, 'since-build="[^"]*"', 'since-build="262.0"')
        $new = [regex]::Replace($new, 'until-build="[^"]*"', 'until-build="262.*"')
        $entry.Delete()
        $ne = $zipU.CreateEntry('META-INF/plugin.xml')
        $sw = New-Object IO.StreamWriter($ne.Open(), (New-Object Text.UTF8Encoding($false)))
        $sw.Write($new); $sw.Close()
        Ok "已适配 build: $($j.Name)"
    }
    finally { $zipU.Dispose() }
}

# ---------- 5. 安装 ----------
$dst = Join-Path $pluginsDir 'localization-zh-cn'
if (Test-Path $dst) { Say "删除旧版本: $dst"; Remove-Item $dst -Recurse -Force }
Copy-Item $pack -Destination $dst -Recurse -Force
Ok "已安装到: $dst"

# ---------- 6. 检查 IDE 是否在运行 ----------
$running = Get-Process -Name 'studio64', 'studio' -ErrorAction SilentlyContinue
Write-Host ''
if ($running) {
    Write-Host '现在请：' -ForegroundColor Yellow
    Write-Host '  1) 完全退出 Android Studio（任务管理器确认没有 studio64.exe）' -ForegroundColor Yellow
}
else {
    Write-Host '现在请：' -ForegroundColor Yellow
}
Write-Host '  2) 启动 Android Studio -> Ctrl+Alt+S' -ForegroundColor Yellow
Write-Host '  3) Appearance & Behavior -> System Settings -> Language and Region' -ForegroundColor Yellow
Write-Host '  4) Language 选 中文(简体) -> Apply -> Restart' -ForegroundColor Yellow
Write-Host ''
Ok '安装完成'
