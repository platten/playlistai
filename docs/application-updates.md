# Application updates

Production desktop builds check the latest published stable release of
`platten/playlistai` once at startup. A newer version opens a dismissible dialog
with release notes, download size, **Update and restart**, **Later**, and a release
page link. Development builds do not check or replace themselves. A network
failure is logged and never prevents using the application. No prompts, taste
profiles, playlists or credentials are sent in the check.

Production version identity comes from the existing `info.version` in
`build/config.yml`. `playlist-ai --version` reports that value without starting
the desktop or models. An explicit `bridge.Version` linker override still takes
precedence. Bump installer metadata and this configuration together as described
in `RELEASING.md`; there is no additional version constant to maintain.

## Packages and permissions

| Installation | Update behavior |
| --- | --- |
| Linux AppImage | Replace the original `APPIMAGE` file, not the temporary mounted executable. |
| Linux portable archive | Replace the executable in its writable installation folder. |
| Windows portable ZIP | Replace the executable after exit, retrying short-lived file locks. |
| Windows NSIS installation | Detect `uninstall.exe`, download the matching installer, close the app, request Windows UAC approval, install silently into the existing folder and relaunch. |
| macOS `.app` | Replace the complete bundle; use the release ZIP or the arm64 DMG fallback. Verify architecture and code signature, preserving the signing team when the current app has one. |
| OS-managed Linux packages or protected portable/macOS folders | Explain the restriction and offer the release page. Do not overwrite package-manager-owned files or change folder permissions. |

Current release naming includes amd64/arm64 Linux and Windows artifacts. The
legacy macOS ZIP/DMG names are only selected for arm64; an Intel macOS release
must provide `playlist-ai-macos-amd64.zip`. Unknown architectures or missing
packages result in an explanation rather than a guessed download.

## Verification and handoff

The backend owns the checked release and selected asset. Installation accepts
no frontend URL, filesystem path or version override. Only exact official
release URLs and HTTPS redirects to GitHub release hosts are accepted. Downloads
must match the GitHub-provided SHA-256 digest and declared size (maximum 512 MiB).
The shared bounded exponential retry policy applies to the check and download.
Checks have a 15-second timeout; installation preparation has a 15-minute budget.
The UI reports download progress in decimal MB and permits cancellation before
handoff. Release notes render as plain text.

ZIP/tar extraction rejects traversal, absolute paths, links, special files,
duplicate files and unexpected entries, and limits expanded data to 1 GiB and
4,096 entries. Executable headers must match the selected architecture. macOS
DMGs are mounted read-only using `hdiutil`; `ditto` copies only the verified app
bundle. These are operating-system tools, not additional desktop dependencies.

A private staging folder holds the verified payload and a copy of the currently
running application as a helper. `--app-update-worker` is dispatched before GUI,
LLM or catalog initialization. Windows helpers do not create console windows.
The parent waits for a readiness marker, then writes a commit marker and quits
normally so its model workers and databases can close. The helper waits up to
two minutes for parent exit; it never kills the application and never replaces
it without that commit. AppImage loader environment variables are removed
before starting the helper and the new application.

For direct replacement, both the installed and staged application hashes are
checked again. The helper retains the old binary/bundle, replaces the target,
and relaunches it. Replacement or immediate launch failure restores the old
application when possible. A failure to restore reports the backup location.
Windows NSIS installs retain an executable backup in the user's update cache;
the installer manages elevated installation and reports UAC cancellation or a
nonzero exit code. This path does not promise transactional rollback of all
installer changes. The verified installer is held against writes/deletion while
the helper independently rechecks its digest against GitHub's latest release
and Windows launches it. This second check requires connectivity and aborts if
the release changed after download. No command shell or Python runtime is involved.

Update outcomes are saved beside the staged job and failures are shown at the
next startup. Successful staging folders older than one minute are cleaned on a
subsequent startup; failed jobs retain recovery files. Direct updates stage next
to the application to keep renames on the same filesystem. NSIS jobs stage under
`<user cache>/playlist-ai/updates/`. Models, settings, history and taste stores
are outside both replacement targets. Unsaved playlists should be saved before
accepting a restart.

Relaunch success means process creation (or macOS `open` success), not a guarantee
against a later application crash. Backups remain available until subsequent
startup cleanup. Abrupt power loss or helper termination between rename steps
can require recovery from the retained backup. GitHub HTTPS and its asset digest
are the trust boundary; the digest is not an independent publisher signature.

## Validation and reproduction

```sh
bash scripts/test.sh
go build -tags production -o /tmp/playlist-ai-version-check .
/tmp/playlist-ai-version-check --version
go test ./internal/updater
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/updater-amd64.exe ./internal/updater
GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go test -c -o /tmp/updater-arm64.exe ./internal/updater
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go test -c -o /tmp/updater-arm64-macos ./internal/updater
# With Vite running on port 9245 and Playwright/Chromium available:
node scripts/capture-update-ui.mjs <playwright/index.mjs> <chromium-executable>
```

Deterministic tests cover numeric version ordering, drafts/prereleases, unknown
versions, platform selection, missing/mismatched asset identity, checksums,
truncated/oversized downloads, retries, cancellation, hostile archives, changed
payloads, rollback and development-build abstention. A subprocess test verifies
that the helper waits for parent exit and requires a committed handoff. Browser
fixtures cover both themes, keyboard focus, narrow windows, reduced motion,
progress, cancellation, retry, dismissible errors, permissions and offline
startup. Cross-compilation is not an executed Windows UAC, macOS Gatekeeper,
AppImage/FUSE or signed-release upgrade test; those require the respective host
and packaged release. No release is published or installed by these checks.

Implementation references: [GitHub release API and asset digests](https://docs.github.com/en/rest/releases/releases),
[Windows ShellExecute elevation](https://learn.microsoft.com/en-us/windows/win32/shell/launch),
and [NSIS installer command-line options](https://nsis.sourceforge.io/Which_command_line_parameters_can_be_used_to_configure_installers).
