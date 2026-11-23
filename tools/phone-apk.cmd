@echo off
setlocal
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0phone-apk.ps1" %*
