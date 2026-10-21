# Shared path validation for the two offline asset-bundle commands.
function Resolve-AssetFile([string]$Base, [string]$Relative) {
    if (!$Relative -or [IO.Path]::IsPathRooted($Relative) -or $Relative.Contains(':') -or $Relative -match '(^|[\\/])\.\.?([\\/]|$)') {
        throw "Invalid bundle path: $Relative"
    }
    if ($Relative -match '[<>"|?*]' -or ($Relative -split '[\\/]' | Where-Object { $_ -match '[. ]$' })) {
        throw "Ambiguous Windows bundle path: $Relative"
    }
    $basePath = [IO.Path]::GetFullPath($Base).TrimEnd('\', '/')
    $path = [IO.Path]::GetFullPath((Join-Path $basePath $Relative))
    if (!$path.StartsWith($basePath + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Bundle path escapes its directory: $Relative"
    }
    # Existing junctions or symbolic links could redirect an otherwise valid path.
    for ($parent = $path; $parent; $parent = Split-Path -Parent $parent) {
        if (Test-Path -LiteralPath $parent) {
            if ((Get-Item -LiteralPath $parent -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) {
                throw "Bundle paths must not pass through a link: $parent"
            }
        }
        if ($parent -eq [IO.Path]::GetPathRoot($parent)) { break }
    }
    return $path
}

function Test-AssetFile([string]$Path, $Entry) {
    if (!(Test-Path -LiteralPath $Path -PathType Leaf)) { return $false }
    return (Get-Item -LiteralPath $Path).Length -eq $Entry.size -and
        (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash -eq $Entry.sha256
}
