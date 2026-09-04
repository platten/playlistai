# Releasing

Playlist AI ships two things per platform: a native **installer** (the primary,
recommended path) and a **portable archive** of the raw binary. Both are built
by [`.github/workflows/release.yml`](../.github/workflows/release.yml) and
attached to a draft GitHub Release.

| OS      | Installer                                  | Portable            |
| ------- | ------------------------------------------- | -------------------- |
| Linux   | `.AppImage`, `.deb`, `.rpm`, `.pkg.tar.zst` | `playlist-ai-linux-amd64.tar.gz` |
| macOS   | `.dmg`                                      | `playlist-ai-macos.zip` (the `.app`) |
| Windows | `<name>-<arch>-installer.exe` (NSIS)        | `playlist-ai-windows-amd64.zip` |

Packaging itself (AppImage/deb/rpm/dmg/NSIS) always runs — it needs no secrets.
**Code signing is best-effort and additive**: every signing step checks for its
secrets first and is skipped, with a log line, if they're absent. An unsigned
release is still a complete, working release; signing only removes OS trust
warnings (Gatekeeper, SmartScreen, `apt`/`dnf` signature checks).

Every installer and portable archive (except AppImage — see Known gaps) also
bundles a pinned CPU build of **llama-server** from
[ggml-org/llama.cpp](https://github.com/ggml-org/llama.cpp), so the local
model works out of the box with nothing else to install. This is fetched
automatically by `task <os>:package` (`build/llama/fetch.sh`, pinned tag +
per-OS SHA-256 in `build/llama/manifest.json`) and needs no configuration —
see "Bundled llama-server runtime" below only if you need to update it.

## Cutting a release

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

## Bundled llama-server runtime

`build/llama/manifest.json` pins one llama.cpp release tag plus the exact
asset name + SHA-256 for each OS in our build matrix (`ubuntu-x64`,
`macos-arm64`, `win-cpu-x64` — CPU-only builds; macOS's build includes Metal,
which needs no separate driver so it doesn't count as "GPU-only" for this
purpose). `build/llama/fetch.sh <os> <bin-dir>`:

1. Downloads that one pinned asset and verifies it against the pinned SHA-256
   (hard failure on mismatch — never silently proceeds).
2. Extracts `llama-server[.exe]` plus the runtime libraries it actually needs
   (`.so`/`.dylib`/`.dll` — skipping the extra CLI tools like `llama-cli`,
   `llama-quantize`, ... this app never runs) into `<bin-dir>`, alongside the
   app binary — the first place `internal/intent/llama`'s `resolveBinary()`
   looks.
3. Copies its MIT `LICENSE` file(s) into `<bin-dir>/licenses/`.
4. Stamps the tag into `<bin-dir>/.llama-runtime-tag` so a re-run with the
   same pin is a no-op.

It's wired in as an automatic dependency of the relevant per-OS packaging
tasks (`linux:create:deb/rpm/aur`, `darwin:create:app:bundle`,
`windows:create:nsis:installer`) — `task <os>:package` just does the right
thing, network permitting. `build/linux/nfpm/nfpm.yaml` picks the staged files
up via a glob (`./bin/*.so*`), `build/darwin/Taskfile.yml`'s
`create:app:bundle` copies them into `Contents/MacOS/` before ad-hoc signing
(so `--deep` ad-hoc-signs them too), and `build/windows/nsis/project.nsi`
embeds them via `File` directives. `release.yml` also runs
`bin/llama-server[.exe] --version` right after each OS's packaging step as a
smoke test.

**To bump the pinned build**: check
[llama.cpp's releases](https://github.com/ggml-org/llama.cpp/releases) for a
recent `bNNNNN` tag with binary assets (the plain `latest` tag usually has
none), update the tag + the three asset names + their `sha256sum <file>` in
`build/llama/manifest.json`, delete any stale `bin/.llama-runtime-tag` you
have locally, and re-run `task linux:create:deb` (or the smoke test in CI) to
confirm it still runs.

## Known gaps

- The app icon (`build/appicon.png`, `build/darwin/icons.icns`,
  `build/windows/icon.ico`) is still the Wails default diamond — a custom
  Playlist AI icon is a nice-to-have, not tracked as a release blocker.
- MSIX packaging is scaffolded (`build/windows/msix/`) but not part of the
  release matrix — `wails3 tool msix` currently expects a Wails v2-style
  `wails.json` this project doesn't have. NSIS is the supported Windows
  installer.
- `wails3 package` (the bare CLI command) only builds the platform's default
  artifact (an unpackaged `.app` on macOS, no `.dmg`); the release workflow
  calls the more specific `wails3 task linux:package` / `darwin:package` +
  `darwin:create:dmg` / `windows:package` tasks directly, with `codesign` /
  `xcrun notarytool` run directly (not through `wails3 tool sign`) on macOS
  for the deep-signing + notarization the bundled llama-server needs, and
  `wails3 tool sign` on Windows.
- The AppImage build does **not** bundle llama-server yet — `wails3 generate
  appimage` is a black box (linuxdeploy under the hood) that only deploys the
  dependencies of the one binary it's given, so there's no clean hook to add
  a second executable + its libraries to the AppDir. AppImage users who want
  the local model can install llama-server separately (see llama.cpp's
  releases) and set `ai.llama_server_path`, or use the `.deb`/`.rpm`/Arch
  package instead, which do bundle it.
