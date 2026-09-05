# scripts/

| script | what it does |
|---|---|
| `./scripts/setup.sh` | Install every prerequisite `test.sh` / `build.sh` need that's missing: Go, Node, pnpm, `wails3`, `golangci-lint`, and (on Linux) the GTK4 + WebKitGTK dev libraries. `--no-system` skips anything needing a package manager or sudo; `--with-nsis` also installs NSIS for Windows cross-packaging. |
| `./scripts/test.sh` | `go vet` + `go test -race` + `golangci-lint`, then regenerate the Wails bindings and run the frontend typecheck + production build. `--no-race` skips the race detector. Exit non-zero on any failure. |
| `./scripts/build.sh` | Compile **and** package the desktop app for every target OS buildable from the current machine. Artifacts go to `./bin/`. |
| `./scripts/reset-userdata.sh` | Delete Playlist AI's per-user data (`os.UserConfigDir()/playlist-ai` + the llama.cpp installer scratch dir) so the next launch is a fresh first run. `--dry-run` lists without deleting; `--yes` skips the prompt. Never touches the repo or `./bin/`. |

`test.sh` / `build.sh` wrap `wails3 task …` (the same commands `.github/workflows/release.yml` uses) and add `$(go env GOPATH)/bin` to `PATH` so a `go install`ed `wails3` / `task` / `golangci-lint` is found.

## Prerequisites

Run `./scripts/setup.sh` to install all of these automatically. By hand:

- **Go** 1.27+, **Node** 22+, **pnpm** 9+
- **wails3**: `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.16`
- **golangci-lint** (for `test.sh`): `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2`
- **Linux only**, for `build.sh`: a C toolchain, `pkg-config`, `libgtk-4-dev`, `libwebkitgtk-6.0-dev`

`setup.sh` uses `sudo` only for the package-manager steps (Linux dev libraries, NSIS); it announces each `sudo` command and its reason before running it, and `--no-system` opts out of all of them.

## What `build.sh` can package, and where

| target | host = Linux | host = macOS | host = Windows |
|---|---|---|---|
| Linux (AppImage/deb/rpm/Arch) | ✅ native (needs `libgtk-4-dev`, `libwebkitgtk-6.0-dev`) | ⏭ skipped (CGO + GTK) | ⏭ skipped |
| Windows (NSIS installer) | ✅ cross — needs `makensis` (`apt install nsis`) | ✅ cross — needs `brew install makensis` | ✅ native |
| macOS (.app + .dmg) | ⏭ skipped — needs a Mac | ✅ native | ⏭ skipped |

Targets that can't be built here are skipped with a note; `build.sh` still exits 0 as long as the host-OS package succeeded.

The recommendation catalog and the llama.cpp runtime are **not** part of the
build — the app downloads/installs those on first launch (see `docs/RELEASING.md`).
