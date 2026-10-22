$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $projectRoot
$appBinary = Join-Path $projectRoot 'bin\veyra.exe'
$nextBinary = Join-Path $projectRoot 'bin\veyra-next.exe'
if ((Test-Path -LiteralPath $nextBinary) -and (!(Test-Path -LiteralPath $appBinary) -or (Get-Item -LiteralPath $nextBinary).LastWriteTimeUtc -ge (Get-Item -LiteralPath $appBinary).LastWriteTimeUtc)) { $appBinary = $nextBinary }
if (!(Test-Path -LiteralPath $appBinary -PathType Leaf)) { throw 'Veyra is not built. Run build.cmd (browser) or build-terminal.cmd (terminal) after installing the documented assets.' }
Write-Host 'Starting Veyra...'
& $appBinary -root $projectRoot @args
exit $LASTEXITCODE
