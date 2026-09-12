# Install pnpm with the official standalone Windows installer.
# https://pnpm.io/installation#on-windows
#Requires -Version 5.1
[CmdletBinding()]
param(
    [ValidatePattern('^\d+(\.\d+\.\d+)?$')]
    [string]$Version = "9.15.9"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
if ($env:OS -ne "Windows_NT") { throw "install-pnpm.ps1 requires Windows" }

$pnpmHome = $env:PNPM_HOME
if ([string]::IsNullOrWhiteSpace($pnpmHome)) {
    $pnpmHome = [Environment]::GetEnvironmentVariable("PNPM_HOME", "User")
}
if ([string]::IsNullOrWhiteSpace($pnpmHome)) {
    $pnpmHome = Join-Path $env:LOCALAPPDATA "pnpm"
}
if (-not [IO.Path]::IsPathRooted($pnpmHome)) { throw "PNPM_HOME must be an absolute path" }
$env:PNPM_HOME = $pnpmHome

$installerFile = Join-Path ([IO.Path]::GetTempPath()) ("playlist-ai-pnpm-" + [guid]::NewGuid() + ".ps1")
$previousVersion = $env:PNPM_VERSION
$previousTLS = [Net.ServicePointManager]::SecurityProtocol
try {
    $env:PNPM_VERSION = $Version
    [Net.ServicePointManager]::SecurityProtocol = $previousTLS -bor [Net.SecurityProtocolType]::Tls12
    Write-Host "==> Install pnpm $Version with https://get.pnpm.io/install.ps1"
    Invoke-WebRequest -Uri "https://get.pnpm.io/install.ps1" -UseBasicParsing -OutFile $installerFile
    # Isolate the upstream script from our strict mode and caller variables.
    # Use this PowerShell edition, including 5.1 on contributor machines.
    $powerShellExe = (Get-Process -Id $PID).Path
    & $powerShellExe -NoProfile -ExecutionPolicy Bypass -File $installerFile
    if ($LASTEXITCODE -ne 0) { throw "Official pnpm installer failed (exit $LASTEXITCODE)" }
} finally {
    $env:PNPM_VERSION = $previousVersion
    [Net.ServicePointManager]::SecurityProtocol = $previousTLS
    Remove-Item -LiteralPath $installerFile -Force -ErrorAction SilentlyContinue
}

# The official installer updates the user environment, not this process or
# subsequent Actions steps. Prepend only PNPM_HOME; retain the selected Go/Node
# toolchains and avoid rebuilding PATH from machine-wide registry values.
$pnpmExe = Join-Path $pnpmHome "pnpm.exe"
if (-not (Test-Path -LiteralPath $pnpmExe -PathType Leaf)) {
    throw "Official installer did not produce $pnpmExe. Check its output and Windows Security protection history, then retry."
}
$actualVersion = (& $pnpmExe --version | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or $actualVersion -notmatch '^\d+\.\d+\.\d+$') {
    throw "Installed pnpm failed its version health check: $actualVersion"
}
$versionMatches = if ($Version.Contains('.')) { $actualVersion -eq $Version } else { $actualVersion.Split('.')[0] -eq $Version }
if (-not $versionMatches) { throw "Expected pnpm $Version, found $actualVersion at $pnpmExe" }
$env:Path = "$pnpmHome$([IO.Path]::PathSeparator)$env:Path"
if ($env:GITHUB_PATH) { Add-Content -LiteralPath $env:GITHUB_PATH -Value $pnpmHome -Encoding utf8 }
if ($env:GITHUB_ENV) { Add-Content -LiteralPath $env:GITHUB_ENV -Value "PNPM_HOME=$pnpmHome" -Encoding utf8 }
Write-Host " PASS  pnpm $actualVersion ($pnpmExe)" -ForegroundColor Green
