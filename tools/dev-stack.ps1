<#
.SYNOPSIS
  校零智选本地开发栈：PostgreSQL + Go API + Vite（+ 模拟器端口映射）。

.DESCRIPTION
  用一条命令把骑手端/商家端联调需要的东西拉起来，并记录 PID 与日志：

      tools\dev-stack.ps1 status         查看状态
      tools\dev-stack.ps1 start          启动（数据库服务、后端、前端、adb 映射）
      tools\dev-stack.ps1 stop           停止后端与前端
      tools\dev-stack.ps1 restart        重启
      tools\dev-stack.ps1 init-db        首次初始化：initdb + 注册服务 + 建库

  默认端口：商家/骑手/运营 Vite 5173、用户端 Vite 5194、后端 8080、PostgreSQL 5432。
  默认入口：http://rider.localhost:5173/rider.html（后端测试登录账号 123456 / 123456）。
  这些端口必须与 APK（apps/*/local.properties）和小程序（apps/user-miniprogram/storefront.js）
  写死的 origin 一致；任一端改端口都要同步改客户端，否则后端按入口 origin 拒绝并返回 404。
  状态文件与日志放在仓库根的 .dev-stack/（已加入 .gitignore）。

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File tools\dev-stack.ps1 start

.EXAMPLE
  tools\dev-stack.cmd start -WithEmulator
#>
[CmdletBinding()]
param(
    [ValidateSet('start', 'stop', 'restart', 'status', 'init-db')]
    [string]$Action = 'status',
    [int]$WebPort = 5173,
    [int]$UserPort = 5194,
    [int]$ApiPort = 8080,
    [string]$PgService = 'pgsql-local',
    [string]$PgHome = (Join-Path $HOME 'pgsql'),
    [string]$PgData = (Join-Path $HOME 'pgdata'),
    [string]$DbName = 'campus',
    [string]$AvdName = 'Medium_Phone_API_37.0',
    [switch]$WithEmulator,
    [switch]$Force,
    [switch]$SkipMigrate
)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$State = Join-Path $Root '.dev-stack'
$Backend = Join-Path $Root 'backend'
$Web = Join-Path $Root 'web'
$ApiExe = Join-Path $State 'campus-api.exe'
$ApiPidFile = Join-Path $State 'api.pid'
$WebPidFile = Join-Path $State 'web.pid'
$UserWebPidFile = Join-Path $State 'user-web.pid'

$Sdk = if ($env:ANDROID_HOME) { $env:ANDROID_HOME } else { Join-Path $env:LOCALAPPDATA 'Android\Sdk' }
$Adb = Join-Path $Sdk 'platform-tools\adb.exe'
$Emulator = Join-Path $Sdk 'emulator\emulator.exe'

# 变量名后紧跟 ?sslmode 时 PowerShell 会把 DbName?sslmode 当成变量名，必须用 ${DbName} 划定边界。
$DbUrl = "postgres://postgres@127.0.0.1:5432/${DbName}?sslmode=disable"

# 四个入口必须是 <entry>.localhost：员工端与 WebPort 一致，用户端与 UserPort 一致，后端才放行测试登录。
$env:APP_ENV = 'development'
$env:HTTP_ADDR = "127.0.0.1:$ApiPort"
$env:DATABASE_URL = $DbUrl
$env:MIGRATION_DATABASE_URL = $DbUrl
$env:USER_ORIGIN = "http://user.localhost:$UserPort"
$env:RIDER_ORIGIN = "http://rider.localhost:$WebPort"
$env:ADMIN_ORIGIN = "http://admin.localhost:$WebPort"
$env:MERCHANT_ORIGIN = "http://merchant.localhost:$WebPort"
$env:ALLOW_TEST_AUTH = 'true'
$env:COD_ENABLED = 'true'
$env:ORDERS_ENABLED = 'true'
$env:DISPATCH_ENABLED = 'true'
$env:BALANCE_ENABLED = 'true'
$env:WECHAT_ENABLED = 'false'
$env:EMAIL_SIGNUP_ENABLED = 'false'
$env:EMAIL_NOTIFICATIONS_ENABLED = 'false'
$env:DEV_API_ORIGIN = "http://127.0.0.1:$ApiPort"

function Write-Step($msg) { Write-Host "[dev-stack] $msg" -ForegroundColor Cyan }
function Write-Ok($msg) { Write-Host "[dev-stack] $msg" -ForegroundColor Green }
function Write-Warn2($msg) { Write-Host "[dev-stack] $msg" -ForegroundColor Yellow }

function Test-Port([int]$port) {
    $client = New-Object System.Net.Sockets.TcpClient
    try {
        $async = $client.BeginConnect('127.0.0.1', $port, $null, $null)
        if (-not $async.AsyncWaitHandle.WaitOne(400)) { return $false }
        $client.EndConnect($async)
        return $true
    } catch {
        return $false
    } finally {
        $client.Close()
    }
}

function Get-PortPid([int]$port) {
    try {
        $conn = Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction Stop | Select-Object -First 1
        if ($conn) { return [int]$conn.OwningProcess }
    } catch {
    }
    return 0
}

function Wait-Port([int]$port, [int]$timeoutSeconds = 40) {
    for ($i = 0; $i -lt $timeoutSeconds; $i++) {
        if (Test-Port $port) { return $true }
        Start-Sleep -Seconds 1
    }
    return $false
}

function Save-Pid($path, $processId) {
    if ($processId) { Set-Content -Path $path -Value $processId -Encoding ascii }
}

function Read-Pid($path) {
    if (Test-Path $path) {
        $raw = (Get-Content -Path $path -ErrorAction SilentlyContinue | Select-Object -First 1)
        if ($raw -and $raw.Trim() -match '^\d+$') { return [int]$raw.Trim() }
    }
    return 0
}

function Stop-Recorded($path, [int]$port) {
    $processId = Read-Pid $path
    if ($processId -gt 0) {
        $proc = Get-Process -Id $processId -ErrorAction SilentlyContinue
        if ($proc) {
            Stop-Process -Id $processId -Force -ErrorAction SilentlyContinue
            Write-Ok "已停止 PID $processId"
        }
        Remove-Item -Path $path -ErrorAction SilentlyContinue
        return
    }
    $portPid = Get-PortPid $port
    if ($portPid -gt 0) {
        Write-Warn2 "端口 $port 上有外部进程 PID $portPid（不是本脚本启动的），未处理"
    }
}

function Ensure-Postgres {
    $service = Get-Service -Name $PgService -ErrorAction SilentlyContinue
    if (-not $service) {
        throw "找不到 PostgreSQL 服务 '$PgService'。首次使用请先运行：tools\dev-stack.ps1 init-db"
    }
    if ($service.Status -ne 'Running') {
        Write-Step "启动服务 $PgService ..."
        try { Start-Service -Name $PgService } catch { throw "启动服务失败（需要管理员权限）：$($_.Exception.Message)" }
        if (-not (Wait-Port 5432 30)) { throw 'PostgreSQL 30 秒内没有监听 5432' }
    }
    $psql = Join-Path $PgHome 'bin\psql.exe'
    if (Test-Path $psql) {
        $exists = & $psql -h 127.0.0.1 -p 5432 -U postgres -tAc "select 1 from pg_database where datname='$DbName'" 2>$null
        if (-not $exists) {
            Write-Step "创建数据库 $DbName ..."
            & (Join-Path $PgHome 'bin\createdb.exe') -h 127.0.0.1 -p 5432 -U postgres $DbName
        }
    }
    Write-Ok "PostgreSQL 5432 / 数据库 $DbName"
}

function Build-Api {
    $needsBuild = -not (Test-Path $ApiExe)
    if (-not $needsBuild) {
        $newest = Get-ChildItem -Path $Backend -Recurse -Include '*.go', '*.sql' -ErrorAction SilentlyContinue | Sort-Object LastWriteTime -Descending | Select-Object -First 1
        if ($newest -and $newest.LastWriteTime -gt (Get-Item $ApiExe).LastWriteTime) { $needsBuild = $true }
    }
    if (-not $needsBuild) { return }
    Write-Step '编译后端 campus-api.exe ...'
    Push-Location $Backend
    try {
        & go build -o $ApiExe ./cmd/api
        if ($LASTEXITCODE -ne 0) { throw "go build 失败（exit $LASTEXITCODE）" }
    } finally {
        Pop-Location
    }
    Write-Ok "已编译 $ApiExe"
}

function Get-AppliedMigrations {
    $psql = Join-Path $PgHome 'binpsql.exe'
    if (-not (Test-Path $psql)) { return -1 }
    $raw = & $psql -h 127.0.0.1 -p 5432 -U postgres -d $DbName -tA -c 'select count(*) from schema_migrations' 2>&1
    if ($LASTEXITCODE -ne 0) { return -1 }
    $text = (($raw | Out-String).Trim())
    if ($text -match '^[0-9]+$') { return [int]$text }
    return -1
}

function Invoke-Migrate {
    if ($SkipMigrate) { Write-Warn2 '按参数跳过数据库迁移'; return }
    Write-Step '执行数据库迁移 ...'
    $output = $null
    for ($attempt = 1; $attempt -le 3; $attempt++) {
        $output = & $ApiExe migrate 2>&1
        if ($LASTEXITCODE -eq 0) { Write-Ok '迁移完成'; return }
        Write-Warn2 "迁移第 $attempt 次失败，2 秒后重试"
        Start-Sleep -Seconds 2
    }
    $applied = Get-AppliedMigrations
    $files = (Get-ChildItem -Path (Join-Path $Backend 'migrations') -Filter '*.sql' -ErrorAction SilentlyContinue).Count
    $output | ForEach-Object { Write-Host "    $_" -ForegroundColor DarkGray }
    if ($applied -ge 0 -and $applied -ge $files) {
        Write-Warn2 "迁移命令失败，但数据库已有 $applied/$files 条迁移记录，按“已迁移”继续启动"
        return
    }
    Write-Warn2 "迁移失败（数据库记录 $applied/$files）。继续启动后端口会起来，但新表可能缺失，请检查后端输出与 DATABASE_URL"
}

function Start-Api {
    if (Test-Port $ApiPort) {
        $portPid = Get-PortPid $ApiPort
        $recorded = Read-Pid $ApiPidFile
        if ($recorded -eq $portPid -and $recorded -gt 0) {
            Write-Ok "后端已在运行（PID $portPid，端口 $ApiPort）"
            return
        } elseif ($Force) {
            Write-Warn2 "端口 $ApiPort 被外部进程 PID $portPid 占用，-Force 已接管"
            Stop-Process -Id $portPid -Force -ErrorAction SilentlyContinue
            Start-Sleep -Seconds 2
        } else {
            Write-Warn2 "端口 $ApiPort 已被 PID $portPid 占用，跳过启动后端（加 -Force 可接管）"
            return
        }
    }
    Build-Api
    Invoke-Migrate
    $outLog = Join-Path $State 'api.out.log'
    $errLog = Join-Path $State 'api.err.log'
    $proc = Start-Process -FilePath $ApiExe -WorkingDirectory $Backend -WindowStyle Hidden -RedirectStandardOutput $outLog -RedirectStandardError $errLog -PassThru
    Save-Pid $ApiPidFile $proc.Id
    if (-not (Wait-Port $ApiPort 30)) {
        Write-Warn2 '后端没有起来，最后的日志：'
        foreach ($logFile in @($outLog, $errLog)) {
            if (Test-Path $logFile) {
                Get-Content -Path $logFile -Tail 8 | ForEach-Object { Write-Host "    $_" -ForegroundColor DarkGray }
            }
        }
        throw "后端没有在 $ApiPort 起监听，完整日志：$outLog"
    }
    Write-Ok "后端 运行中 http://127.0.0.1:$ApiPort（PID $($proc.Id)，日志 $outLog）"
}

function Start-Vite([string]$Label, [int]$Port, [string]$PidFile, [string]$LogName) {
    if (Test-Port $Port) {
        $portPid = Get-PortPid $Port
        $recorded = Read-Pid $PidFile
        if ($recorded -eq $portPid -and $recorded -gt 0) {
            Write-Ok "$Label 已在运行（PID $portPid，端口 $Port）"
            return
        } elseif ($Force) {
            Write-Warn2 "端口 $Port 被外部进程 PID $portPid 占用，-Force 已接管"
            Stop-Process -Id $portPid -Force -ErrorAction SilentlyContinue
            Start-Sleep -Seconds 2
        } else {
            Write-Warn2 "端口 $Port 已被 PID $portPid 占用，跳过启动 $Label（加 -Force 可接管）"
            return
        }
    }
    $vite = Join-Path $Web 'node_modules\vite\bin\vite.js'
    if (-not (Test-Path $vite)) { throw "找不到 $vite，先在 web/ 执行 npm ci" }
    $node = Get-Command node -ErrorAction SilentlyContinue
    if (-not $node) { throw 'PATH 里找不到 node' }
    $outLog = Join-Path $State "$LogName.out.log"
    $errLog = Join-Path $State "$LogName.err.log"
    $viteArgs = @($vite, '--port', $Port, '--strictPort', '--host', '127.0.0.1')
    $proc = Start-Process -FilePath $node.Source -WorkingDirectory $Web -WindowStyle Hidden -ArgumentList $viteArgs -RedirectStandardOutput $outLog -RedirectStandardError $errLog -PassThru
    Save-Pid $PidFile $proc.Id
    if (-not (Wait-Port $Port 40)) { throw "$Label 没有在 $Port 起监听，日志：$errLog" }
    Write-Ok "$Label 运行中 http://127.0.0.1:$Port（PID $($proc.Id)，日志 $outLog）"
}

function Start-Web {
    Start-Vite 'Vite 员工端' $WebPort $WebPidFile 'web'
    Start-Vite 'Vite 用户端' $UserPort $UserWebPidFile 'user-web'
}

function Start-Devices {
    if (-not (Test-Path $Adb)) { Write-Warn2 "找不到 adb（$Adb），跳过设备端口映射"; return }
    $devices = & $Adb devices | Select-String 'device$'
    if (-not $devices -and $WithEmulator) {
        if (Test-Path $Emulator) {
            Write-Step "冷启动模拟器 $AvdName ..."
            Start-Process -FilePath $Emulator -ArgumentList @('-avd', $AvdName) -WindowStyle Normal | Out-Null
            & $Adb wait-for-device | Out-Null
            Start-Sleep -Seconds 3
            $devices = & $Adb devices | Select-String 'device$'
        } else {
            Write-Warn2 "找不到 emulator.exe（$Emulator）"
        }
    }
    if (-not $devices) { Write-Warn2 '没有连接中的设备，跳过 adb reverse'; return }
    & $Adb reverse "tcp:$WebPort" "tcp:$WebPort" | Out-Null
    Write-Ok "adb reverse tcp:$WebPort -> tcp:$WebPort"
    & $Adb reverse "tcp:$UserPort" "tcp:$UserPort" | Out-Null
    Write-Ok "adb reverse tcp:$UserPort -> tcp:$UserPort"
    & $Adb shell am force-stop cn.xiaoling.rider 2>$null | Out-Null
    & $Adb shell am start -n cn.xiaoling.rider/.MainActivity 2>$null | Out-Null
    Write-Ok '已在设备上打开骑手端'
}

function Show-Status {
    $rows = @(
        @{ Name = 'PostgreSQL'; Port = 5432; Url = "postgres://127.0.0.1:5432/$DbName" },
        @{ Name = 'Go API'; Port = $ApiPort; Url = "http://127.0.0.1:$ApiPort" },
        @{ Name = 'Vite 员工'; Port = $WebPort; Url = "http://rider.localhost:$WebPort/rider.html" },
        @{ Name = 'Vite 用户'; Port = $UserPort; Url = "http://user.localhost:$UserPort/user.html" }
    )
    foreach ($row in $rows) {
        $portPid = Get-PortPid $row.Port
        if ($portPid -gt 0) {
            Write-Host ("  {0,-11} {1,-6} 运行中 PID{2,-7} {3}" -f $row.Name, $row.Port, $portPid, $row.Url) -ForegroundColor Green
        } else {
            Write-Host ("  {0,-11} {1,-6} 未运行" -f $row.Name, $row.Port) -ForegroundColor DarkGray
        }
    }
    if (Test-Path $Adb) {
        $devices = (& $Adb devices) -join ' '
        if ($devices -match 'device\s') {
            $reverse = (& $Adb reverse --list) -join ','
            Write-Host "  设备        已连接；reverse: $reverse" -ForegroundColor Green
        } else {
            Write-Host '  设备        未连接' -ForegroundColor DarkGray
        }
    }
    Write-Host "  状态目录    $State"
}

function Invoke-InitDb {
    $initdb = Join-Path $PgHome 'bin\initdb.exe'
    if (-not (Test-Path $initdb)) {
        throw "找不到 $initdb。请先把 PostgreSQL 免安装版解压到 $PgHome"
    }
    if (-not (Test-Path (Join-Path $PgData 'PG_VERSION'))) {
        Write-Step "初始化数据目录 $PgData ..."
        & $initdb -D $PgData -U postgres -A trust -E UTF8 --locale=C | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'initdb 失败' }
    }
    if (-not (Get-Service -Name $PgService -ErrorAction SilentlyContinue)) {
        Write-Step "注册服务 $PgService（需要管理员权限）..."
        & (Join-Path $PgHome 'bin\pg_ctl.exe') register -N $PgService -D $PgData -S auto -o "-p 5432 -c listen_addresses=127.0.0.1" | Out-Null
    }
    $service = Get-Service -Name $PgService -ErrorAction SilentlyContinue
    if ($service -and $service.Status -ne 'Running') { Start-Service -Name $PgService }
    Wait-Port 5432 30 | Out-Null
    Ensure-Postgres
    Write-Ok "数据库就绪：$DbUrl"
    Write-Warn2 '接下来执行：tools\dev-stack.ps1 start'
}

New-Item -ItemType Directory -Force -Path $State | Out-Null

switch ($Action) {
    'init-db' { Invoke-InitDb }
    'start' {
        Write-Step "仓库根目录 $Root"
        Ensure-Postgres
        Start-Api
        Start-Web
        Start-Devices
        Show-Status
        Write-Host "  商家端      http://merchant.localhost:$WebPort/merchant.html"
        Write-Host "  骑手端      http://rider.localhost:$WebPort/rider.html"
        Write-Host "  用户端      http://user.localhost:$UserPort/user.html"
        Write-Host "  运营端      http://admin.localhost:$WebPort/admin.html（测试登录 123456 / 123456）"
    }
    'stop' {
        Stop-Recorded $ApiPidFile $ApiPort
        Stop-Recorded $WebPidFile $WebPort
        Stop-Recorded $UserWebPidFile $UserPort
        Write-Ok ("已停止后端与前端；PostgreSQL 服务保持运行（如需停止：sc stop " + $PgService + "）")
    }
    'restart' {
        Stop-Recorded $ApiPidFile $ApiPort
        Stop-Recorded $WebPidFile $WebPort
        Stop-Recorded $UserWebPidFile $UserPort
        Start-Sleep -Seconds 2
        Ensure-Postgres
        Start-Api
        Start-Web
        Start-Devices
        Show-Status
    }
    'status' { Show-Status }
}
