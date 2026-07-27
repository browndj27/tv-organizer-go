@echo off
setlocal enabledelayedexpansion

rem Usage: delete-file-types.bat "<target dir>" "<extensions .txt file>"
rem If both args are supplied, the script runs unattended (no prompts, no
rem confirmation, no pause) so it is safe to call from Task Scheduler.
set "TARGET=%~1"
set "LISTFILE=%~2"
set "UNATTENDED=0"
if not "%~1"=="" if not "%~2"=="" set "UNATTENDED=1"

if "%TARGET%"=="" set /p TARGET="Enter directory path to clean: "
if "%LISTFILE%"=="" set /p LISTFILE="Enter path to .txt file listing file extensions: "

set "LOGFILE=%~dp0delete-file-types.log"
echo ==== Run started: %DATE% %TIME% ====>>"%LOGFILE%"
echo Target: %TARGET%>>"%LOGFILE%"
echo Extension list: %LISTFILE%>>"%LOGFILE%"

if not exist "%TARGET%" (
    echo ERROR: Directory does not exist: %TARGET%
    echo ERROR: Directory does not exist: %TARGET%>>"%LOGFILE%"
    if "%UNATTENDED%"=="0" pause
    exit /b 1
)

if not exist "%LISTFILE%" (
    echo ERROR: Extension list file does not exist: %LISTFILE%
    echo ERROR: Extension list file does not exist: %LISTFILE%>>"%LOGFILE%"
    if "%UNATTENDED%"=="0" pause
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
    echo ERROR: No valid file extensions found in %LISTFILE%>>"%LOGFILE%"
    if "%UNATTENDED%"=="0" pause
    exit /b 1
)

if "%UNATTENDED%"=="0" (
    echo.
    echo WARNING: This will permanently delete files!
    set /p CONFIRM="Are you sure you want to continue? (Y/N): "
    if /i not "!CONFIRM!"=="Y" (
        echo Operation cancelled.
        echo Operation cancelled by user.>>"%LOGFILE%"
        exit /b 0
    )
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
        forfiles /P "%TARGET%" /S /M *!EXT! /C "cmd /c if @isdir==FALSE (del @path && echo Deleted: @path)" >>"%LOGFILE%" 2>nul
    )
)

echo ==== Run finished: %DATE% %TIME% ====>>"%LOGFILE%"

echo.
echo Done. See %LOGFILE% for a record of deleted files.
if "%UNATTENDED%"=="0" pause
