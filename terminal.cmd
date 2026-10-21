@echo off
setlocal
call "%~dp0start.cmd" -tui -character profiles/characters/veyra.json %*
exit /b %errorlevel%
