# Playlist AI

Turn a plain-language prompt — *"upbeat instrumental stuff like Justice, 20
tracks"* — into a playlist, then push it to Qobuz (or anywhere) via Soundiiz.

- **Local-first.** A local llama.cpp model parses your prompt into a small
  structured intent; a vector walk over a locally-stored music-embedding catalog
  picks the tracks. No account, no cloud inference, works offline.
- **The model never picks songs.** It only translates text → intent. Track
  selection is deterministic and reproducible.
- **Then it leaves.** Optionally resolve each track's ISRC via MusicBrainz, then
  hand off to Soundiiz (a tokenless browser handoff — it matches the catalog and
  writes to your service) or download a CSV you import yourself.

Go + [Wails v3](https://v3.wails.io) desktop app (macOS / Windows / Linux;
GTK4 / WebKitGTK 6.0 on Linux), React + TypeScript frontend, pnpm + Taskfile
build.

## Status

All nine planned milestones are done — see [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
for the design and the milestone list, and [`docs/RELEASING.md`](docs/RELEASING.md)
for how releases are cut.

Every installer/portable archive (except AppImage — see `docs/RELEASING.md`'s
known gaps) bundles a CPU build of `llama-server` so the local model works out
of the box; set `ai.llama_server_path` in your config (`PLAYLISTAI_CONFIG=path/to/config.toml`)
to point at a GPU-accelerated llama.cpp build instead if you want one (see
[llama.cpp's releases](https://github.com/ggml-org/llama.cpp/releases) for
CUDA/ROCm/Vulkan variants).

The **recommendation catalog** (~957k tracks, derived from the Deej-AI
dataset) ships too — compressed to ~210 MB and committed to the repo via
Git LFS at `build/catalog-dist/catalog.tar.zst`. Every installer/portable
archive (again except AppImage) carries it, and the app decompresses it on
first launch behind a one-time "Decompressing dataset" step. Nothing to
download, no account, no config. See [`docs/CATALOG.md`](docs/CATALOG.md) for
how it's built and how to regenerate it.

## Develop

This repo uses **Git LFS** for the compressed catalog
(`build/catalog-dist/catalog.tar.zst`, ~210 MB). Install it *before* cloning
(`git lfs install`), or, if you already cloned, run `git lfs pull` once —
otherwise that path is a small text pointer and `wails3 package` will ship an
empty catalog. `go test ./...` and `wails3 build` don't need it; only
packaging does.

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
(both GPL-3.0), and the embedding catalog it recommends over (self-hosted —
see [`docs/CATALOG.md`](docs/CATALOG.md)) is derived from Deej-AI's
pre-computed datasets. See [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).
