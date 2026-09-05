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

The installers **don't** bundle a llama.cpp runtime — that keeps them small.
On first run the setup wizard installs llama.cpp via ggml-org's official
installer (`llama.app/install.sh` / `install.ps1`): a **GPU build** (CUDA /
ROCm / Vulkan / Metal) when one is available **plus a CPU build**, so the
parser can fall back to CPU if a GPU build won't run a given model. Already
have `llama-server` or the unified `llama` binary? It's picked up from
PATH / `~/.local/bin` / `~/.llama-app`, or point `ai.llama_server_path` at
it. `ai.gpu_layers` pins/limits GPU offload.

The **recommendation catalog** (~957k tracks, derived from the Deej-AI
dataset) is a compressed ~210 MB archive the app **downloads on first
launch** (`catalog.archive_url` — a hosted `catalog.tar.zst`), then
decompresses into your data dir behind a one-time progress popup. Nothing to
configure, no account. It is not in the repo and not in the installers. See
[`docs/CATALOG.md`](docs/CATALOG.md) for the hosting details and how to
regenerate it.

## Develop

Nothing special — no Git LFS, no large blobs in the repo. `go test ./...`
needs only Go.

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
