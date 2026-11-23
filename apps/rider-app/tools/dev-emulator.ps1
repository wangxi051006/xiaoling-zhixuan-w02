# 骑手端模拟器/真机联调：起本地 API + 端口反向，让 APK 里的 rider.localhost 能访问主机开发服务器。
# 用法（在任意目录执行）：
#   pwsh -File apps/rider-app/tools/dev-emulator.ps1                 # Web 5174、API 8080，自动起 API 并做 adb reverse
#   pwsh -File apps/rider-app/tools/dev-emulator.ps1 -SkipApi        # API 已由你自己起，只做 adb reverse
#   pwsh -File apps/rider-app/tools/dev-emulator.ps1 -WebPort 5175 -ApiPort 8080
param(
  [int]$WebPort = 5174,
  [int]$ApiPort = 8080,
  [switch]$SkipApi
)

$ErrorActionPreference = 'Stop'
$repo = Resolve-Path (Join-Path $PSScriptRoot '../..')
$logDir = Join-Path $PSScriptRoot '../.dev'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null

function Find-Adb {
  $candidates = @()
  if ($env:ANDROID_HOME) { $candidates += (Join-Path $env:ANDROID_HOME 'platform-tools/adb.exe') }
  if ($env:ANDROID_SDK_ROOT) { $candidates += (Join-Path $env:ANDROID_SDK_ROOT 'platform-tools/adb.exe') }
  $candidates += (Join-Path $env:LOCALAPPDATA 'Android/Sdk/platform-tools/adb.exe')
  foreach ($path in $candidates) { if (Test-Path $path) { return $path } }
  $command = Get-Command adb -ErrorAction SilentlyContinue
  if ($command) { return $command.Source }
  throw '找不到 adb：请安装 Android SDK platform-tools，或设置 ANDROID_HOME。'
}

$adb = Find-Adb
$devices = & $adb devices | Select-Object -Skip 1 | Where-Object { $_ -match '\sdevice$' }
if (-not $devices) {
  Write-Host '没有已连接的模拟器或真机。先启动模拟器（或在手机上打开 USB 调试）再执行本脚本。' -ForegroundColor Yellow
  & $adb devices
  exit 1
}

# 反向映射：设备上的 rider.localhost 解析到 127.0.0.1，正需要这条映射才能到达主机。
& $adb reverse "tcp:$WebPort" "tcp:$WebPort" | Out-Null
Write-Host "adb reverse 已设置：设备 127.0.0.1:$WebPort -> 主机 $WebPort" -ForegroundColor Green

if (-not $SkipApi) {
  $listening = Get-NetTCPConnection -LocalPort $ApiPort -State Listen -ErrorAction SilentlyContinue
  if ($listening) {
    Write-Host "端口 $ApiPort 已有进程在监听，跳过启动 API（如果不是本次代码编译的，请先停掉它）。" -ForegroundColor Yellow
  } else {
    $go = (Get-Command go -ErrorAction SilentlyContinue).Source
    if (-not $go -and (Test-Path 'C:/Program Files/Go/bin/go.exe')) { $go = 'C:/Program Files/Go/bin/go.exe' }
    if (-not $go) { throw '找不到 go：请把 Go 加入 PATH 或手动启动 API。' }
    $exe = Join-Path $logDir 'xiaoling-api.exe'
    Push-Location (Join-Path $repo 'backend')
    try {
      & $go build -o $exe ./cmd/api
      if ($LASTEXITCODE) { throw 'API 编译失败。' }
    } finally { Pop-Location }

    $origins = @{
      USER_ORIGIN = "http://user.localhost:5173"; ADMIN_ORIGIN = "http://admin.localhost:5173"
      MERCHANT_ORIGIN = "http://merchant.localhost:5173"; RIDER_ORIGIN = "http://rider.localhost:$WebPort"
    }
    $environment = @{
      APP_ENV = 'development'; HTTP_ADDR = "127.0.0.1:$ApiPort"
      DATABASE_URL = 'postgres://postgres@127.0.0.1:5432/campus?sslmode=disable'
      MIGRATION_DATABASE_URL = 'postgres://postgres@127.0.0.1:5432/campus?sslmode=disable'
      ALLOW_TEST_AUTH = 'true'; COD_ENABLED = 'true'; BALANCE_ENABLED = 'true'
      ORDERS_ENABLED = 'true'; DISPATCH_ENABLED = 'true'
      PRODUCT_IMAGE_DIR = (Join-Path $logDir 'product-images')
    } + $origins
    foreach ($key in $environment.Keys) { [Environment]::SetEnvironmentVariable($key, $environment[$key], 'Process') }
    $api = Start-Process -FilePath $exe -WorkingDirectory (Join-Path $repo 'backend') -WindowStyle Hidden -PassThru `
      -RedirectStandardOutput (Join-Path $logDir 'api.out.log') -RedirectStandardError (Join-Path $logDir 'api.err.log')
    Start-Sleep -Seconds 2
    $code = try { (Invoke-WebRequest -UseBasicParsing -TimeoutSec 3 -Uri "http://127.0.0.1:$ApiPort/api/v1/me").StatusCode } catch { $_.Exception.Response.StatusCode.value__ }
    Write-Host "API 已启动：PID $($api.Id)，http://127.0.0.1:$ApiPort（探测 /me -> $code，404/401 都算正常）" -ForegroundColor Green
  }
}

Write-Host "接下来：在设备上打开 APK；若页面是旧的，点 Chrome 里的 Reload。" -ForegroundColor Cyan
Write-Host "APK 的 RIDER_ORIGIN 必须是 http://rider.localhost:$WebPort（当前 Web 开发服务器端口）。"
