# Build-time only: pinned LLVM-MinGW supplies C compilers for Windows x64/ARM64.
# Nothing from this toolchain is downloaded by the desktop wizard or packaged.
#Requires -Version 5.1
[CmdletBinding()]
param(
    [ValidateSet("amd64", "arm64")][string]$Architecture = "amd64",
    [string]$Directory = (Join-Path $env:LOCALAPPDATA "playlist-ai-build-tools"),
    [switch]$CheckOnly
)
Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
if ($env:OS -ne "Windows_NT") { throw "This compiler installer runs on Windows." }

$version = "20260908"
$hostArch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64" -or $env:PROCESSOR_ARCHITEW6432 -eq "ARM64") { "aarch64" } else { "x86_64" }
$sha256 = if ($hostArch -eq "aarch64") { "7fe35f60407473c420ac72b25137f54385b0affd2c42efd507c0062fc626ab56" } else { "1bcf74d06b724aeecaa6412ca85f5b26fb1da770e7cdcefa9263c9c5c3ad34b6" }
$name = "llvm-mingw-$version-ucrt-$hostArch"
$target = if ($Architecture -eq "arm64") { "aarch64" } else { "x86_64" }
$compiler = Join-Path $Directory "$name\bin\$target-w64-mingw32-clang.exe"
if (-not (Test-Path -LiteralPath $compiler -PathType Leaf)) {
    if ($CheckOnly) { throw "Native CLAP compiler missing. Run scripts/install-clap-toolchain.ps1 (or scripts/setup.ps1), then rebuild." }
    [void][IO.Directory]::CreateDirectory($Directory)
    $staging = Join-Path $Directory (".llvm-install-" + [guid]::NewGuid().ToString("N"))
    [void][IO.Directory]::CreateDirectory($staging)
    try {
        $archive = Join-Path $staging "$name.zip"
        $url = "https://github.com/mstorsjo/llvm-mingw/releases/download/$version/$name.zip"
        $previousProgress = $ProgressPreference
        try {
            $ProgressPreference = "SilentlyContinue"
            Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $archive
        } finally { $ProgressPreference = $previousProgress }
        if ((Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash -ne $sha256) { throw "LLVM-MinGW archive checksum mismatch" }
        Expand-Archive -LiteralPath $archive -DestinationPath $staging
        $extracted = Join-Path $staging $name
        if (-not (Test-Path -LiteralPath (Join-Path $extracted "bin\$target-w64-mingw32-clang.exe"))) { throw "Compiler missing from verified archive" }
        # Never overwrite an existing toolchain, including an incomplete one.
        [IO.Directory]::Move($extracted, (Join-Path $Directory $name))
    } finally {
        # Only this invocation's private staging directory is disposable.
        Remove-Item -LiteralPath $staging -Recurse -Force
    }
}
# Go's CC value is tokenized; quote paths containing spaces.
Write-Output ('"' + $compiler + '"')
