# Playlist AI — Architecture

A cross-platform desktop app that turns a natural-language prompt into a playlist
by walking a pre-computed music-embedding space, then hands that playlist to a
streaming service via Soundiiz. Go + [Wails v3] backend (GTK4 / WebKitGTK 6.0 on
Linux), React + TypeScript frontend, pnpm + Taskfile build.

> **Status:** early implementation. This document tracks the design as built.
> An earlier revision described a local-audio-analysis app (ONNX encoder, library
> scanning); that approach was dropped — see [§8](#8-relationship-to-deej-ai).

---

## 1. Principles

- **Local-first core.** `prompt → MusicIntent → playlist` runs entirely on the
  user's machine: a local llama.cpp model parses the prompt, and a vector walk
  over a locally-stored catalog selects tracks. The only network calls are
  optional and user-initiated (first-launch asset downloads, MusicBrainz
  enrichment, Soundiiz export, preview playback).
- **The LLM is a translator, not a recommender.** Its entire job is
  `natural language → MusicIntent` (a small JSON struct). It never sees the
  catalog and never names or ranks output tracks. All selection is deterministic
  Go.
- **Swappable backends.** Every hard dependency sits behind an interface in
  `internal/ports` with an in-memory fake in `internal/fakes`. Implementations
  never import each other; they are wired only in `internal/app`.
- **Minimal global state.** One `*app.Container`, built in `app.New`. `context`,
  `*slog.Logger`, and config are passed explicitly. No package-level singletons.
- **Graceful degradation.** Before the model is downloaded, a rule-based parser
  produces a coarser `MusicIntent` so the app still works.

---

## 2. Data flow

```
FIRST LAUNCH (one-time, user-initiated, with progress bars):
  • llama GGUF  ───► OS data dir      • catalog (int8 vecs + SQLite) ───► data dir

  local, offline: the recommendation core
  ────────────────────────────────────────
  user prompt ──► IntentParser ──MusicIntent──► RecommendationEngine ──► Playlist
  (llama / rules)                                 │  resolve seeds (Catalog)
  UI sliders: creativity / noise /                │  blended-cosine kNN (SimilarityEngine)
  lookback / count  ─── override, re-run ────────►│  + Gaussian noise + dedup (artist/id)

  optional, online, user-initiated (progress-tracked):
  ───────────────────────────────────────────────────
  Playlist ──► Enricher (MusicBrainz: ISRC, album, year) ──► Exporter (Soundiiz handoff | CSV) ──► Qobuz
           └─► PreviewProvider (Deezer public API → Spotify CDN fallback) → 30s playback

INVARIANT: prompt → playlist is 100% local. The LLM only does text → MusicIntent.
```

The canonical copy of this diagram lives in `internal/app/doc.go`.

---

## 3. Ports

Primary (`internal/ports`):

| Port | Responsibility |
|---|---|
| `IntentParser` | `natural language → core.MusicIntent`. Local only (llama.cpp or `rules`). No catalog access. |
| `SimilarityEngine` | Rank catalog tracks by blended cosine similarity to a query. Brute force, matching upstream. |
| `RecommendationEngine` | The only component that turns a `MusicIntent` into an ordered `core.Playlist`. Deterministic given `(intent, catalog, seed)`. |

Supporting:

| Port | Responsibility |
|---|---|
| `Catalog` | Read-only shipped dataset: metadata, the two embedding spaces, `Resolve` (token search). |
| `Enricher` | `[]TrackRef → []EnrichedTrack` (ISRC + metadata) via MusicBrainz. Never fails the batch for one miss. |
| `Exporter` | Send a playlist out — `soundiiz-handoff` (tokenless POST to `soundiiz.com/go/import-playlist`, open the returned `shareUrl`) or `csv` (always available, no network). |
| `PreviewProvider` | Resolve a ~30s preview URL, no API key. `deezer` then `spotifycdn`. |
| `Progress` | Coarse progress updates for any operation that can exceed ~5s. |

Every port has a deterministic in-memory implementation in `internal/fakes`.

---

## 4. `MusicIntent`

The whole contract between the LLM and the deterministic engine
(`internal/core/intent.go`). `Normalized()` clamps every field and applies
defaults; the engine never trusts a raw parse.

```go
type MusicIntent struct {
    Version      int
    Seeds        IntentSeeds       // Queries (catalog search strings) or resolved TrackIDs
    Count        int               // 1..100
    Mode         Mode              // "similar" (1 seed) | "journey" (>=2 seeds)
    Creativity   float64           // 0..1 blend of the two embedding spaces
    Noise        float64           // 0..1 "drunk" — Gaussian added to the query vector
    Lookback     int               // 1..10 running-average window
    Constraints  IntentConstraints // artist excludes; no-repeat-artist; exclude-seed-artists
    NotesForUser string            // shown in UI, never used for selection
}
```

The dataset carries no year / genre / BPM / duration / explicit metadata, so
those are *not* constraints. `Creativity`, `Noise`, `Lookback`, and `Count` are
live UI sliders: changing one re-runs `Build` with the same resolved seeds and no
re-parse.

---

## 5. Package layout

```
main.go                     Wails v3 shell (root; application.New + one window)
Taskfile.yml                wails3 build/package entry (per-OS Taskfiles in build/)
build/                      generated packaging assets (icons, Info.plist, nsis, nfpm, appimage)
cmd/
  catalogpack/  maintainer tool: build/catalog/ -> build/catalog-dist/catalog.tar.zst
                (tar + zstd). That archive is git-ignored and hosted off-repo
                (catalog.archive_url); the app downloads it on first launch.
internal/
  core/       domain types only — zero framework imports
  ports/      the interfaces (+ Progress helpers)
  fakes/      in-memory implementations for tests
  config/     TOML load + validate → immutable Config
  app/        composition root: Container, New, Close; doc.go pipeline diagram
  bridge/     Wails v3 Service (API) + WailsProgress event emitter (thin; no logic)
  catalog/    Open(dir): mmap vectors.i8 + read-only catalog.sqlite (modernc, pure Go);
              ports.Catalog — Len/Dim/ID/RowOf/Meta/Vectors + Resolve (token-substring
              over a normalized search column; search.go mirrors python normalize_search)
  dataset/    Download (HTTP Range resume, optional sha256/size, atomic rename) +
              LoadManifest + Fetch; Download is reused by modelmgr.
              bundle.go: DownloadArchive (fetch a hosted catalog.tar.zst),
              Unpack (decompress + verify into catalog.dir), FindBundledArchive
              (a locally-staged archive next to the app)
  similarity/ brute/ — brute-force blended-cosine engine over ports.Catalog;
              reads int8 rows via RawRow (no float32 copy), precomputed per-row
              inverse norms, bounded top-K heap, deterministic tie-break by row.
              Matches deej-ai.online-app most_similar.
  reco/       deejai/ — Go port of backend/deejai.py: make_playlist (single seed) +
              join_the_dots (>=2 seeds), seeded Gaussian noise, id/display/artist
              dedup; deterministic given (intent, catalog, intent.Seed)
  intent/     rules/  — dependency-free regex/keyword prompt → core.MusicIntent
                        (always available; the fallback)
              schema/ — LLM wire shape + GBNF grammar + response → core.MusicIntent
              llama/  — llama runtime child process (Server; `llama-server` or
                        the unified `llama serve`) + streaming
                        /v1/chat/completions client; runtime.go: DetectRuntime
                        (PATH / ~/.local/bin / ~/.llama-app / next-to-app) +
                        InstallOfficial (ggml-org's installer, GPU-aware)
              modelmgr/ — embedded GGUF catalog (models-manifest.json),
                          resumable download (skips if present), GGUF magic check
  enrich/     [M7] musicbrainz/
  export/     [M7] soundiizcsv/ soundiizhandoff/
  preview/    [M8] deezer/ spotifycdn/
frontend/     Vite + React + TS + @wailsio/runtime; pnpm; Tailwind v4 + Radix
  src/design/     tokens.css (dark + light palette, @theme inline) · theme.ts (system/explicit/reduced-motion)
  src/components/ ProgressBar (+ useProgress), EmptyState, LoadingState,
                  ErrorState, Slider, Stepper, TrackRow, Button, icons,
                  (catalog download+unpack lives in the first-run wizard's
                  a blocking popup before the app renders, if one is present)
  src/screens/    GenerateScreen (prompt → parsed-intent chips → playlist),
                  CatalogSearch (search / "similar to X" / first-launch download),
                  PlaylistScreen (live creativity/noise/lookback/count + Regenerate),
                  SettingsScreen (AI-model panel: catalog download / use-a-file /
                  back-to-rules), Gallery; [M7+] ReviewExport, FirstRun
  src/lib/api.ts  re-export of the generated bindings
  bindings/       generated by `wails3 generate bindings` (gitignored)
python/       catalogfmt.py (shared format) · fetch_pickles.py (Google Drive
              fetch + sanity check) · convert_pickles.py · make_test_catalog.py
              · parity_playlist.py [M5]  (build-time tooling, not shipped)
models/       catalog-manifest.json  (asset URLs + checksums; blobs never committed)
```

---

## 6. Configuration

One TOML file over `config.Default()`; missing keys keep defaults; `Validate()`
runs before the app starts. Key sections: `[catalog]` (`dir`; `archive_url` +
`archive_size` + `archive_sha256` for the first-launch download — defaulted;
`manifest_url` and `bundle_path` alternatives), `[ai]` (model id/path, n_ctx,
threads), `[enrich]`
(MusicBrainz user-agent — required — cache path, min match score), `[preview]`
(`deezer` | `spotify` | `off`). Export needs no configuration — the Soundiiz
handoff is tokenless.

---

## 7. Testing

- `go test ./...` needs only the Go toolchain — no llama, no network. `catalog`
  tests run against committed synthetic fixtures in
  `internal/catalog/testdata/` (regenerate with `python/make_test_catalog.py`);
  `dataset` tests run against an in-process `httptest` server.
- `catalog/search.go`'s `normalizeSearch` is asserted row-for-row against the
  `search` column Python wrote into the fixture, keeping the two normalizers in
  step.
- Each port is exercised through its fake; the real implementations add
  contract/parity tests.
- The load-bearing test is a golden-parity check: `python/parity_playlist.py`
  (a stdlib-only reimplementation of upstream `backend/deejai.py`, `noise=0`)
  emits `internal/reco/deejai/testdata/golden/*.json` from the synthetic
  catalog; the Go engine must reproduce each sequence within edit distance 1
  (exact on the first 3 picks). It currently matches all 7 fixtures exactly.

---

## 8. Relationship to Deej-AI

The recommendation technique comes from [teticio/Deej-AI] and its web backend
[teticio/deej-ai.online-app], both **GPL-3.0**:

- `internal/reco/deejai` is a Go port of `backend/deejai.py` — a blended-cosine
  similarity walk over two 100-dimensional embedding spaces (`spotifytovec.p`,
  audio-content; `tracktovec.p`, Spotify-playlist co-occurrence), blended by a
  `creativity` weight, with additive Gaussian "noise" and artist/id dedup.
- The catalog is those pre-computed vectors, converted to L2-normalized int8 +
  SQLite (`python/convert_pickles.py`) and downloaded on first launch from
  `catalog.manifest_url`. **This project does not build, host, or embed that
  catalog itself** — `catalog.manifest_url` defaults to empty, so a fresh
  install has nothing to recommend over until an operator self-hosts one; see
  [`docs/CATALOG.md`](CATALOG.md) for the (untested-against-real-data, but
  fully wired) conversion + hosting steps.

Consequently **Playlist AI is licensed GPL-3.0**. See `LICENSE` and `NOTICE`;
an operator who hosts and distributes the converted catalog takes on the
written offer of source for the data and `python/convert_pickles.py` that
implies.

---

## 9. Milestones

1. **Skeleton** — layout, core types, ports + fakes, `Container`, config, Wails
   shell, `Progress` contract, CI (lint / test / cross-compile). *(done)*
2. **Design pass** — mockup canvas; Tailwind v4 + Radix; `src/design/` tokens +
   theme; shared components (`ProgressBar` + `useProgress`, `EmptyState`,
   `LoadingState`, `ErrorState`, `Slider`, `TrackRow`, `Button`). *(done)*
3. **Catalog** — `catalogfmt.py` + `convert_pickles.py` + synthetic fixtures;
   `internal/catalog` mmap + SQLite loader + token search; `internal/dataset`
   resumable checksummed download; `CatalogSearch` screen + bridge methods.
   `GetCatalogInfo` reports whether a source is even configured so the UI can
   say so plainly instead of offering a download that's guaranteed to fail.
   The real Deej-AI pickles (`python/fetch_pickles.py`, Google Drive) are
   fetched + converted (956,917 tracks, end-to-end verified), compressed
   (`cmd/catalogpack`), and hosted off-repo; the app downloads + unpacks it
   on first launch (`catalog.archive_url`, milestone 9). See
   [`docs/CATALOG.md`](CATALOG.md). *(done)*
4. **Similarity** — `internal/similarity/brute` blended two-space cosine engine
   (reference-impl parity tested); `SimilarTracks` bridge method; "similar to X"
   view with a creativity slider in the Catalog screen. *(done)*
5. **Recommendation** — `internal/reco/deejai` port of `make_playlist` +
   `join_the_dots` + noise + dedup; `parity_playlist.py` golden fixtures (exact
   match); `BuildPlaylist` bridge method; Playlist screen with live
   creativity / noise / lookback / count controls + Regenerate. *(done)*
6a. **IntentParser (rules)** — `internal/intent/rules` regex/keyword parser;
    `ParseIntent` / `GenerateFromPrompt` bridge methods; Generate screen
    (prompt → parsed-intent chips → playlist). *(done)*
6b. **IntentParser (llama)** — `internal/intent/schema` (GBNF + response parse);
    `internal/intent/llama` (`Server` child process + chat `Client`, subprocess-
    lifecycle tested against a compiled fake); `app` swaps `rules → llama` in the
    background when `ai.model_path` is set. *(done)*
6c. **Model manager** — `internal/intent/modelmgr` (embedded GGUF catalog +
    resumable download, size + SHA-256 pinned and verified — see M9);
    `config.Prefs` persistence; `Container.SetModel / DownloadModel /
    ClearModel`; Settings AI-model panel with live download progress. *(done)*
7. **Enrichment + export** — `internal/enrich/musicbrainz` (SQLite-cached ISRC +
   metadata lookup, 1 req/s rate limit); `internal/export/soundiizcsv` (Soundiiz
   file import) and `internal/export/soundiizhandoff` (tokenless
   `POST /go/import-playlist`, validated share URL, opened in the browser);
   `Container.Enrich / Exporter(name)`; bridge `EnrichPlaylist / ExportCSV /
   OpenSoundiizHandoff`; the ReviewExport screen (enrich progress → per-track
   match table with editable ISRC + include toggle → name + export). *(done)*
8. **Preview** — `internal/preview/deezer` (public Deezer search API, no key,
   in-memory cache, falls back to the bundled Spotify CDN URL on a miss or a
   request failure) and `internal/preview/spotifycdn` (bundled URL only, no
   network — used when `preview.provider = "spotify"`); `Container.wirePreview`
   picks one by `preview.provider` (`"off"` leaves it nil); bridge
   `GetPreviewURL(id)`. Frontend: `PreviewPlayerProvider` / `usePreviewPlayer`
   own a single `<audio>` element and resolve a track's URL on first play;
   `MiniPlayerBar` (play/pause, scrub, close) wired into `TrackRow.onPlay` on
   the Playlist and Catalog screens. *(done)*
9. **Polish & ship** — model integrity hashes pinned (size + SHA-256 in
   `models-manifest.json`, verified against a fresh download of each file;
   surfaced as a "verified" badge in Settings); `.github/workflows/release.yml`
   packages per-OS installers (Linux AppImage/deb/rpm/Arch, macOS `.dmg`,
   Windows NSIS) plus a portable archive per OS on a `vX.Y.Z` tag push, and
   opens a draft GitHub Release — see [`docs/RELEASING.md`](RELEASING.md) for
   the version-bump steps and the optional signing secrets (PGP on Linux,
   Developer ID + notarization on macOS, Authenticode on Windows — each step
   skips cleanly without its secrets); fixed several stale placeholder values
   left over from the M1 scaffold (`nfpm.yaml`, `.desktop` files, macOS
   `Info.plist`s, the Windows manifest/NSIS defines — wrong binary name,
   `MIT`/`My Company`/`com.example.*` instead of the real GPL-3.0 metadata).
   First-run wizard (`FirstRunWizard.tsx`): welcome → catalog download → model
   choice/download/skip → preview provider choice → done; `config.Prefs`
   gains `PreviewProvider` + `OnboardingDone` (and `Container.SetModel` /
   `ClearModel` were fixed to read-modify-write prefs.json instead of
   overwriting it, which used to silently erase these new fields);
   `Container.SetPreviewProvider` makes the preview backend swappable at
   runtime, also exposed in Settings. llama.cpp runtime — **not bundled**
   (that's most of the old installer size): the wizard's model step runs
   ggml-org's official installer (`InstallLlamaRuntime` → `llama.app/install.sh`
   / `install.ps1`) twice, staging a GPU-capable build (CUDA / ROCm / Vulkan
   / Metal) and a CPU build into `<data dir>/llama/`; a determinate 2-step
   progress bar (op `"llama-install"`). `llama.New` tries the GPU build then
   the CPU build — if the GPU one won't start or go healthy for a model, the
   CPU one is used. `DetectRuntime` also covers a manual install (PATH,
   `~/.local/bin`, `~/.llama-app`, next to the app, `ai.llama_server_path`)
   and runs the unified binary as `llama serve`; `ai.gpu_layers` pins/limits
   GPU offload. Catalog on first launch: the app has no catalog in the repo or
   the installer — `cmd/catalogpack` compresses the converted catalog (tar + zstd
   `SpeedBestCompression`, ~210 MB for 956,917 tracks) and it's hosted off-repo (`catalog.archive_url`, Cloudflare R2, with a pinned size + SHA-256). The first-run wizard's catalog step calls
   `DownloadCatalog` automatically: `internal/dataset.DownloadArchive` fetches
   it (resumable, verified), `internal/dataset.Unpack` decompresses it into
   the data dir, both behind one progress popup;
   `Container.EnsureCatalog` drives the source-precedence order (staged local
   archive → `archive_url` → `manifest_url`). Also this milestone: the intent
   parse now streams — the Generate screen shows a live progress bar while the
   local model works (`ParseWithProgress`, op `"intent"`), and it falls back
   to the rules parser if the model errors or times out. See
   [`docs/RELEASING.md`](RELEASING.md) and [`docs/CATALOG.md`](CATALOG.md).
   *(current)*

Remaining known gap: no custom app icon (still the Wails default) — cosmetic,
not a release blocker.

[Wails v3]: https://v3.wails.io
[teticio/Deej-AI]: https://github.com/teticio/Deej-AI
[teticio/deej-ai.online-app]: https://github.com/teticio/deej-ai.online-app
