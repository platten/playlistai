# Native, offline NSIS regression. Installs only fixture bytes into a temporary
# folder: no UAC, desktop startup, shortcuts, registry writes or user data.
#Requires -Version 5.1
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '_common.ps1')
Assert-Command 'makensis' 'run scripts/setup.ps1'
$fixtureRoot = Join-Path ([IO.Path]::GetTempPath()) ('Playlist AI installer test ' + [guid]::NewGuid())
[void][IO.Directory]::CreateDirectory($fixtureRoot)
try {
    $payload = Join-Path $fixtureRoot 'payload.exe'
    [IO.File]::WriteAllText($payload, 'new release executable fixture')
    $include = Join-Path $RepoRoot 'build\windows\nsis\install_files.nsh'
    $wailsInclude = Join-Path $RepoRoot 'build\windows\nsis\wails_tools.nsh'
    $sourceTemplate = @'
Unicode true
!define REQUEST_EXECUTION_LEVEL user
!define ARG_WAILS_AMD64_BINARY "@PAYLOAD@"
!define ARG_WAILS_ARM64_BINARY "@PAYLOAD@"
Name "Playlist AI installer regression"
OutFile "@OUTPUT@"
!include "@WAILS@"
!include "@INCLUDE@"
Section
    @INSTALL@
    FileOpen $0 "$INSTDIR\committed" w
    FileWrite $0 "installed"
    FileClose $0
SectionEnd
'@
    foreach ($mode in @('legacy', 'fixed')) {
        $output = Join-Path $fixtureRoot "$mode.exe"
        $install = if ($mode -eq 'legacy') { 'SetOutPath $INSTDIR' + "`n" + '!insertmacro wails.files' } else { '!insertmacro playlistai.installFiles' }
        $source = $sourceTemplate.Replace('@OUTPUT@', $output).Replace('@INCLUDE@', $include).Replace('@WAILS@', $wailsInclude).Replace('@PAYLOAD@', $payload).Replace('@INSTALL@', $install)
        $sourceFile = Join-Path $fixtureRoot "$mode.nsi"
        [IO.File]::WriteAllText($sourceFile, $source)
        & makensis /V2 $sourceFile
        if ($LASTEXITCODE -ne 0) { throw "Could not compile $mode installer" }
    }
    $destination = Join-Path $fixtureRoot 'Installed App With Spaces'
    [void][IO.Directory]::CreateDirectory($destination)
    $target = Join-Path $destination 'playlist-ai.exe'
    $committed = Join-Path $destination 'committed'
    [IO.File]::WriteAllText($target, 'previous executable fixture')
    $oldHash = (Get-FileHash -LiteralPath $target).Hash
    function Invoke-FixtureInstaller([string]$Mode) {
        $installerProcess = Start-Process -FilePath (Join-Path $fixtureRoot "$Mode.exe") -ArgumentList @('/S', "/D=$destination") -WindowStyle Hidden -PassThru
        if (-not $installerProcess.WaitForExit(15000)) {
            $installerProcess.Kill()
            throw "$Mode installer timed out"
        }
        $installerProcess.Refresh()
        return $installerProcess.ExitCode
    }
    $lock = [IO.File]::Open($target, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    try {
        if ((Invoke-FixtureInstaller 'legacy') -ne 0 -or -not (Test-Path -LiteralPath $committed)) { throw 'Legacy false-success reproduction failed' }
        if ((Get-FileHash -LiteralPath $target).Hash -ne $oldHash) { throw 'Legacy fixture changed the locked executable' }
        Remove-Item -LiteralPath $committed
        if ((Invoke-FixtureInstaller 'fixed') -eq 0) { throw 'Locked executable reported success' }
        if (Test-Path -LiteralPath $committed) { throw 'Installer continued after a failed replacement' }
        if ((Get-FileHash -LiteralPath $target).Hash -ne $oldHash) { throw 'Failed replacement damaged the old executable' }
        Write-Pass 'locked executable: legacy false success reproduced; fixed installer fails and retains old bytes'
    } finally { $lock.Dispose() }
    if ((Invoke-FixtureInstaller 'fixed') -ne 0) { throw 'Unlocked installation failed' }
    if ((Get-FileHash -LiteralPath $target).Hash -ne (Get-FileHash -LiteralPath $payload).Hash) { throw 'New executable was not installed' }
    if (-not (Test-Path -LiteralPath $committed)) { throw 'Successful installer did not continue' }
    Write-Pass 'retry after closing lock replaces executable at the existing path with spaces'
} finally {
    $resolvedFixture = [IO.Path]::GetFullPath($fixtureRoot)
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    if (-not $resolvedFixture.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -or -not ([IO.Path]::GetFileName($resolvedFixture)).StartsWith('Playlist AI installer test ')) {
        throw "Unsafe installer fixture cleanup path: $resolvedFixture"
    }
    Remove-Item -LiteralPath $resolvedFixture -Recurse -Force
}
