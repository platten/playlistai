# Run the same Go, lint, binding, typecheck, and frontend build gate as test.sh.
#Requires -Version 5.1
[CmdletBinding()]
param([switch]$NoRace)

. (Join-Path $PSScriptRoot "_common.ps1")
Assert-Command "go" "run scripts/setup.ps1"

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

if ($NoRace) {
    Invoke-TestStep "go test" { & go test -count=1 ./... }
} else {
    Invoke-TestStep "go test -race" { & go test -race -count=1 ./... }
}

if (Test-Command "golangci-lint") {
    Invoke-TestStep "golangci-lint" { & golangci-lint run ./... }
} else {
    Write-Skip "golangci-lint not installed - run scripts/setup.ps1"
}

if ((Test-Command "node") -and (Test-Command "pnpm")) {
    if (Test-Command "wails3") {
        Invoke-TestStep "wails3 generate bindings" { & wails3 generate bindings -clean=true -ts -i }
    } else {
        Write-Skip "wails3 not installed - typecheck will use bindings already on disk"
    }
    Push-Location (Join-Path $RepoRoot "frontend")
    try {
        Invoke-TestStep "pnpm install" { & pnpm install --frozen-lockfile }
        Invoke-TestStep "frontend typecheck" { & pnpm run typecheck }
        Invoke-TestStep "frontend build" { & pnpm run build }
    } finally {
        Pop-Location
    }
} else {
    Write-Skip "node/pnpm not found - frontend checks were not run"
}

Write-Host ""
if ($failed.Count -gt 0) { throw "failed: $($failed -join ', ')" }
Write-Pass "all checks passed"
