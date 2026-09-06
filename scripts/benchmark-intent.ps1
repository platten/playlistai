# Benchmark the versioned intent parser on Windows with rules or local llama.cpp.
#Requires -Version 5.1
[CmdletBinding()]
param(
    [ValidateSet("llama", "rules")][string]$Backend = "llama",
    [string]$Model = "",
    [string]$RuntimePath = "",
    [string]$ModelID = "",
    [string]$Dataset = "internal/evaluation/testdata/intent-model-v1.json",
    [string]$OutputDir = "",
    [ValidateRange(1, 100)][int]$Repeat = 3,
    [ValidateRange(128, 1048576)][int]$ContextSize = 4096,
    [ValidateRange(0, 4096)][int]$Threads = 0,
    [ValidateRange(-1, 10000)][int]$GPULayers = 0,
    [string]$Device = "",
    [string]$Case = ""
)

. (Join-Path $PSScriptRoot "_common.ps1")
Assert-Command "go" "run scripts/setup.ps1"
if (-not (Test-Path -LiteralPath $Dataset -PathType Leaf)) { throw "dataset not found: $Dataset" }

if ($Backend -eq "llama") {
    if ([string]::IsNullOrWhiteSpace($Model)) { throw "-Model is required for the llama backend" }
    if (-not (Test-Path -LiteralPath $Model -PathType Leaf)) { throw "model not found: $Model" }
    if ([string]::IsNullOrWhiteSpace($RuntimePath)) {
        foreach ($name in @("llama-server", "llama")) {
            $command = Get-Command $name -ErrorAction SilentlyContinue
            if ($command) { $RuntimePath = $command.Source; break }
        }
        if ([string]::IsNullOrWhiteSpace($RuntimePath)) {
            $managedRuntime = Join-Path $env:LOCALAPPDATA "llama-app\llama.exe"
            if (Test-Path -LiteralPath $managedRuntime) { $RuntimePath = $managedRuntime }
        }
    }
    if ([string]::IsNullOrWhiteSpace($RuntimePath) -or -not (Test-Path -LiteralPath $RuntimePath -PathType Leaf)) {
        throw "llama.cpp runtime not found; pass -RuntimePath"
    }
}

if ([string]::IsNullOrWhiteSpace($OutputDir)) {
    $leaf = "playlist-ai-intent-bench-{0}-{1}" -f (Get-Date -Format "yyyyMMdd-HHmmss"), ([guid]::NewGuid().ToString("N").Substring(0, 8))
    $OutputDir = Join-Path ([IO.Path]::GetTempPath()) $leaf
}
[void](New-Item -ItemType Directory -Force -Path $OutputDir)

$cliArgs = @(
    "run", "./cmd/intenteval",
    "-dataset", $Dataset,
    "-backend", $Backend,
    "-repeat", $Repeat,
    "-n-ctx", $ContextSize,
    "-threads", $Threads,
    "-gpu-layers", $GPULayers,
    "-output", (Join-Path $OutputDir "report.json"),
    "-markdown", (Join-Path $OutputDir "report.md")
)
if ($Backend -eq "llama") {
    $cliArgs += @("-model", $Model, "-runtime", $RuntimePath)
    if ($ModelID) { $cliArgs += @("-model-id", $ModelID) }
    if ($Device) { $cliArgs += @("-device", $Device) }
}
if ($Case) { $cliArgs += @("-case", $Case) }

Write-Info "intent benchmark: $Backend (Windows)"
& go @cliArgs
if ($LASTEXITCODE -ne 0) { throw "intent benchmark failed" }
Write-Pass "reports written to $OutputDir"
