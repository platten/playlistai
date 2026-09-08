# Offline failure-path checks for the Windows installer wrapper.
#Requires -Version 5.1
Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$fixtureRoot = Join-Path ([IO.Path]::GetTempPath()) ("playlist-pnpm-test-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $fixtureRoot | Out-Null
$savedEnvironment = @{}
foreach ($name in @("PNPM_HOME", "PNPM_VERSION", "GITHUB_PATH", "GITHUB_ENV")) {
    $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
}
$originalPath = $env:Path
$installerState = @{ Case = ""; Download = "" }

# Only substitute the network response; run the actual wrapper and child
# PowerShell process. No network, user registry changes, or downloads needed.
function Invoke-WebRequest {
    param([string]$Uri, [switch]$UseBasicParsing, [string]$OutFile)
    if ($Uri -ne "https://get.pnpm.io/install.ps1") { throw "Unexpected installer URL: $Uri" }
    $installerState.Download = $OutFile
    if ($installerState.Case -eq "download") {
        Set-Content -LiteralPath $OutFile -Value "# partial download"
        throw "fixture download failure"
    }
    $content = if ($installerState.Case -eq "exit") { "exit 23" } else { "# installer produced no executable" }
    Set-Content -LiteralPath $OutFile -Value $content
}

try {
    foreach ($case in @(
        @{ Name = "download"; Message = "fixture download failure" },
        @{ Name = "exit"; Message = "Official pnpm installer failed (exit 23)" },
        @{ Name = "missing"; Message = "Official installer did not produce*" }
    )) {
        $installerState.Case = $case.Name
        $env:PNPM_HOME = Join-Path $fixtureRoot $case.Name
        $env:PNPM_VERSION = "preserve-me"
        $env:GITHUB_PATH = Join-Path $fixtureRoot "github-path"
        $env:GITHUB_ENV = Join-Path $fixtureRoot "github-env"
        $failure = ""
        try { & (Join-Path $PSScriptRoot "install-pnpm.ps1") -Version "9" }
        catch { $failure = $_.Exception.Message }
        if ($failure -notlike $case.Message) { throw "$($case.Name): unexpected failure '$failure'" }
        if (Test-Path -LiteralPath $installerState.Download) { throw "Temporary installer was not removed" }
        if ($env:PNPM_VERSION -ne "preserve-me" -or $env:Path -ne $originalPath) { throw "Failed installation changed version or PATH" }
        if ((Test-Path $env:GITHUB_PATH) -or (Test-Path $env:GITHUB_ENV)) { throw "Failed installation exported Actions environment" }
        Write-Host " PASS  pnpm installer rejects $($case.Name) failure" -ForegroundColor Green
    }
} finally {
    foreach ($name in $savedEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], "Process")
    }
    Remove-Item -LiteralPath $fixtureRoot -Recurse -Force
}
# Child process failures are expected in these fixtures, not a failing gate.
$global:LASTEXITCODE = 0
