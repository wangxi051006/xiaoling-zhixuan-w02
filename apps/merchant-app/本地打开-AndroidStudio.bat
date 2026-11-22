@echo off
chcp 65001 >nul
setlocal EnableExtensions

rem ============================================================
rem  Local dev launcher for the merchant TWA shell.
rem  Android Studio must see MERCHANT_ORIGIN / RIDER_ORIGIN at
rem  Gradle configure time, otherwise client.gradle aborts with
rem  "MERCHANT_ORIGIN must be the deployed HTTPS origin (got: null)".
rem  Launching Studio from this script guarantees those variables
rem  are present even when the Explorer environment is stale.
rem ============================================================

set "MERCHANT_ORIGIN=http://merchant.localhost:5173/merchant.html"
set "RIDER_ORIGIN=http://rider.localhost:5173/rider.html"
set "ALLOW_LOCAL_ORIGIN=1"
set "JAVA_HOME=C:\Users\Administrator\.jdks\jdk-17.0.21.1+2"

set "STUDIO_EXE=D:\android studio\bin\studio64.exe"
set "PROJECT_DIR=%~dp0."

cd /d "%~dp0" || goto :no_cd

set "RUNNING="
for /f "delims=" %%A in ('tasklist /fi "imagename eq studio64.exe" /nh 2^>nul ^| "%SystemRoot%\System32\findstr.exe" /i "studio64.exe"') do set "RUNNING=1"
if defined RUNNING goto :already_running

if not exist "%STUDIO_EXE%" goto :no_exe

echo [1/2] Stopping stale Gradle daemons ...
if exist gradlew.bat call gradlew.bat --stop >nul 2>&1

echo [2/2] Starting Android Studio with local origin env ...
echo        MERCHANT_ORIGIN=%MERCHANT_ORIGIN%
echo        RIDER_ORIGIN=%RIDER_ORIGIN%
start "" "%STUDIO_EXE%" "%PROJECT_DIR%"
exit /b 0

:already_running
echo.
echo [x] Android Studio is already running.
echo     Its environment is fixed at startup, so a new sync would
echo     still see MERCHANT_ORIGIN=null.
echo     Close it first:  File  ^>  Exit  (confirm no studio64.exe
echo     is left in Task Manager), then run this script again.
echo.
pause
exit /b 1

:no_exe
echo [x] Not found: %STUDIO_EXE%
pause
exit /b 1

:no_cd
echo [x] Cannot enter %~dp0
pause
exit /b 1
