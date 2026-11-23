@echo off
chcp 65001 >nul
rem 双击即打三端 debug APK；要指定目标/正式域名请直接调 powershell 脚本。
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build-apk.ps1" %*
echo.
pause
