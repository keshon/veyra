param(
  [Parameter(Mandatory=$true)][string]$PythonHome,
  [Parameter(Mandatory=$true)][string]$SitePackages,
  [Parameter(Mandatory=$true)][string]$Models,
  [string]$Destination = ''
)
$ErrorActionPreference = 'Stop'
function Get-BundleHash([string]$Path) {
 $stream = [IO.File]::OpenRead($Path)
 $sha = [Security.Cryptography.SHA256]::Create()
 try { return ([BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-','').ToLowerInvariant() }
 finally { $stream.Dispose(); $sha.Dispose() }
}
if (!$Destination) { $Destination = Join-Path $PSScriptRoot '../assets' }
$Destination = [IO.Path]::GetFullPath($Destination)
$target = Join-Path $Destination 'runtimes/chatterbox'
if (Test-Path $target) { throw "Destination exists: $target. Verify or choose a new destination; existing runtimes are not replaced." }
if (!(Test-Path (Join-Path $PythonHome 'python.exe'))) { throw 'PythonHome must contain a standalone python.exe' }
if (!(Test-Path (Join-Path $SitePackages 'torch'))) { throw 'SitePackages must contain the prepared CUDA dependencies' }
foreach ($variant in @('nano','turbo')) {
 if (!(Test-Path (Join-Path $Models "$variant/ve.safetensors"))) { throw "Missing $variant model assets" }
 if (Test-Path (Join-Path $Destination "models/chatterbox/$variant")) { throw "Model destination already exists: $variant" }
}
$stage = Join-Path $Destination ('runtimes/chatterbox-stage-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $stage -Force | Out-Null
Copy-Item -LiteralPath (Join-Path $PythonHome 'python.exe') -Destination $stage
foreach ($item in Get-ChildItem -LiteralPath $PythonHome) {
 if ($item.Name -eq 'python.exe' -or $item.Name -eq 'Scripts') { continue }
 Copy-Item -LiteralPath $item.FullName -Destination $stage -Recurse
}
$packages = Join-Path $stage 'Lib/site-packages'
New-Item -ItemType Directory -Path $packages -Force | Out-Null
foreach ($item in Get-ChildItem -LiteralPath $SitePackages) {
 Copy-Item -LiteralPath $item.FullName -Destination $packages -Recurse -Force
}
# Isolated mode ignores PYTHONPATH and user packages. The runtime resolves its
# standard library and dependencies relative to python.exe, without a venv.
& (Join-Path $stage 'python.exe') -I -c "import sys,torch,torchaudio; from chatterbox.tts_turbo import ChatterboxTurboTTS; print(sys.prefix); print(torch.__version__)"
if ($LASTEXITCODE) { throw "Runtime import failed; staged files retained at $stage" }
Move-Item -LiteralPath $stage -Destination $target
foreach ($variant in @('nano','turbo')) {
 $modelTarget = Join-Path $Destination "models/chatterbox/$variant"
 New-Item -ItemType Directory -Path $modelTarget -Force | Out-Null
 foreach ($item in Get-ChildItem -LiteralPath (Join-Path $Models $variant)) {
  if ($item.Name -eq '.cache') { continue }
  Copy-Item -LiteralPath $item.FullName -Destination $modelTarget -Recurse
 }
}
Write-Host 'Models copied; hashing bundle files...'
$files = @()
foreach ($root in @($target, (Join-Path $Destination 'models/chatterbox'))) {
 foreach ($file in Get-ChildItem -LiteralPath $root -Recurse -File) {
  if ($file.FullName -match '__pycache__' -or $file.Extension -eq '.pyc') { continue }
  $files += [ordered]@{path=$file.FullName.Substring($Destination.Length+1).Replace('\','/'); size=$file.Length; sha256=(Get-BundleHash $file.FullName)}
 }
}
[ordered]@{version=1; platform='windows-x86_64'; source='locally promoted tested installation'; files=$files} | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $Destination 'chatterbox-manifest.json') -Encoding utf8
Write-Host "Installed independent runtime: $target"
