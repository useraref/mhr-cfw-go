@echo off
setlocal enabledelayedexpansion

set "SCRIPT_DIR=%~dp0"
cd /d "%SCRIPT_DIR%"

echo ========================================
echo  MHR-CFW Go Builder
echo ========================================
echo.

if not exist "go.mod" (
    echo Error: go.mod not found. Make sure you are in the project directory.
    pause
    exit /b 1
)

echo Building mhr-cfw-go.exe...
echo.

go build -ldflags "-s -w" -o mhr-cfw-go.exe ./cmd/mhr-cfw

if errorlevel 1 (
    echo.
    echo Build FAILED!
    pause
    exit /b 1
)

echo.
echo Build successful: mhr-cfw-go.exe
echo.

if exist "ivk.ico" (
    echo Adding icon...
    echo Note: Icon injection requires inject_icon.exe (run separately if needed)
)

echo.
echo ========================================
echo  Done! Run with: mhr-cfw-go.exe
echo ========================================
pause