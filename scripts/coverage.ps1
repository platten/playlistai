#Requires -Version 5.1
# Reports remain available even when the 95% threshold fails.
[CmdletBinding()]
param()
$ErrorActionPreference = "Stop"
Push-Location (Split-Path $PSScriptRoot -Parent)
try {
    New-Item -ItemType Directory -Force coverage | Out-Null
    $failed = $false
    & go test -count=1 '-coverpkg=./...' -covermode=atomic -coverprofile=coverage/backend.out ./...
    if ($LASTEXITCODE -ne 0) { $failed = $true }
    & go tool cover -html=coverage/backend.out -o coverage/backend.html
    if ($LASTEXITCODE -ne 0) { $failed = $true }
    & go run ./cmd/coveragecheck -profile coverage/backend.out
    if ($LASTEXITCODE -ne 0) { $failed = $true }
    Push-Location frontend
    try {
        & pnpm run test:coverage
        if ($LASTEXITCODE -ne 0) { $failed = $true }
    } finally { Pop-Location }
    if ($failed) { throw "Coverage checks failed; inspect coverage/ and frontend/coverage/." }
} finally { Pop-Location }
