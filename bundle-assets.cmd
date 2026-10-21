@echo off
setlocal
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\bundle-assets.ps1" %*
exit /b %errorlevel%
