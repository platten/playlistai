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

The installers **don't** bundle a llama.cpp runtime (that's the bulk of the
old package size). The app installs one on first run via ggml-org's official
installer — GPU build when available, CPU otherwise — see
[`docs/ARCHITECTURE.md`](ARCHITECTURE.md) and `internal/intent/llama/runtime.go`.
Nothing about llama.cpp is fetched or staged at package time.

The **recommendation catalog** is *not* in the installers or the repo — the
app downloads it (~210 MB, `catalog.archive_url`) and decompresses it on
first launch. Releases need nothing for this. See
[`docs/CATALOG.md`](CATALOG.md).

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
  `xcrun notarytool` run directly (not through `wails3 tool sign`) on macOS,
  and `wails3 tool sign` on Windows.
- The catalog and the llama.cpp runtime are both set up by the app on first
  launch, so every package format (AppImage included) behaves the same.
