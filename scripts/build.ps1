# Build and package Playlist AI natively on Windows. Artifacts land in .\bin.
#Requires -Version 5.1
[CmdletBinding()]
param(
    [ValidateSet("amd64", "arm64", "all")]
    [string]$Architecture = $(if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" })
)

. (Join-Path $PSScriptRoot "_common.ps1")
if ($env:OS -ne "Windows_NT") { throw "build.ps1 is for Windows; use build.sh on Linux or macOS" }

Assert-Command "go" "run scripts/setup.ps1"
Assert-GoVersion
Assert-Command "node" "run scripts/setup.ps1"
Assert-Command "pnpm" "run scripts/setup.ps1"
Assert-Command "wails3" "run scripts/setup.ps1"
Add-NSISToPath
Assert-Command "makensis" "run scripts/setup.ps1 to install NSIS"

$architectures = if ($Architecture -eq "all") { @("amd64", "arm64") } else { @($Architecture) }
foreach ($arch in $architectures) {
    $compiler = & (Join-Path $PSScriptRoot "install-clap-toolchain.ps1") -Architecture $arch -CheckOnly
    Write-Info "package Windows NSIS installer ($arch)"
    & wails3 task windows:package "ARCH=$arch" "CGO_ENABLED=1" "CC=$compiler"
    if ($LASTEXITCODE -ne 0) { throw "Windows $arch package failed" }
    $buildInfo = (& go version -m (Join-Path $RepoRoot "bin\playlist-ai.exe") | Out-String)
    if ($LASTEXITCODE -ne 0 -or $buildInfo -notmatch 'CGO_ENABLED=1' -or $buildInfo -notmatch "GOARCH=$arch") { throw "Packaged application lacks the expected native build configuration" }
    if ($arch -eq "amd64" -or $env:PROCESSOR_ARCHITECTURE -eq "ARM64") {
        $check = Start-Process -FilePath (Join-Path $RepoRoot "bin\playlist-ai.exe") -ArgumentList "--check-audio-worker" -PassThru
        try {
            if (-not $check.WaitForExit(15000)) { $check.Kill(); throw "Native CLAP capability check timed out" }
            if ($check.ExitCode -ne 0) { throw "Packaged application is missing native CLAP support" }
        } finally { $check.Dispose() }
    }
    Write-Pass "Windows $arch package"
}

Write-Info "artifacts in .\bin"
$artifactDir = Join-Path $RepoRoot "bin"
if (Test-Path $artifactDir) {
    $artifacts = @(Get-ChildItem $artifactDir -File | Where-Object {
        $_.Name -match '(installer\.exe|\.msi$|\.msix$|\.zip$|\.tar\.gz$)'
    })
    if ($artifacts.Count -gt 0) {
        $artifacts | ForEach-Object { Write-Host "        $($_.Name)  $($_.Length) bytes" }
    } else {
        Write-Skip "no Windows package artifacts found"
    }
}
