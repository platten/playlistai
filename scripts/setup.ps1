# Install and verify the Windows prerequisites for test.ps1 and build.ps1.
# winget is preferred; an existing Scoop installation is the fallback.
#Requires -Version 5.1
[CmdletBinding()]
param(
    [switch]$NoSystem,
    [switch]$WithRace
)

. (Join-Path $PSScriptRoot "_common.ps1")

if ($env:OS -ne "Windows_NT") { throw "setup.ps1 is for Windows; use setup.sh on Linux or macOS" }

$missing = [Collections.Generic.List[string]]::new()
$notes = [Collections.Generic.List[string]]::new()
$packageManager = if (Test-Command "winget") { "winget" } elseif (Test-Command "scoop") { "scoop" } else { "" }

function Install-SystemPackage {
    param(
        [Parameter(Mandatory = $true)][string]$WingetID,
        [Parameter(Mandatory = $true)][string]$ScoopName,
        [switch]$Upgrade
    )
    if ($NoSystem) {
        Write-Skip "-NoSystem: not installing $WingetID"
        return $false
    }
    if ($packageManager -eq "winget") {
        $verb = if ($Upgrade) { "upgrade" } else { "install" }
        Write-Info "$verb $WingetID with winget"
        & winget $verb --id $WingetID --exact --source winget --accept-package-agreements --accept-source-agreements --disable-interactivity | Out-Host
        if ($LASTEXITCODE -ne 0 -and $Upgrade) {
            & winget install --id $WingetID --exact --source winget --accept-package-agreements --accept-source-agreements --disable-interactivity | Out-Host
        }
        $ok = $LASTEXITCODE -eq 0
    } elseif ($packageManager -eq "scoop") {
        $verb = if ($Upgrade) { "update" } else { "install" }
        Write-Info "$verb $ScoopName with Scoop"
        & scoop $verb $ScoopName | Out-Host
        $ok = $?
    } else {
        $notes.Add("install App Installer/winget, or Scoop, then re-run scripts/setup.ps1")
        return $false
    }
    Update-ProcessPath
    Add-GoBinToPath
    Add-NSISToPath
    return $ok
}

function Test-WebView2 {
    $clientID = "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"
    $keys = @(
        "HKLM:\SOFTWARE\Microsoft\EdgeUpdate\Clients\$clientID",
        "HKLM:\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\$clientID",
        "HKCU:\SOFTWARE\Microsoft\EdgeUpdate\Clients\$clientID"
    )
    foreach ($key in $keys) {
        $entry = Get-ItemProperty -Path $key -Name "pv" -ErrorAction SilentlyContinue
        if ($entry -and $entry.pv -and $entry.pv -ne "0.0.0.0") { return $true }
    }
    $locations = @(
        (Join-Path ${env:ProgramFiles(x86)} "Microsoft\EdgeWebView\Application\*\msedgewebview2.exe"),
        (Join-Path $env:LOCALAPPDATA "Microsoft\EdgeWebView\Application\*\msedgewebview2.exe")
    )
    return $null -ne ($locations | Get-Item -ErrorAction SilentlyContinue | Select-Object -First 1)
}

$packageManagerLabel = if ($packageManager) { " - package manager: $packageManager" } else { "" }
Write-Info "host: Windows$packageManagerLabel"

$goUpgrade = $false
if (Test-Command "go") {
    $goVersion = Get-NumericVersion (& go version)
    if ($goVersion -ge $GoVersionMin) { Write-Pass "Go $goVersion" } else { $goUpgrade = $true }
}
if (-not (Test-Command "go") -or $goUpgrade) {
    [void](Install-SystemPackage -WingetID "GoLang.Go" -ScoopName "go" -Upgrade:$goUpgrade)
}
if (-not (Test-Command "go") -or (Get-NumericVersion (& go version)) -lt $GoVersionMin) {
    $missing.Add("Go $GoVersionMin+")
}

