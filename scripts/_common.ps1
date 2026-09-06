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

function Update-ProcessPath {
    $machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $parts = @($machinePath, $userPath, $env:Path) | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }
    $env:Path = $parts -join [IO.Path]::PathSeparator
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
