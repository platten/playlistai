# build

Platform packaging assets for `wails build`.

- `appicon.png` — 512×512 source icon (placeholder; replace during the design milestone).
- `wails build` generates the `darwin/` and `windows/` subdirectories with default
  `Info.plist`, manifest, and NSIS installer templates on first run. Commit them
  once they need project-specific edits (bundle id, signing, installer copy).

CI (`.github/workflows/ci.yml`) runs `wails build` per OS; artifacts are uploaded
from `build/bin/`.
