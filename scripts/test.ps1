# Run the same Go, lint, binding, typecheck, and frontend build gate as test.sh.
#Requires -Version 5.1
[CmdletBinding()]
param(
    [switch]$NoRace,
    [switch]$Coverage,
    [string]$TestReportDirectory = "",
    [switch]$HostCoverage,
    [ValidateRange(0, 32)][int]$PackageParallelism = 0
)

. (Join-Path $PSScriptRoot "_common.ps1")
Assert-Command "go" "run scripts/setup.ps1"
Assert-GoVersion
if ($TestReportDirectory) {
    Assert-Command "node" "Node is required for JSON test timing reports"
    $TestReportDirectory = [IO.Path]::GetFullPath($TestReportDirectory)
    [void][IO.Directory]::CreateDirectory($TestReportDirectory)
}
if ($HostCoverage -and -not $TestReportDirectory) { throw "HostCoverage requires TestReportDirectory" }

$failed = [Collections.Generic.List[string]]::new()
function Invoke-TestStep([string]$Label, [scriptblock]$Action) {
    Write-Info $Label
    try {
        $global:LASTEXITCODE = 0
        & $Action
        if ($LASTEXITCODE -ne 0) { throw "exit code $LASTEXITCODE" }
        Write-Pass $Label
    } catch {
        Write-Fail "$Label - $($_.Exception.Message)"
        $failed.Add($Label)
    }
}

Invoke-TestStep "PowerShell syntax" {
    $parseErrors = [Collections.Generic.List[string]]::new()
    Get-ChildItem $PSScriptRoot -Filter "*.ps1" | ForEach-Object {
        $tokens = $null
        $errors = $null
        [void][Management.Automation.Language.Parser]::ParseFile($_.FullName, [ref]$tokens, [ref]$errors)
        foreach ($parseError in $errors) { $parseErrors.Add("$($_.Name): $($parseError.Message)") }
    }
    if ($parseErrors.Count -gt 0) { throw ($parseErrors -join [Environment]::NewLine) }
}

Invoke-TestStep "pnpm installer failure handling" { & (Join-Path $PSScriptRoot "test-pnpm-installer.ps1") }
Invoke-TestStep "CLAP compiler installer" { & (Join-Path $PSScriptRoot "test-clap-toolchain.ps1") }

if ((Test-Command "node") -and (Test-Command "pnpm")) {
    Invoke-TestStep "CI timing helper" { & node (Join-Path $PSScriptRoot "go-test.test.mjs") }
    if (Test-Command "wails3") {
        Invoke-TestStep "wails3 generate bindings" { & wails3 generate bindings -clean=true -ts -i }
    } else {
        Write-Skip "wails3 not installed - typecheck will use bindings already on disk"
    }
    Push-Location (Join-Path $RepoRoot "frontend")
    try {
        Invoke-TestStep "pnpm install" { & pnpm install --frozen-lockfile }
        Invoke-TestStep "frontend typecheck" { & pnpm run typecheck }
        Invoke-TestStep "frontend tests" { & pnpm test }
        # typecheck already ran; standalone pnpm build still includes tsc.
        Invoke-TestStep "frontend build" { & pnpm run build:bundle }
    } finally {
        Pop-Location
    }
} else {
    Write-Skip "node/pnpm not found - frontend checks were not run"
}

# The frontend runs first because main.go embeds frontend/dist (go:embed), so
# every Go step that builds the root package needs that directory to exist. On a
# fresh clone it does not.
Invoke-TestStep "go vet" { & go vet ./... }

Invoke-TestStep "pure-Go core compile" {
    $packages = @(& go list ./internal/... | Where-Object { $_ -notlike "*/internal/bridge" })
    if ($LASTEXITCODE -ne 0) { throw "go list failed" }
    $previousCGO = $env:CGO_ENABLED
    try {
        $env:CGO_ENABLED = "0"
        & go test -run '^$' @packages
    } finally {
        $env:CGO_ENABLED = $previousCGO
    }
}

$goTestArgs = @('-count=1')
if (-not $NoRace) { $goTestArgs += '-race' }
if ($PackageParallelism -gt 0) { $goTestArgs += @('-p', "$PackageParallelism") }
if ($HostCoverage) {
    $goTestArgs += @('-coverpkg=./...', '-covermode=atomic', "-coverprofile=$(Join-Path $TestReportDirectory 'backend.out')")
}
$goTestArgs += './...'
Invoke-TestStep "go test $($goTestArgs -join ' ')" {
    if ($TestReportDirectory) {
        & node (Join-Path $PSScriptRoot 'go-test.mjs') (Join-Path $TestReportDirectory 'race-tests') @goTestArgs
    } else {
        & go test @goTestArgs
    }
}

if (Test-Command "golangci-lint") {
    Invoke-TestStep "golangci-lint" { & golangci-lint run ./... }
} else {
    Write-Skip "golangci-lint not installed - run scripts/setup.ps1"
}

if ($Coverage) {
    Invoke-TestStep "95% application coverage" { & (Join-Path $PSScriptRoot "coverage.ps1") }
}

Write-Host ""
if ($failed.Count -gt 0) { throw "failed: $($failed -join ', ')" }
Write-Pass "all checks passed"
