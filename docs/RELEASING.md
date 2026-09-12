# Releasing

Build and automation jobs prefer the registered self-hosted Windows/Linux x86-64
runners. Native macOS and Linux ARM64 stay hosted. See
[runner routing and the hosted override](self-hosted-runners.md).

Playlist AI ships two things per platform: a native **installer** (the primary,
recommended path) and a **portable archive** of the raw binary. Both are built
by [`.github/workflows/release.yml`](../.github/workflows/release.yml) and
attached to a draft GitHub Release.

| OS      | Arches        | Installer                                  | Portable            |
| ------- | ------------- | ------------------------------------------- | -------------------- |
| Linux   | amd64, arm64  | `.AppImage`, `.deb`, `.rpm`, `.pkg.tar.zst` | `playlist-ai-linux-<arch>.tar.gz` |
| macOS   | arm64         | `.dmg`                                      | `playlist-ai-macos.zip` (the `.app`) |
| Windows | amd64, arm64  | `playlist-ai-<arch>-installer.exe` (NSIS)   | `playlist-ai-windows-<arch>.zip` |

Linux arm64 builds natively on a `ubuntu-24.04-arm` runner (the build is CGO);
Windows arm64 is a cgo cross-compile with pinned LLVM-MinGW on the x86 runner,
so both Windows architectures include the built-in CLAP worker. `ci.yml` builds
every one of these on each push, so a broken arch shows up before a tag is cut.

Packaging itself (AppImage/deb/rpm/dmg/NSIS) always runs — it needs no secrets.
**Code signing is best-effort and additive**: every signing step checks for its
secrets first and is skipped, with a log line, if they're absent. An unsigned
release is still a complete, working release; signing only removes OS trust
warnings (Gatekeeper, SmartScreen, `apt`/`dnf` signature checks).

The installers **don't** bundle a llama.cpp runtime (that's the bulk of the
old package size). The app installs one on first run via ggml-org's official
installer — GPU build when available, CPU otherwise — see
[`docs/ARCHITECTURE.md`](ARCHITECTURE.md) and `internal/intent/llama/runtime.go`.
Nothing about llama.cpp is fetched or staged at package time.

The **recommendation catalog** is *not* in the installers or the repo — the
app downloads it (~210 MB, `catalog.archive_url`) and decompresses it on
first launch. Releases need nothing for this. See
[`docs/CATALOG.md`](CATALOG.md).

Application releases must never attach `catalog.tar.zst`, a catalog directory,
SQLite catalogs or vector data. Keep local catalog build outputs in place for
development; their presence under `bin/` does not make them release assets.
The release workflow uploads exact application package filenames, then checks
the assembled `dist/` directory against
[`build/release-assets.txt`](../build/release-assets.txt). Publication fails on
unexpected entries (including directories and symlinks), empty files or missing
packages. Only the validated paths reach the GitHub Release action. The later
winget step uploads its specifically named metadata ZIP separately.

Run `bash scripts/validate-release-assets.sh dist` to validate an assembled
release and print its upload list. Never manually upload `bin/*` or `dist/*`.
When intentionally adding an application package format, update the explicit
workflow paths and approved list together. The guard regression tests run in
CI and `scripts/test.sh` via `bash scripts/test-release-assets.sh`.

### Verify Linux package architecture

The DEB/RPM/Arch packaging tasks must export the target `GOARCH` to nfpm, not
only to the Go compiler. An unset `${GOARCH}` in `nfpm.yaml` defaults to amd64,
even on a native ARM64 runner. The tasks explicitly set it from `ARCH`; the
packaging regression in `build/linux_packaging_test.go` exercises all three
formats with native, amd64, and arm64 targets using the real Wails task runner.
It skips when Wails is unavailable or the test host has no POSIX shell.

Before publishing, inspect package metadata as well as filenames. For example,
`dpkg-deb --field playlist-ai-arm64.deb Version Architecture` must report
`arm64`, and `.PKGINFO` in the ARM64 Arch archive must declare `aarch64`.
Confirm the packaged executable's ELF architecture and digest match the portable
archive. A successful package build alone does not catch a mislabeled header.

For the immutable v0.10.0 tag, which predates the task fix, set `GOARCH` explicitly
when reproducing packages. Its ARM64 DEB/RPM/Arch assets were repackaged before
publication from the unchanged CI-built ARM64 executable, using the tagged
configuration and Wails v3.0.0-beta.16:

```sh
# In a checkout of v0.10.0, with its CI-built ARM64 binary at bin/playlist-ai:
wails3 task linux:generate:dotdesktop
GOARCH=arm64 SOURCE_DATE_EPOCH=1789077963 wails3 tool package \
  -name playlist-ai -format deb -config build/linux/nfpm/nfpm.yaml -out bin
# Repeat with -format rpm and -format archlinux, then use the release filenames.
```

