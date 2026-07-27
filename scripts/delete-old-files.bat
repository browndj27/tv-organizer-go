@echo off
setlocal enabledelayedexpansion

rem Usage: delete-old-files.bat ["<params .txt file>"]
rem The params file's first non-comment line is the target directory,
rem the second is the day threshold. When a params file is supplied the
rem script runs unattended (no prompts, no confirmation, no pause) so it
rem is safe to call from Task Scheduler.
set "PARAMFILE=%~1"
set "UNATTENDED=0"

if not "%PARAMFILE%"=="" (
    if not exist "%PARAMFILE%" (
        echo ERROR: Parameter file does not exist: %PARAMFILE%
        pause
        exit /b 1
    )
    set "LINENUM=0"
    for /f "usebackq tokens=* delims=" %%L in ("%PARAMFILE%") do (
        set "LINE=%%L"
        if not "!LINE!"=="" if not "!LINE:~0,1!"=="#" if not "!LINE:~0,1!"==";" (
            set /a LINENUM+=1
            if !LINENUM!==1 set "TARGET=!LINE!"
            if !LINENUM!==2 set "DAYS=!LINE!"
        )
    )
    set "UNATTENDED=1"
) else (
    set /p TARGET="Enter directory path to clean: "
    set /p DAYS="Enter number of days (files older than this will be deleted): "
)

set "LOGFILE=%~dp0delete-old-files.log"
echo ==== Run started: %DATE% %TIME% ====>>"%LOGFILE%"
echo Target: %TARGET%>>"%LOGFILE%"
echo Days: %DAYS%>>"%LOGFILE%"

if not exist "%TARGET%" (
    echo ERROR: Directory does not exist: %TARGET%
    echo ERROR: Directory does not exist: %TARGET%>>"%LOGFILE%"
    if "%UNATTENDED%"=="0" pause
    exit /b 1
)

rem Validate that DAYS is a number
echo %DAYS%| findstr /r "^[0-9][0-9]*$" >nul
if errorlevel 1 (
    echo ERROR: Days must be a positive number
    echo ERROR: Days must be a positive number: %DAYS%>>"%LOGFILE%"
    if "%UNATTENDED%"=="0" pause
    exit /b 1
)

echo.
echo Deleting files older than %DAYS% days in: %TARGET%
echo.

if "%UNATTENDED%"=="0" (
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

rem Use forfiles to find and delete files older than specified days
rem /S = recursive, /D = date filter (negative value means older than)
forfiles /P "%TARGET%" /S /D -%DAYS% /C "cmd /c if @isdir==FALSE (del @path && echo Deleted: @path)" >>"%LOGFILE%" 2>nul

if errorlevel 1 (
    echo No files found matching criteria or error occurred.
    echo No files found matching criteria or error occurred.>>"%LOGFILE%"
) else (
    echo.
    echo Done.
)

echo ==== Run finished: %DATE% %TIME% ====>>"%LOGFILE%"

echo.
echo Done. See %LOGFILE% for a record of deleted files.
if "%UNATTENDED%"=="0" pause
