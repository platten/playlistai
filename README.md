# Playlist AI

Turn a plain-language prompt — *"upbeat instrumental stuff like Justice, 20
tracks"* — into a playlist, then push it to Qobuz (or anywhere) via Soundiiz.

- **Local-first.** A local llama.cpp model parses your prompt into a small
  structured intent; a vector walk over a locally-stored music-embedding catalog
  picks the tracks. No account, no cloud inference, works offline.
- **The model never picks songs.** It only translates text → intent. Track
  selection is deterministic and reproducible.
- **Then it leaves.** Optionally resolve each track's ISRC via MusicBrainz and
  export to the Soundiiz API or a CSV you import yourself.

Go + [Wails v3](https://v3.wails.io) desktop app (macOS / Windows / Linux;
GTK4 / WebKitGTK 6.0 on Linux), React + TypeScript frontend, pnpm + Taskfile
build.

## Status

Early implementation — milestone 1 (skeleton). See
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the design and the milestone
list.

## Develop

```bash
# Go core — no toolchain beyond Go needed
go test ./...

# Full desktop app
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.16
wails3 doctor          # check system deps
wails3 dev             # run with hot reload
wails3 build           # build bin/playlist-ai
wails3 package         # per-OS installers (AppImage/deb/rpm, .app/dmg, nsis)
```

`wails3` drives pnpm and the Taskfile; you don't call them directly. On Linux it
needs `libgtk-4-dev` and `libwebkitgtk-6.0-dev`.

## Licensing

GPL-3.0. The recommendation technique is ported from
[teticio/Deej-AI](https://github.com/teticio/Deej-AI) and
[teticio/deej-ai.online-app](https://github.com/teticio/deej-ai.online-app)
(both GPL-3.0), and the bundled embedding catalog is derived from Deej-AI's
pre-computed datasets. See [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).
