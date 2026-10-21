@echo off

powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\promote-chatterbox.ps1" %*

exit /b %errorlevel%
