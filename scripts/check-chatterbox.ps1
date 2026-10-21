param([string]$Assets = '')
$ErrorActionPreference = 'Stop'
function Get-BundleHash([string]$Path) {
 $stream = [IO.File]::OpenRead($Path)
 $sha = [Security.Cryptography.SHA256]::Create()
 try { return ([BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-','').ToLowerInvariant() }
 finally { $stream.Dispose(); $sha.Dispose() }
}
if (!$Assets) { $Assets = Join-Path $PSScriptRoot '../assets' }
$Assets = [IO.Path]::GetFullPath($Assets)
$manifest = Get-Content -LiteralPath (Join-Path $Assets 'chatterbox-manifest.json') -Raw | ConvertFrom-Json
if ($manifest.version -ne 1 -or $manifest.platform -ne 'windows-x86_64' -or !$manifest.files.Count) { throw 'Unsupported or empty bundle manifest' }
foreach ($file in $manifest.files) {
 $path = [IO.Path]::GetFullPath((Join-Path $Assets $file.path))
 if (!$path.StartsWith($Assets + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Manifest path escapes assets directory' }
 if (!(Test-Path -LiteralPath $path)) { throw "Missing bundle file: $($file.path)" }
 if ((Get-Item -LiteralPath $path).Length -ne $file.size -or (Get-BundleHash $path) -ne $file.sha256) { throw "Bundle checksum mismatch: $($file.path)" }
}
& (Join-Path $Assets 'runtimes/chatterbox/python.exe') -I -c "import sys,torch,torchaudio; from chatterbox.tts_turbo import ChatterboxTurboTTS; print(sys.prefix); print(torch.__version__)"
if ($LASTEXITCODE) { throw 'Runtime import check failed' }
Write-Host 'Chatterbox bundle checks passed. GPU generation and microphone acceptance are separate.'
