# Offline installer regressions; never downloads or executes a compiler.
#Requires -Version 5.1
Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$installer = Join-Path $PSScriptRoot "install-clap-toolchain.ps1"
$scratch = Join-Path ([IO.Path]::GetTempPath()) ("playlist-ai-clap-test-" + [guid]::NewGuid().ToString("N"))
$hostArch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64" -or $env:PROCESSOR_ARCHITEW6432 -eq "ARM64") { "aarch64" } else { "x86_64" }
$name = "llvm-mingw-20260908-ucrt-$hostArch"
$state = @{ Downloads = 0; BadHash = $false }
function Invoke-WebRequest($Uri, $OutFile, [switch]$UseBasicParsing) {
    if ($Uri -ne "https://github.com/mstorsjo/llvm-mingw/releases/download/20260908/$name.zip") { throw "Unpinned compiler source" }
    $state.Downloads++
    [IO.File]::WriteAllText($OutFile, "synthetic archive")
}
function Get-FileHash($LiteralPath, $Algorithm) {
    $hash = if ($state.BadHash) { "bad" } elseif ($hostArch -eq "aarch64") { "7fe35f60407473c420ac72b25137f54385b0affd2c42efd507c0062fc626ab56" } else { "1bcf74d06b724aeecaa6412ca85f5b26fb1da770e7cdcefa9263c9c5c3ad34b6" }
    return [pscustomobject]@{ Hash = $hash }
}
function Expand-Archive($LiteralPath, $DestinationPath) {
    $bin = Join-Path $DestinationPath "$name\bin"
    [void][IO.Directory]::CreateDirectory($bin)
    foreach ($target in @("x86_64", "aarch64")) { [IO.File]::WriteAllText((Join-Path $bin "$target-w64-mingw32-clang.exe"), "not executable") }
}
try {
    $dir = Join-Path $scratch "path with spaces"
    try {
        & $installer -Directory $dir -CheckOnly | Out-Null
        throw "Missing compiler accepted"
    } catch { if ($_.Exception.Message -notlike "Native CLAP compiler missing*") { throw } }
    if ($state.Downloads -ne 0) { throw "CheckOnly downloaded a compiler" }
    $state.BadHash = $true
    try {
        & $installer -Directory $dir | Out-Null
        throw "Invalid checksum accepted"
    } catch { if ($_.Exception.Message -ne "LLVM-MinGW archive checksum mismatch") { throw } }
    if (Test-Path (Join-Path $dir $name)) { throw "Corrupt compiler activated" }
    $state.BadHash = $false
    foreach ($arch in @("amd64", "arm64")) {
        $target = if ($arch -eq "arm64") { "aarch64" } else { "x86_64" }
        $compiler = & $installer -Directory $dir -Architecture $arch
        $expected = '"' + (Join-Path $dir "$name\bin\$target-w64-mingw32-clang.exe") + '"'
        if ($compiler -ne $expected) { throw "Wrong or unquoted compiler: $compiler" }
    }
    if ($state.Downloads -ne 2) { throw "Verified toolchain was re-downloaded" }
    if (@(Get-ChildItem $dir -Filter ".llvm-install-*").Count -ne 0) { throw "Staging directory retained" }
    Write-Host "PASS: pinned download, checksum rejection, offline reuse, both targets, quoted paths and cleanup"
} finally { if (Test-Path -LiteralPath $scratch) { Remove-Item -LiteralPath $scratch -Recurse -Force } }
