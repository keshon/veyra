$ErrorActionPreference = 'Stop'
$scripts = Split-Path -Parent $PSScriptRoot
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('veyra-bootstrap-test-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $testRoot | Out-Null
function Write-Fixture([string]$Path, [string]$Text) {
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Path) | Out-Null
    [IO.File]::WriteAllText($Path, $Text)
}
function Assert-Failure([scriptblock]$Action) {
    $failed = $false
    try { & $Action } catch { $failed = $true }
    if (!$failed) { throw 'Expected a rejected operation.' }
}
try {
    $source = Join-Path $testRoot 'source with spaces'
    $assetFiles = @(
        'runtimes/whisper-cuda/Release/whisper-server.exe', 'runtimes/whisper-cuda/Release/runtime.dll',
        'runtimes/whisper/Release/whisper-server.exe', 'runtimes/llamacpp/llama-server.exe',
        'runtimes/llamacpp/LICENSE', 'runtimes/chatterbox/python.exe', 'runtimes/chatterbox/Lib/worker.py',
        'runtimes/sherpa/include/sherpa-onnx/c-api/c-api.h', 'runtimes/sherpa/lib/runtime.dll',
        'models/whisper/small.bin', 'models/generation/model.gguf', 'models/vad/silero_vad.onnx',
        'models/chatterbox/nano/weights.bin', 'models/chatterbox/turbo/weights.bin', 'models/kokoro/model.onnx'
    )
    foreach ($file in $assetFiles) { Write-Fixture (Join-Path $source "assets/$file") "fixture $file" }
    Write-Fixture (Join-Path $source '.veyra/conversations/private.json') 'private'
    foreach ($name in @('chatterbox-nano','chatterbox-turbo','kokoro')) {
        $model = if ($name -eq 'kokoro') { '../assets/models/kokoro' } else { '../assets/models/chatterbox/' + $name.Split('-')[-1] }
        $provider = if ($name -eq 'kokoro') { 'kokoro' } else { 'chatterbox' }
        $profile = @{
            listening=@{binary='../assets/runtimes/whisper-cuda/Release/whisper-server.exe';cpu_binary='../assets/runtimes/whisper/Release/whisper-server.exe';model='../assets/models/whisper/small.bin'}
            thinking=@{binary='../assets/runtimes/llamacpp/llama-server.exe';model='../assets/models/generation/model.gguf'}
            speaking=@{provider=$provider;binary='../assets/runtimes/chatterbox/python.exe';model=$model}
        }
        Write-Fixture (Join-Path $source "profiles/$name.json") ($profile | ConvertTo-Json -Depth 5)
        $bundle = Join-Path $testRoot "bundle-$name"
        & (Join-Path $scripts 'bundle-assets.ps1') -Root $source -Destination $bundle -Profile $name
        $clone = Join-Path $testRoot "clone-$name"
        Write-Fixture (Join-Path $clone 'go.mod') 'module fixture'
        & (Join-Path $scripts 'bootstrap.ps1') -Root $clone -Bundle $bundle
        & (Join-Path $scripts 'bootstrap.ps1') -Root $clone -Bundle $bundle
        $manifest = Get-Content -LiteralPath (Join-Path $bundle 'manifest.json') -Raw | ConvertFrom-Json
        foreach ($entry in $manifest.files) {
            if (!(Test-Path -LiteralPath (Join-Path $clone "assets/$($entry.path)"))) { throw 'Incomplete bootstrap.' }
            if ($entry.path -match 'private|conversations') { throw 'User data included in asset bundle.' }
            if ($name -eq 'chatterbox-nano' -and $entry.path -match 'models/chatterbox/turbo') { throw 'Unused Turbo model included.' }
        }
    }
    $bundle = Join-Path $testRoot 'bundle-chatterbox-nano'
    $manifestPath = Join-Path $bundle 'manifest.json'
    $original = [IO.File]::ReadAllText($manifestPath)
    $manifest = $original | ConvertFrom-Json
    $clone = Join-Path $testRoot 'conflict'
    Write-Fixture (Join-Path $clone 'go.mod') 'module fixture'
    $conflict = Join-Path $clone "assets/$($manifest.files[0].path)"
    Write-Fixture $conflict 'existing personal asset'
    Assert-Failure { & (Join-Path $scripts 'bootstrap.ps1') -Root $clone -Bundle $bundle }
    if ([IO.File]::ReadAllText($conflict) -ne 'existing personal asset') { throw 'Existing asset was overwritten.' }
    if (@(Get-ChildItem -LiteralPath (Join-Path $clone 'assets') -File -Recurse).Count -ne 1) { throw 'Copied before validating conflicts.' }
    $clone = Join-Path $testRoot 'invalid'
    Write-Fixture (Join-Path $clone 'go.mod') 'module fixture'
    foreach ($badPath in @('../escape.bin', 'runtimes/../escape.bin', 'runtimes/file.bin.', 'C:\escape.bin')) {
        $manifest.files[0].path = $badPath
        Write-Fixture $manifestPath ($manifest | ConvertTo-Json -Depth 5)
        Assert-Failure { & (Join-Path $scripts 'bootstrap.ps1') -Root $clone -Bundle $bundle }
    }
    Write-Fixture $manifestPath $original
    $manifest = $original | ConvertFrom-Json
    $manifest.files = @($manifest.files) + @($manifest.files[0])
    Write-Fixture $manifestPath ($manifest | ConvertTo-Json -Depth 5)
    Assert-Failure { & (Join-Path $scripts 'bootstrap.ps1') -Root $clone -Bundle $bundle }
    Write-Fixture $manifestPath $original
    $manifest = $original | ConvertFrom-Json
    $outside = Join-Path $testRoot 'junction-target'
    New-Item -ItemType Directory -Path $outside | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $clone 'assets') | Out-Null
    $link = Join-Path $clone 'assets/runtimes'
    New-Item -ItemType Junction -Path $link -Target $outside | Out-Null
    try { Assert-Failure { & (Join-Path $scripts 'bootstrap.ps1') -Root $clone -Bundle $bundle } }
    finally { [IO.Directory]::Delete($link) }
    if (@(Get-ChildItem -LiteralPath $outside -Recurse).Count) { throw 'Bootstrap followed a junction.' }
    $clone = Join-Path $testRoot 'corrupt'
    Write-Fixture (Join-Path $clone 'go.mod') 'module fixture'
    Write-Fixture (Join-Path $bundle "assets/$($manifest.files[0].path)") 'corrupted download'
    Assert-Failure { & (Join-Path $scripts 'bootstrap.ps1') -Root $clone -Bundle $bundle }
    if (Test-Path -LiteralPath (Join-Path $clone 'assets')) { throw 'Invalid bundle created destination assets.' }
    Write-Host 'PASS: all three profiles, paths with spaces, repeated bootstrap, hashes, conflict preservation, traversal/duplicate/junction rejection and user-data exclusion. No inference or real asset copies.'
} finally {
    $resolved = [IO.Path]::GetFullPath($testRoot)
    $temp = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\', '/')
    if (!$resolved.StartsWith($temp + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase) -or
        (Split-Path -Leaf $resolved) -notmatch '^veyra-bootstrap-test-[a-f0-9]{32}$') { throw 'Refusing to remove an unexpected test directory.' }
    Remove-Item -LiteralPath $resolved -Recurse -Force
}
