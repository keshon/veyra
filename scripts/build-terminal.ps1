$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $projectRoot
foreach ($tool in @('go', 'gcc')) {
    if (!(Get-Command $tool -ErrorAction SilentlyContinue)) { throw "Terminal build requires $tool on PATH. See README source-checkout requirements." }
}
if (!(Test-Path -LiteralPath 'assets/runtimes/sherpa/include/sherpa-onnx/c-api/c-api.h')) {
    throw 'Missing Sherpa development headers under assets/runtimes/sherpa/include. Install the documented assets before building.'
}
New-Item -ItemType Directory -Force (Join-Path $projectRoot 'bin') | Out-Null
$workerTarget = Join-Path $projectRoot 'bin\kokoro-worker.exe'
$runningWorker = Get-CimInstance Win32_Process -Filter "Name = 'kokoro-worker.exe'" | Where-Object { $_.ExecutablePath -eq $workerTarget }
if ($runningWorker) { throw 'Stop Veyra before rebuilding its native worker.' }
& gcc -O2 -Wall -Wextra -Werror -static-libgcc -I assets/runtimes/sherpa/include internal/provider/kokoro/native/worker.c -o $workerTarget
if ($LASTEXITCODE -ne 0) { throw 'Kokoro worker build failed' }
$vadTarget = Join-Path $projectRoot 'bin\vad-worker.exe'
$runningVAD = Get-CimInstance Win32_Process -Filter "Name = 'vad-worker.exe'" | Where-Object { $_.ExecutablePath -eq $vadTarget }
if ($runningVAD) { throw 'Stop Veyra before rebuilding its speech detector.' }
& gcc -O2 -Wall -Wextra -Werror -static-libgcc -I assets/runtimes/sherpa/include internal/provider/vad/native/worker.c -o $vadTarget
if ($LASTEXITCODE -ne 0) { throw 'VAD worker build failed' }
go test ./cmd/... ./internal/...
if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
$targetBinary = 'bin\veyra.exe'
if (Get-Process veyra -ErrorAction SilentlyContinue) { $targetBinary = 'bin\veyra-next.exe' }
go build -o $targetBinary ./cmd/veyra
if ($LASTEXITCODE -ne 0) { throw 'Build failed' }
