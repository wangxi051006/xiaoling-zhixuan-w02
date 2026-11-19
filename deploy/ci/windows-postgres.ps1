param([ValidateSet('start','stop')][string]$Action='start')
$ErrorActionPreference='Stop'
$taskBin=Join-Path $PSScriptRoot 'windows-tools/node_modules/@embedded-postgres/windows-x64/native/bin'
$taskData=Join-Path $env:RUNNER_TEMP 'xiaoling-p07-pg-data'
if ($Action -eq 'stop') {
 if(Test-Path -LiteralPath (Join-Path $taskData 'postmaster.pid')) { & (Join-Path $taskBin 'pg_ctl.exe') -D $taskData -m fast -w stop; if($LASTEXITCODE){throw 'owned PG stop failed'} }
 exit 0
}
if(Test-Path -LiteralPath $taskData){throw 'refusing to reuse existing data directory'}
if(Get-NetTCPConnection -LocalPort 55497 -ErrorAction SilentlyContinue){throw 'P06 required port 55497 already occupied'}
& (Join-Path $taskBin 'postgres.exe') --version
& (Join-Path $taskBin 'initdb.exe') -D $taskData -U postgres -A trust --encoding=UTF8 --locale=C
if($LASTEXITCODE){throw 'isolated initdb failed'}
& (Join-Path $taskBin 'pg_ctl.exe') -D $taskData -l (Join-Path $env:RUNNER_TEMP 'xiaoling-p07-postgres.log') -o '-h 127.0.0.1 -p 55497' -w start
if($LASTEXITCODE){throw 'isolated PG startup failed'}
# The pinned Windows package omits createdb; initdb already created postgres in this job's fresh cluster.
'TEST_DATABASE_URL=postgres://postgres@127.0.0.1:55497/postgres?sslmode=disable' | Out-File -FilePath $env:GITHUB_ENV -Encoding utf8 -Append
