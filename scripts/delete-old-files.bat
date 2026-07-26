@echo off
setlocal

set /p TARGET="Enter directory path to clean: "
set /p DAYS="Enter number of days (files older than this will be deleted): "

if not exist "%TARGET%" (
    echo ERROR: Directory does not exist: %TARGET%
    pause
    exit /b 1
)

rem Validate that DAYS is a number
echo %DAYS%| findstr /r "^[0-9][0-9]*$" >nul
if errorlevel 1 (
    echo ERROR: Days must be a positive number
    pause
    exit /b 1
)

echo.
echo Deleting files older than %DAYS% days in: %TARGET%
echo.
echo WARNING: This will permanently delete files!
set /p CONFIRM="Are you sure you want to continue? (Y/N): "

if /i not "%CONFIRM%"=="Y" (
    echo Operation cancelled.
    pause
    exit /b 0
)

echo.
echo Processing...
echo.

rem Use forfiles to find and delete files older than specified days
rem /S = recursive, /D = date filter (negative value means older than)
forfiles /P "%TARGET%" /S /D -%DAYS% /C "cmd /c if @isdir==FALSE (del @path && echo Deleted: @path)" 2>nul

if errorlevel 1 (
    echo No files found matching criteria or error occurred.
) else (
    echo.
    echo Done.
)

pause
