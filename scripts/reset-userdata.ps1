# Remove Playlist AI per-user state so the next launch starts the setup wizard.
#Requires -Version 5.1
[CmdletBinding()]
param(
    [Alias("y")][switch]$Yes,
    [switch]$DryRun
)

. (Join-Path $PSScriptRoot "_common.ps1")
if ($env:OS -ne "Windows_NT") { throw "reset-userdata.ps1 is for Windows; use reset-userdata.sh on Linux or macOS" }

$targets = [Collections.Generic.List[string]]::new()
function Add-Target([string]$Path) {
    if ([string]::IsNullOrWhiteSpace($Path)) { return }
    $fullPath = [IO.Path]::GetFullPath($Path).TrimEnd('\')
    $protected = @(
        [IO.Path]::GetPathRoot($fullPath).TrimEnd('\'),
        [IO.Path]::GetFullPath($HOME).TrimEnd('\'),
        [IO.Path]::GetFullPath($RepoRoot).TrimEnd('\'),
        [IO.Path]::GetFullPath($env:APPDATA).TrimEnd('\'),
        [IO.Path]::GetFullPath($env:LOCALAPPDATA).TrimEnd('\')
    )
    if ($protected -contains $fullPath) {
        Write-Skip "refusing unsafe reset target: $fullPath"
        return
    }
    $targets.Add($fullPath)
}

Add-Target (Join-Path $env:APPDATA "playlist-ai")
Add-Target (Join-Path $HOME ".playlist-ai")
Add-Target (Join-Path $RepoRoot "playlist-ai-data")
Add-Target (Join-Path $env:LOCALAPPDATA "llama-app")

if ($env:PLAYLISTAI_CONFIG -and (Test-Path -LiteralPath $env:PLAYLISTAI_CONFIG -PathType Leaf)) {
    $match = Select-String -LiteralPath $env:PLAYLISTAI_CONFIG -Pattern '^\s*data_dir\s*=\s*"([^"]+)"' | Select-Object -First 1
    if ($match) { Add-Target $match.Matches[0].Groups[1].Value }
}

$seen = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
$existing = @($targets | Where-Object { $seen.Add($_) -and (Test-Path -LiteralPath $_) })
if ($existing.Count -eq 0) {
    Write-Pass "nothing to remove - Playlist AI has no user data on this machine"
    exit 0
}

Write-Info "will remove:"
foreach ($target in $existing) { Write-Host "        $target" }
if ($DryRun) {
    Write-Skip "-DryRun: nothing deleted"
    exit 0
}

if (-not $Yes) {
    $reply = Read-Host "Delete these paths? [y/N]"
    if ($reply -notmatch '(?i)^(y|yes)$') { throw "aborted" }
}

foreach ($target in $existing) {
    Remove-Item -LiteralPath $target -Recurse -Force
    Write-Pass "removed $target"
}
Write-Pass "done - the next launch will start the setup wizard"
