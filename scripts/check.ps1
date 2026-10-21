$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $projectRoot
if (!(Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'Checking a source checkout requires Go on PATH. For a built installation, use start.cmd -check.'
}
Write-Host 'Checking source configuration and local assets. No inference, microphone capture or application-data creation.'
& go run ./cmd/veyra -root $projectRoot @args -check
exit $LASTEXITCODE
