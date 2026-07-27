@echo off
setlocal enabledelayedexpansion

set /p TARGET="Enter directory path to clean: "
set /p LISTFILE="Enter path to .txt file listing file extensions: "

if not exist "%TARGET%" (
    echo ERROR: Directory does not exist: %TARGET%
    pause
    exit /b 1
)

if not exist "%LISTFILE%" (
    echo ERROR: Extension list file does not exist: %LISTFILE%
    pause
    exit /b 1
)

echo.
echo The following file types will be deleted from: %TARGET%
echo.

set COUNT=0
for /f "usebackq tokens=* delims=" %%E in ("%LISTFILE%") do (
    set "EXT=%%E"
    rem Skip blank lines and comments starting with # or ;
    if not "!EXT!"=="" if not "!EXT:~0,1!"=="#" if not "!EXT:~0,1!"==";" (
        set "EXT=!EXT: =!"
        if not "!EXT:~0,1!"=="." set "EXT=.!EXT!"
        echo   *!EXT!
        set /a COUNT+=1
    )
)

if !COUNT!==0 (
    echo ERROR: No valid file extensions found in %LISTFILE%
    pause
    exit /b 1
)

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

for /f "usebackq tokens=* delims=" %%E in ("%LISTFILE%") do (
    set "EXT=%%E"
    if not "!EXT!"=="" if not "!EXT:~0,1!"=="#" if not "!EXT:~0,1!"==";" (
        set "EXT=!EXT: =!"
        if not "!EXT:~0,1!"=="." set "EXT=.!EXT!"
        rem /M filters by extension, /S recurses subdirectories
        forfiles /P "%TARGET%" /S /M *!EXT! /C "cmd /c if @isdir==FALSE (del @path && echo Deleted: @path)" 2>nul
    )
)

echo.
echo Done.
pause
