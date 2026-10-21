param(
    [Parameter(Mandatory=$true)][string]$Destination,
    [ValidateSet('chatterbox-nano','chatterbox-turbo','kokoro')][string]$Profile = 'chatterbox-nano',
    [string]$Root = (Split-Path -Parent $PSScriptRoot)
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'asset-bundle.ps1')
$Root = [IO.Path]::GetFullPath($Root)
$assets = Join-Path $Root 'assets'
$Destination = [IO.Path]::GetFullPath($Destination)
if (Test-Path -LiteralPath $Destination) { throw 'Bundle destination must not exist. Existing bundles are never overwritten.' }
if ($Destination.StartsWith($assets + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'Bundle destination must be outside the source assets directory.'
}
$manifestPath = Join-Path $Root "profiles/$Profile.json"
$profileData = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
function Profile-Path([string]$Value) {
    if (!$Value) { throw 'Profile is missing a required asset path.' }
    if ([IO.Path]::IsPathRooted($Value)) { return [IO.Path]::GetFullPath($Value) }
    return [IO.Path]::GetFullPath((Join-Path (Split-Path -Parent $manifestPath) $Value))
}
$sources = @(
    (Split-Path -Parent (Profile-Path $profileData.listening.binary)),
    (Profile-Path $profileData.listening.model),
    (Split-Path -Parent (Profile-Path $profileData.thinking.binary)),
    (Profile-Path $profileData.thinking.model),
    (Profile-Path $profileData.speaking.model),
    (Join-Path $assets 'runtimes/sherpa'),
    (Join-Path $assets 'models/vad')
)
if ($profileData.listening.cpu_binary) { $sources += Split-Path -Parent (Profile-Path $profileData.listening.cpu_binary) }
if ($profileData.speaking.provider -eq 'chatterbox') { $sources += Split-Path -Parent (Profile-Path $profileData.speaking.binary) }
$files = @{}
foreach ($source in $sources) {
    if (!(Test-Path -LiteralPath $source)) { throw "Missing source asset: $source" }
    foreach ($file in Get-ChildItem -LiteralPath $source -File -Recurse -Force) {
        if ($file.FullName -match '[\\/](__pycache__|\.cache)[\\/]' -or $file.Extension -eq '.pyc') { continue }
        if (!$file.FullName.StartsWith($assets + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Asset is outside the installation: $($file.FullName). Use an installation-relative model path for an offline bundle."
        }
        $relative = $file.FullName.Substring($assets.Length + 1).Replace('\', '/')
        Resolve-AssetFile $assets $relative | Out-Null
        $files[$relative] = $file.FullName
    }
}
if (!$files.Count) { throw 'No assets selected.' }
$entries = [Collections.Generic.List[object]]::new()
foreach ($relative in ($files.Keys | Sort-Object)) {
    Write-Progress -Activity 'Packaging verified assets' -Status $relative -PercentComplete (100 * $entries.Count / $files.Count)
    $target = Resolve-AssetFile (Join-Path $Destination 'assets') $relative
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $target) | Out-Null
    Copy-Item -LiteralPath $files[$relative] -Destination $target
    $entry = [ordered]@{path=$relative;size=(Get-Item -LiteralPath $files[$relative]).Length;sha256=(Get-FileHash -LiteralPath $files[$relative] -Algorithm SHA256).Hash.ToLowerInvariant()}
    if (!(Test-AssetFile $target $entry)) { throw "Copy verification failed: $relative. Incomplete bundle retained at $Destination" }
    $entries.Add($entry)
}
[ordered]@{version=1;platform='windows-x86_64';profile=$Profile;files=@($entries.ToArray())} |
    ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $Destination 'manifest.json') -Encoding utf8
Write-Progress -Activity 'Packaging verified assets' -Completed
Write-Host "Offline assets ready: $Destination ($($entries.Count) files). No application data included."