The release notes record this packaging-only correction. Do not rewrite a pushed
tag or replace its executable with code from a later commit to repair metadata.

## Cutting a release

Windows packaging requires `scripts/install-clap-toolchain.ps1` (also called by
`scripts/setup.ps1`). The installer verifies a pinned upstream LLVM-MinGW archive
and installs it under the local build-tools directory, not application user data.
CI and release workflows run it before `scripts/build.ps1`. The build wrapper
requires cgo and validates both architecture/build metadata and, for runnable
targets, `playlist-ai.exe --check-audio-worker`. The probe exits without starting
the GUI or downloading models. Do not ship `CGO_ENABLED=0` Windows builds as
analysis-capable packages. Compilers and models are not release assets.

The original v0.8.0 tag predates this packaging correction. Its existing Windows
installers still lack the worker; a newly built application is required.

1. Bump the version in two places (they must match):
   - `build/config.yml` → `info.version`
   - `build/linux/nfpm/nfpm.yaml` → `version`

   Then run `wails3 update build-assets -name "playlist-ai" -binaryname "playlist-ai" -config build/config.yml -dir build`
   from the repo root to propagate `info.version` into the macOS `Info.plist`s
   and the Windows manifest/NSIS defines. That command also resets a few
   `nfpm.yaml` fields it doesn't know about (`section`, `maintainer`,
   `homepage`, `license`) back to placeholders — reapply those from git
   history (`git diff build/linux/nfpm/nfpm.yaml`) or copy them back by hand
   before committing.
2. Commit the version bump.
3. Tag and push:
   ```sh
   git tag v0.2.0
   git push origin v0.2.0
   ```
4. The `Release` workflow builds all three OSes and opens a **draft** release
   with every artifact attached. Review it, edit the notes if you want, then
   publish it from the GitHub UI — nothing goes live automatically.

## Signing secrets

All secrets are optional repository secrets (Settings → Secrets and variables
→ Actions). Set none of them and you still get a complete unsigned release.

### Linux — PGP-signed `.deb`/`.rpm`

| Secret | Value |
| --- | --- |
| `LINUX_PGP_PRIVATE_KEY` | An ASCII-armored PGP private key (`gpg --export-secret-keys --armor <key-id>`) |
| `LINUX_PGP_PASSWORD` | The key's passphrase (omit if the key has none) |

### macOS — signed (and optionally notarized) `.app`/`.dmg`

