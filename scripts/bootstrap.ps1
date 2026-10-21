param(
    [Parameter(Mandatory=$true)][string]$Bundle,
    [string]$Root = (Split-Path -Parent $PSScriptRoot)
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'asset-bundle.ps1')
$Bundle = [IO.Path]::GetFullPath($Bundle)
$Root = [IO.Path]::GetFullPath($Root)
if (!(Test-Path -LiteralPath (Join-Path $Root 'go.mod') -PathType Leaf)) { throw 'Destination must be a Veyra source checkout.' }
$manifest = Get-Content -LiteralPath (Join-Path $Bundle 'manifest.json') -Raw | ConvertFrom-Json
if ($manifest.version -ne 1 -or $manifest.platform -ne 'windows-x86_64' -or
    $manifest.profile -notin @('chatterbox-nano','chatterbox-turbo','kokoro') -or !$manifest.files.Count) {
    throw 'Unsupported or empty asset bundle.'
}
$seen = @{}
# Validate all sources and existing destinations before copying anything.
foreach ($entry in $manifest.files) {
    if ($entry.path -isnot [string] -or
        !($entry.size -is [int] -or $entry.size -is [long]) -or $entry.size -lt 0 -or
        $entry.sha256 -notmatch '^[a-fA-F0-9]{64}$') { throw 'Invalid or duplicate asset manifest entry.' }
    $source = Resolve-AssetFile (Join-Path $Bundle 'assets') $entry.path
    $target = Resolve-AssetFile (Join-Path $Root 'assets') $entry.path
    if ($seen.ContainsKey($target)) { throw "Duplicate bundle destination: $($entry.path)" }
    $seen[$target] = $true
    Write-Progress -Activity 'Verifying offline bundle' -Status $entry.path -PercentComplete (100 * $seen.Count / $manifest.files.Count)
    if (!(Test-AssetFile $source $entry)) { throw "Missing or corrupt bundle file: $($entry.path)" }
    if ((Test-Path -LiteralPath $target) -and !(Test-AssetFile $target $entry)) {
        throw "Existing asset differs: $target. Back it up or choose a fresh checkout; nothing was replaced."
    }
}
Write-Progress -Activity 'Verifying offline bundle' -Completed
foreach ($entry in $manifest.files) {
    $source = Resolve-AssetFile (Join-Path $Bundle 'assets') $entry.path
    $target = Resolve-AssetFile (Join-Path $Root 'assets') $entry.path
    if (Test-Path -LiteralPath $target) { continue }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $target) | Out-Null
    $partial = $target + '.' + [guid]::NewGuid().ToString('N') + '.part'
    Copy-Item -LiteralPath $source -Destination $partial
    if (!(Test-AssetFile $partial $entry)) { throw "Copy verification failed; partial file retained: $partial" }
    [IO.File]::Move($partial, $target)
}
Write-Host "Assets installed and verified for $($manifest.profile). Existing matching files were reused."
Write-Host "Next: build.cmd, then check.cmd -profile $($manifest.profile), then start.cmd -profile $($manifest.profile)"
Write-Host 'No tools installed, no models started, no user data moved.'
