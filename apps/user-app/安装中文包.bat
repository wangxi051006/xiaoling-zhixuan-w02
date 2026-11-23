@echo off
chcp 65001 >nul
setlocal
set "SRC=%~1"
set "TGT=%~2"
if "%SRC%"=="" (
    powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0zh-cn-install.ps1"
) else if "%TGT%"=="" (
    powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0zh-cn-install.ps1" -Source "%SRC%"
) else (
    powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0zh-cn-install.ps1" -Source "%SRC%" -Target "%TGT%"
)
echo.
pause
