$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $projectRoot
foreach ($tool in @('go', 'npm', 'gcc')) {
    if (!(Get-Command $tool -ErrorAction SilentlyContinue)) { throw "Build requires $tool on PATH. See README source-checkout requirements." }
}
if (!(Test-Path -LiteralPath 'assets/runtimes/sherpa/include/sherpa-onnx/c-api/c-api.h')) {
    throw 'Missing Sherpa development headers under assets/runtimes/sherpa/include. Install the documented assets before building.'
}
$env:GOCACHE = Join-Path $projectRoot '.cache\go'
$env:GOTMPDIR = Join-Path $projectRoot '.cache\tmp'
New-Item -ItemType Directory -Force $env:GOTMPDIR | Out-Null
Push-Location (Join-Path $projectRoot 'web')
try {
    npm ci
    if ($LASTEXITCODE -ne 0) { throw 'Frontend dependency installation failed' }
    npm run check
    if ($LASTEXITCODE -ne 0) { throw 'Frontend checks failed' }
    npm run build
    if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }
} finally { Pop-Location }
New-Item -ItemType Directory -Force (Join-Path $projectRoot 'bin') | Out-Null
& gcc -O2 -Wall -Wextra -Werror -static-libgcc -I assets/runtimes/sherpa/include internal/provider/kokoro/native/worker.c -o bin/kokoro-worker.exe
if ($LASTEXITCODE -ne 0) { throw 'Kokoro worker build failed; stop active voice processes before rebuilding' }
$vadTarget = Join-Path $projectRoot 'bin\vad-worker.exe'
$runningVAD = Get-CimInstance Win32_Process -Filter "Name = 'vad-worker.exe'" | Where-Object { $_.ExecutablePath -eq $vadTarget }
if ($runningVAD) { throw 'Stop Veyra before rebuilding its speech detector.' }
& gcc -O2 -Wall -Wextra -Werror -static-libgcc -I assets/runtimes/sherpa/include internal/provider/vad/native/worker.c -o $vadTarget
if ($LASTEXITCODE -ne 0) { throw 'VAD worker build failed' }
go test ./cmd/... ./internal/...
if ($LASTEXITCODE -ne 0) { throw 'Tests failed' }
$targetBinary = 'bin\veyra.exe'
$runningApp = Get-CimInstance Win32_Process -Filter "Name = 'veyra.exe'" | Where-Object { $_.ExecutablePath -eq (Join-Path $projectRoot 'bin\veyra.exe') }
if ($runningApp) { $targetBinary = 'bin\veyra-next.exe' }
New-Item -ItemType Directory -Force (Join-Path $projectRoot 'bin') | Out-Null
go build -o $targetBinary ./cmd/veyra
if ($LASTEXITCODE -ne 0) { throw 'Build failed' }
