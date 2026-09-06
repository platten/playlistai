# Shared setup for scripts/*.ps1. Dot-source this file; do not run it directly.
#Requires -Version 5.1

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$script:RepoRoot = (Get-Item (Split-Path -Parent $PSScriptRoot)).FullName
Set-Location $script:RepoRoot

$script:GoVersionMin = [version]"1.27"
$script:NodeVersionMin = [version]"22.0"
$script:PnpmVersion = "9"
$script:Wails3Version = "v3.0.0-beta.16"
$script:GolangCILintVersion = "v2.13.2"

function Write-Info([string]$Message) { Write-Host "==> $Message" -ForegroundColor Cyan }
function Write-Pass([string]$Message) { Write-Host " PASS  $Message" -ForegroundColor Green }
function Write-Skip([string]$Message) { Write-Host " skip  $Message" -ForegroundColor Yellow }
function Write-Fail([string]$Message) { Write-Host " FAIL  $Message" -ForegroundColor Red }

function Test-Command([string]$Name) {
    return $null -ne (Get-Command $Name -ErrorAction SilentlyContinue)
}

function Assert-Command([string]$Name, [string]$Hint) {
    if (-not (Test-Command $Name)) { throw "$Name not found - $Hint" }
}

function Get-NumericVersion([string]$Text) {
    if ($Text -match '(\d+(?:\.\d+){1,3})') { return [version]$Matches[1] }
    return [version]"0.0"
}

# go.mod pins the language version, so a too-old toolchain fails every Go step
# at once. Report it once, with the version and the binary actually resolved,
# instead of leaving the caller to infer it from a wall of downstream failures.
function Assert-GoVersion {
    $found = Get-NumericVersion (& go version)
    if ($found -lt $script:GoVersionMin) {
        $resolved = (Get-Command go -ErrorAction SilentlyContinue).Source
        $where = if ([string]::IsNullOrWhiteSpace($resolved)) { "" } else { " at $resolved" }
        throw "Go $($script:GoVersionMin)+ required, found $found$where - run scripts/setup.ps1, or put the intended toolchain first on PATH"
    }
}

# Pick up tools installed after this process started, without disturbing the
# toolchain the caller already chose.
#
# Order matters. CI (actions/setup-go), version managers and developer shells
# all select a toolchain by putting it first on the *process* PATH. Rebuilding
# PATH with the registry values in front silently swaps in the machine-wide
# copy instead — which is how CI ended up running the runner image's Go 1.24
# against a go.mod that requires 1.27. So the process PATH keeps precedence and
# the registry values are only appended. Entries are de-duplicated so repeated
# calls cannot grow PATH without bound.
function Update-ProcessPath {
    $sources = @(
        $env:Path,
        [Environment]::GetEnvironmentVariable("Path", "Machine"),
        [Environment]::GetEnvironmentVariable("Path", "User")
    )
    $seen = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    $ordered = [Collections.Generic.List[string]]::new()
    foreach ($source in $sources) {
        if ([string]::IsNullOrWhiteSpace($source)) { continue }
        foreach ($entry in $source -split [IO.Path]::PathSeparator) {
            $trimmed = $entry.Trim()
            if ([string]::IsNullOrWhiteSpace($trimmed)) { continue }
            if ($seen.Add($trimmed.TrimEnd('\', '/'))) { $ordered.Add($trimmed) }
        }
    }
    $env:Path = $ordered -join [IO.Path]::PathSeparator
}

function Add-GoBinToPath {
    if (-not (Test-Command "go")) { return }
    $goPath = (& go env GOPATH 2>$null).Trim()
    if ([string]::IsNullOrWhiteSpace($goPath)) { return }
    $goBin = Join-Path $goPath "bin"
    $pathParts = $env:Path -split [IO.Path]::PathSeparator
    if ($pathParts -notcontains $goBin) { $env:Path = "$env:Path$([IO.Path]::PathSeparator)$goBin" }
}

function Add-NSISToPath {
    if (Test-Command "makensis") { return }
    $roots = @(${env:ProgramFiles(x86)}, $env:ProgramFiles) | Where-Object { $_ }
    foreach ($root in $roots) {
        $nsisDir = Join-Path $root "NSIS"
        if (Test-Path (Join-Path $nsisDir "makensis.exe")) {
            $env:Path = "$env:Path$([IO.Path]::PathSeparator)$nsisDir"
            return
        }
    }
}

Update-ProcessPath
Add-GoBinToPath
Add-NSISToPath
