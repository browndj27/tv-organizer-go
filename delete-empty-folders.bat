@echo off
setlocal

set /p TARGET="Enter directory path to clean: "

if not exist "%TARGET%" (
    echo ERROR: Directory does not exist: %TARGET%
    pause
    exit /b 1
)

echo.
echo Deleting empty folders in: %TARGET%
echo.

for /f "delims=" %%d in ('dir /ad /b /s "%TARGET%" 2^>nul ^| sort /r') do (
    rd "%%d" 2>nul && echo Deleted: %%d
)

echo.
echo Done.
pause
