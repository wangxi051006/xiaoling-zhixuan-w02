@echo off
chcp 65001 >nul
setlocal EnableExtensions

rem ============================================================
rem  用户端 TWA 壳的本地开发启动器。
rem  Android Studio 的 Gradle 进程不继承终端环境变量，
rem  这里显式给出 USER_ORIGIN / ALLOW_LOCAL_ORIGIN / JAVA_HOME，
rem  再用 studio64.exe 打开本工程，避免 client.gradle 报
rem  "USER_ORIGIN is required to build"。
rem ============================================================

set "USER_ORIGIN=http://user.localhost:5194/user.html"
set "ALLOW_LOCAL_ORIGIN=1"
set "JAVA_HOME=C:\Users\Administrator\.jdks\jdk-17.0.21.1+2"

set "STUDIO_EXE=D:\android studio\bin\studio64.exe"
set "PROJECT_DIR=%~dp0."

cd /d "%~dp0" || goto :no_cd

if not exist "%STUDIO_EXE%" (
    echo 未找到 Android Studio: %STUDIO_EXE%
    echo 请改本文件里的 STUDIO_EXE，或直接用命令行 gradlew.bat :user:assembleDebug
    pause
    exit /b 1
)

echo 用户端 origin: %USER_ORIGIN%
start "" "%STUDIO_EXE%" "%PROJECT_DIR%"
exit /b 0

:no_cd
echo 无法进入工程目录
pause
exit /b 1
