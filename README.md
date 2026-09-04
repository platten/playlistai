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

Go + [Wails v2](https://wails.io) desktop app (macOS / Windows / Linux), React +
TypeScript frontend.

## Status

Early implementation — milestone 1 (skeleton). See
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the design and the milestone
list.

## Develop

```bash
# Go core (no toolchain beyond Go needed)
go test ./...

# Frontend
cd frontend && npm ci && npm run build

# Full desktop app (needs the Wails CLI + platform webview deps)
go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0
wails dev
```

On Linux, `wails` needs `libgtk-3-dev` and `libwebkit2gtk-4.1-dev`.

## Licensing

GPL-3.0. The recommendation technique is ported from
[teticio/Deej-AI](https://github.com/teticio/Deej-AI) and
[teticio/deej-ai.online-app](https://github.com/teticio/deej-ai.online-app)
(both GPL-3.0), and the bundled embedding catalog is derived from Deej-AI's
pre-computed datasets. See [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).
