@echo off

powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\check-chatterbox.ps1" %*

exit /b %errorlevel%