| Secret | Value |
| --- | --- |
| `MACOS_CERTIFICATE_P12` | A **Developer ID Application** certificate + private key, exported from Keychain Access as `.p12`, then base64-encoded (`base64 -i cert.p12 \| pbcopy`) |
| `MACOS_CERTIFICATE_PASSWORD` | The `.p12` export password |
| `MACOS_SIGNING_IDENTITY` | The identity string, e.g. `Developer ID Application: Your Name (TEAMID)` |
| `APPLE_ID` | Apple ID email, for notarization (optional — signing works without it) |
| `APPLE_TEAM_ID` | Your 10-character Apple Developer Team ID |
| `APPLE_APP_SPECIFIC_PASSWORD` | An [app-specific password](https://support.apple.com/en-us/102654) for that Apple ID |

Setting only the certificate secrets gets you a **signed but not notarized**
`.app`; macOS will still show a Gatekeeper prompt on first launch (dismissible
via right-click → Open), but no "damaged app" error. Adding the three
`APPLE_*` secrets additionally notarizes and stamps it, removing that prompt.
Requires an active [Apple Developer Program](https://developer.apple.com/programs/)
membership.

### Windows — Authenticode-signed installer

| Secret | Value |
| --- | --- |
| `WINDOWS_CERTIFICATE_PFX` | A code-signing certificate + private key as `.pfx`, base64-encoded |
| `WINDOWS_CERTIFICATE_PASSWORD` | The `.pfx` export password |

Most CAs now issue EV code-signing certs only on a hardware token, which can't
be dropped into CI as a base64 secret — this path assumes a standard
(non-EV) OV certificate exported as `.pfx`. Without it, Windows SmartScreen
will warn on first run until the binary builds enough install reputation on
its own; this is expected and not a bug.

## The llama.cpp runtime (not part of releases)

Nothing about llama.cpp is bundled, fetched, or staged at package time. On
first run the wizard's model step calls `InstallLlamaRuntime`, which runs
ggml-org's official installer (`sh -c 'curl -fsSL https://llama.app/install.sh | sh'`
on macOS/Linux, `powershell -c 'irm https://llama.app/install.ps1 | iex'` on
Windows) **twice**: once for a GPU-capable build (CUDA / ROCm / Vulkan /
Metal when the machine has one, CPU otherwise), once with the GPU probes
skipped to get a plain CPU build. Both are copied into
`<data dir>/llama/{llama-primary,llama-cpu}` (macOS gets only the Metal
build). `internal/intent/llama` tries `primary` first and falls back to
`cpu` if it won't start / go healthy for a model. `DetectRuntime` still
covers a manually-installed runtime (PATH, `~/.local/bin`, `~/.llama-app`,
next to the app, or `ai.llama_server_path`) and knows to run the unified
binary as `llama serve`.

## The catalog (not part of releases)

The recommendation catalog is downloaded by the app on first launch, not
shipped. Releases don't touch it. The compressed archive is hosted off-repo
at `catalog.archive_url` (`config.Default()`), with a pinned size + SHA-256;
the first-run wizard's catalog step fetches and unpacks it behind
a progress popup the first time the app runs. To rebuild or re-host it, see
[`docs/CATALOG.md`](CATALOG.md).

## winget

The NSIS installer is winget-compatible (`InstallerType: nullsoft`, supports
`/S` and registers a `QuietUninstallString`).

- **On each release**, the `winget` job in `release.yml` renders the manifest
  templates in `build/windows/winget/` with this version, the release download
  URLs, and the installers' SHA-256s, and attaches them to the release as
  `platten.PlaylistAI-<version>-winget.zip` — usable directly with
  `winget install --manifest <unzipped dir>`.
- **When you publish the draft release**, `winget-submit.yml` opens the
  version-bump PR against `microsoft/winget-pkgs` via
  `vedantmgoyal9/winget-releaser`. It only runs if the optional `WINGET_TOKEN`
  secret is set (a classic PAT with `public_repo` scope, on an account that has
  forked `microsoft/winget-pkgs`). You can also trigger it by hand
  (`workflow_dispatch`, passing the tag).

Bump `ManifestVersion` in the three template files if the winget schema moves on.

## Known gaps

- The macOS "Liquid Glass" icon (`Assets.car`, built from
  `build/appicon.icon/`) is generated during a macOS packaging run with
  Xcode 26 or newer and is not checked in. Its full-size `playlist-ai.png`
  layer is a copy of `build/appicon.png`; update both when changing the artwork.
  See [Apple's Icon Composer guide](https://developer.apple.com/documentation/xcode/creating-your-app-icon-using-icon-composer)
  for the 1024-pixel canvas and supported layer formats.
  Icon generation clears old compiled assets before rebuilding, and bundle
  assembly removes any previously bundled asset catalog so an unsupported
  toolchain cannot retain an outdated icon. The files
  `build/darwin/icons.icns` / `build/windows/icon.ico` / `build/appicon.png`
  (regenerated from `build/appicon.svg` via `wails3 generate icons`) are the
  cross-platform fallbacks and are checked in.
- MSIX packaging is scaffolded (`build/windows/msix/`) but not part of the
  release matrix — `wails3 tool msix` currently expects a Wails v2-style
  `wails.json` this project doesn't have. NSIS is the supported Windows
  installer (and is what winget consumes — see above).
- `wails3 package` (the bare CLI command) only builds the platform's default
  artifact (an unpackaged `.app` on macOS, no `.dmg`); the release workflow
  calls the more specific `wails3 task linux:package` / `darwin:package` +
  `darwin:create:dmg` / `windows:package` tasks directly, with `codesign` /
  `xcrun notarytool` run directly (not through `wails3 tool sign`) on macOS,
  and `wails3 tool sign` on Windows.
- Contributor wrappers mirror those native tasks: `scripts/build.sh` packages
  on Linux/macOS, while `scripts/build.ps1 -Architecture amd64|arm64|all`
  packages NSIS installers on Windows. They do not sign, notarize, tag, or
  publish releases.
- The catalog and the llama.cpp runtime are both set up by the app on first
  launch, so every package format (AppImage included) behaves the same.

## Startup application updates

Production builds now read `info.version` from `build/config.yml` for the running
application version as well as installer metadata. Verify the packaged binary
with `playlist-ai --version`. Keep the existing release asset names and ensure
GitHub reports a SHA-256 digest for every application asset. Preserve the macOS
portable `.app` ZIP upload; the updater also supports the existing arm64 DMG.
For an Intel macOS release, publish `playlist-ai-macos-amd64.zip` explicitly.

See [Application updates](application-updates.md) for the startup prompt,
installer/portable behavior, trust boundaries, recovery and platform test matrix.
Before publishing the first updater-enabled release, execute upgrades on Windows
(NSIS/UAC and portable), macOS (signed bundle and DMG), and Linux (AppImage and
portable) using packaged builds. Existing 0.7.0 binaries do not gain an updater
until users install an updater-enabled build.
