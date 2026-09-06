# Cross-platform scripts

Run the `.sh` commands from Bash on Linux or macOS. Run the `.ps1` commands
from Windows PowerShell 5.1+ or PowerShell 7. Both command families operate
from the repository root and expose the same core workflows.

| workflow | Linux / macOS | Windows PowerShell |
|---|---|---|
| Install dependencies | `./scripts/setup.sh` | `.\scripts\setup.ps1` |
| Test | `./scripts/test.sh` | `.\scripts\test.ps1` |
| Build/package | `./scripts/build.sh` | `.\scripts\build.ps1` |
| Reset user data | `./scripts/reset-userdata.sh --dry-run` | `.\scripts\reset-userdata.ps1 -DryRun` |
| Benchmark intent parser | `./scripts/benchmark-intent.sh …` | `.\scripts\benchmark-intent.ps1 …` |

The test commands run Go vet/tests, a pure-Go core compile, lint when installed,
Wails binding generation, frontend typechecking, and the production frontend
build. The Bash gate also checks all shell scripts; the PowerShell gate parses
all `.ps1` files. Use `--no-race` or `-NoRace` only where the Go race detector
is unavailable.

## Prerequisites

Run the platform setup script to install all of these automatically. By hand:

- **Go** 1.27+, **Node** 22+, **pnpm** 9+
- **wails3**: `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.16`
- **golangci-lint** (for `test.sh`): `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2`
- **Linux**: a C toolchain, `pkg-config`, GTK4, and WebKitGTK 6.0 development libraries
- **macOS**: Xcode Command Line Tools (`xcode-select --install`)
- **Windows**: NSIS; WebView2 is normally already installed on Windows 10/11

`setup.sh` supports apt, dnf, pacman, zypper, and Homebrew. It uses `sudo` only
for Linux system packages; `--no-system` opts out. On Windows, `setup.ps1`
prefers winget (`GoLang.Go`, `OpenJS.NodeJS.LTS`, `NSIS.NSIS`, and WebView2
when absent) and falls back to an existing Scoop installation. `-NoSystem` skips package-manager
changes but still installs project Go/Node tools when their runtimes exist. Use
`-WithRace` to install/check the modern mingw-w64 compiler required by the
Windows Go race detector.

## What `build.sh` can package, and where

| target | host = Linux | host = macOS | host = Windows |
|---|---|---|---|
| Linux (AppImage/deb/rpm/Arch) | ✅ native (needs `libgtk-4-dev`, `libwebkitgtk-6.0-dev`) | ⏭ skipped (CGO + GTK) | ⏭ skipped |
| Windows (NSIS installer) | ✅ cross — needs `makensis` (`apt install nsis`) | ✅ cross — needs `brew install makensis` | ✅ native |
| macOS (.app + .dmg) | ⏭ skipped — needs a Mac | ✅ native | ⏭ skipped |

Targets that can't be built here are skipped with a note; `build.sh` still exits 0 as long as the host-OS package succeeded.

On Windows, `build.ps1` creates the native NSIS package. Pass
`-Architecture amd64`, `-Architecture arm64`, or `-Architecture all`.

Both reset scripts enumerate exact app-data paths and ask once before removal.
`--dry-run` / `-DryRun` lists them without changing anything, while `--yes` /
`-Yes` is intended for disposable test profiles. Neither script touches
version-controlled files or `bin/`.

## Intent parser benchmarks

The wrappers run the versioned `cmd/intenteval` harness and default reports to
a temporary directory so local model paths are not accidentally committed.
Use the same dataset and settings when comparing operating systems.

```bash
./scripts/benchmark-intent.sh --backend rules
./scripts/benchmark-intent.sh --model /models/model.gguf \
  --runtime /opt/llama/llama-server --device CUDA0 --gpu-layers 0
```

```powershell
.\scripts\benchmark-intent.ps1 -Backend rules
.\scripts\benchmark-intent.ps1 -Model C:\Models\model.gguf `
  -RuntimePath C:\llama.cpp\llama-server.exe -Device CUDA0 -GPULayers 0
```

The recommendation catalog and the llama.cpp runtime are **not** part of the
build — the app downloads/installs those on first launch (see `docs/RELEASING.md`).
