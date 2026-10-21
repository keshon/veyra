$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $projectRoot
$revision = 'db25077c33951fe163b42986fba0132e279872a2'
$manifest = Get-Content -LiteralPath profiles\minicpm-weights.json -Raw | ConvertFrom-Json
foreach ($entry in $manifest) {
    $path = Join-Path $projectRoot ('assets\models\minicpm\' + $entry.path)
    New-Item -ItemType Directory -Force (Split-Path $path) | Out-Null
    if ((Test-Path -LiteralPath $path) -and (Get-Item -LiteralPath $path).Length -eq $entry.size) {
        if ((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() -eq $entry.sha256) {
            Write-Host ('Verified: ' + $entry.path)
            continue
        }
        throw ('Checksum mismatch: ' + $path + '. Preserve or rename it before downloading again.')
    }
    Write-Host ('Downloading: ' + $entry.path)
    & curl.exe -L --fail --connect-timeout 30 --speed-time 120 --speed-limit 1024 --retry 5 -C - -o $path ('https://huggingface.co/openbmb/MiniCPM-o-4_5-gguf/resolve/' + $revision + '/' + $entry.path + '?download=true')
    if ($LASTEXITCODE -ne 0) { throw ('Download failed: ' + $entry.path + '. Re-run to resume.') }
    if ((Get-Item -LiteralPath $path).Length -ne $entry.size -or (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() -ne $entry.sha256) {
        throw ('Checksum/size mismatch: ' + $path)
    }
}