$nodeUpgrade = $false
if (Test-Command "node") {
    $nodeVersion = Get-NumericVersion (& node --version)
    if ($nodeVersion -ge $NodeVersionMin) { Write-Pass "Node $nodeVersion" } else { $nodeUpgrade = $true }
}
if (-not (Test-Command "node") -or $nodeUpgrade) {
    [void](Install-SystemPackage -WingetID "OpenJS.NodeJS.LTS" -ScoopName "nodejs-lts" -Upgrade:$nodeUpgrade)
}
if (-not (Test-Command "node") -or (Get-NumericVersion (& node --version)) -lt $NodeVersionMin) {
    $missing.Add("Node $NodeVersionMin+")
}

if (-not (Test-Command "pnpm")) {
    Write-Info "install pnpm $PnpmVersion"
    if (Test-Command "corepack") {
        & corepack enable
        & corepack prepare "pnpm@$PnpmVersion" --activate
    } elseif (Test-Command "npm") {
        & npm install --global "pnpm@$PnpmVersion"
    }
}
if (Test-Command "pnpm") { Write-Pass "pnpm $(& pnpm --version)" } else { $missing.Add("pnpm") }

if (-not (Test-Command "makensis")) {
    [void](Install-SystemPackage -WingetID "NSIS.NSIS" -ScoopName "nsis")
    Add-NSISToPath
}
if (Test-Command "makensis") { Write-Pass "NSIS" } else { $missing.Add("NSIS/makensis") }

if (Test-WebView2) {
    Write-Pass "Microsoft Edge WebView2 Runtime"
} elseif ($packageManager -eq "winget" -and -not $NoSystem) {
    [void](Install-SystemPackage -WingetID "Microsoft.EdgeWebView2Runtime" -ScoopName "")
    if (Test-WebView2) { Write-Pass "Microsoft Edge WebView2 Runtime" }
    else { $missing.Add("Microsoft Edge WebView2 Runtime") }
} else {
    $missing.Add("Microsoft Edge WebView2 Runtime")
    $notes.Add("install WebView2 with winget or Microsoft's Evergreen installer")
}

if (Test-Command "go") {
    foreach ($tool in @(
        @{ Name = "wails3"; Module = "github.com/wailsapp/wails/v3/cmd/wails3@$Wails3Version" },
        @{ Name = "golangci-lint"; Module = "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$GolangCILintVersion" }
    )) {
        if (-not (Test-Command $tool.Name)) {
            Write-Info "go install $($tool.Module)"
            & go install $tool.Module
            if ($LASTEXITCODE -ne 0) { $missing.Add($tool.Name) }
            Add-GoBinToPath
        }
        if (Test-Command $tool.Name) { Write-Pass $tool.Name }
    }
}

if ($WithRace) {
    if (-not (Test-Command "gcc")) {
        if ($packageManager -eq "scoop") {
            [void](Install-SystemPackage -WingetID "MSYS2.MSYS2" -ScoopName "gcc")
        } else {
            [void](Install-SystemPackage -WingetID "MSYS2.MSYS2" -ScoopName "gcc")
            $pacman = "C:\msys64\usr\bin\pacman.exe"
            if (Test-Path $pacman) {
                & $pacman -S --needed --noconfirm mingw-w64-ucrt-x86_64-gcc
                $env:Path = "$env:Path;C:\msys64\ucrt64\bin"
            }
        }
    }
    if (Test-Command "gcc") {
        $syncLibrary = (& gcc --print-file-name libsynchronization.a).Trim()
        if ($syncLibrary -eq "libsynchronization.a") { $missing.Add("mingw-w64 v8+ for go test -race") }
        else { Write-Pass "Windows race-detector C toolchain" }
    } else {
        $missing.Add("gcc/mingw-w64 for go test -race")
    }
} else {
    $notes.Add("use -WithRace to install/check the optional mingw-w64 toolchain required by go test -race")
}

if (Test-Command "wails3") {
    Write-Info "checking Wails platform dependencies"
    & wails3 doctor
    if ($LASTEXITCODE -ne 0) { $notes.Add("wails3 doctor reported a platform issue; WebView2 is normally preinstalled on Windows 10/11") }
}

Write-Host ""
if ($notes.Count -gt 0) {
    Write-Skip "notes:"
    $notes | ForEach-Object { Write-Host "        - $_" }
}
if ($missing.Count -gt 0) { throw "still missing: $($missing -join ', ')" }
Write-Pass "all prerequisites are installed - run scripts/test.ps1 or scripts/build.ps1"
