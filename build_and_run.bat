@echo off
setlocal

cd /d "%~dp0"

set "EXE_NAME=updater.exe"

echo [1/2] Building %EXE_NAME% ...
go build -o "%EXE_NAME%" .
if errorlevel 1 (
    echo.
    echo Build failed.
    pause
    exit /b 1
)

echo.
echo [2/2] Starting %EXE_NAME% ...
if not exist "%EXE_NAME%" (
    echo %EXE_NAME% not found.
    pause
    exit /b 1
)

start "updater" cmd /k ""%~dp0%EXE_NAME%""
echo %EXE_NAME% launch command sent.

exit /b 0
