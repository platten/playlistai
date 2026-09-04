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
  dataset/    LoadManifest + Fetch: HTTP Range resume, sha256 verify, atomic rename,
              byte progress under op "catalog"
  similarity/ brute/ — brute-force blended-cosine engine over ports.Catalog;
              reads int8 rows via RawRow (no float32 copy), precomputed per-row
              inverse norms, bounded top-K heap, deterministic tie-break by row.
              Matches deej-ai.online-app most_similar.
  reco/       deejai/ — Go port of backend/deejai.py: make_playlist (single seed) +
              join_the_dots (>=2 seeds), seeded Gaussian noise, id/display/artist
              dedup; deterministic given (intent, catalog, intent.Seed)
  intent/     rules/ — dependency-free regex/keyword prompt → core.MusicIntent
              (always available; the llama fallback)
              [M6b] llama/ (llama-server subprocess), schema/ (GBNF), modelmgr/
  enrich/     [M7] musicbrainz/
  export/     [M7] soundiizcsv/ soundiizhandoff/
  preview/    [M8] deezer/ spotifycdn/
frontend/     Vite + React + TS + @wailsio/runtime; pnpm; Tailwind v4 + Radix
  src/design/     tokens.css (dark + light palette, @theme inline) · theme.ts (system/explicit/reduced-motion)
  src/components/ ProgressBar (+ useProgress), EmptyState, LoadingState,
                  ErrorState, Slider, Stepper, TrackRow, Button, icons
  src/screens/    GenerateScreen (prompt → parsed-intent chips → playlist),
                  CatalogSearch (search / "similar to X" / first-launch download),
                  PlaylistScreen (live creativity/noise/lookback/count + Regenerate),
                  Gallery; [M7+] ReviewExport, FirstRun, Settings
  src/lib/api.ts  re-export of the generated bindings
  bindings/       generated by `wails3 generate bindings` (gitignored)
python/       catalogfmt.py (shared format) · convert_pickles.py · make_test_catalog.py
              · parity_playlist.py [M5]  (build-time tooling, not shipped)
models/       catalog-manifest.json  (asset URLs + checksums; blobs never committed)
```

---

## 6. Configuration

One TOML file over `config.Default()`; missing keys keep defaults; `Validate()`
runs before the app starts. Key sections: `[catalog]` (dir + manifest URL),
`[ai]` (model id/path, n_ctx, threads), `[enrich]` (MusicBrainz user-agent —
required — cache path, min match score), `[preview]` (`deezer` | `spotify` |
`off`). Export needs no configuration — the Soundiiz handoff is tokenless.

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
- The bundled catalog is those pre-computed vectors, converted to L2-normalized
  int8 + SQLite and rehosted, downloaded on first launch.

Consequently **Playlist AI is licensed GPL-3.0**. See `LICENSE` and `NOTICE`;
distributions that ship the converted catalog include a written offer of source
for the data and `python/convert_pickles.py`.

---

## 9. Milestones

1. **Skeleton** — layout, core types, ports + fakes, `Container`, config, Wails
   shell, `Progress` contract, CI (lint / test / cross-compile). *(done)*
2. **Design pass** — mockup canvas; Tailwind v4 + Radix; `src/design/` tokens +
   theme; shared components (`ProgressBar` + `useProgress`, `EmptyState`,
   `LoadingState`, `ErrorState`, `Slider`, `TrackRow`, `Button`). *(done)*
3. **Catalog** — `catalogfmt.py` + `convert_pickles.py` + synthetic fixtures;
   `internal/catalog` mmap + SQLite loader + token search; `internal/dataset`
   resumable checksummed download; `CatalogSearch` screen + bridge methods. *(done)*
4. **Similarity** — `internal/similarity/brute` blended two-space cosine engine
   (reference-impl parity tested); `SimilarTracks` bridge method; "similar to X"
   view with a creativity slider in the Catalog screen. *(done)*
5. **Recommendation** — `internal/reco/deejai` port of `make_playlist` +
   `join_the_dots` + noise + dedup; `parity_playlist.py` golden fixtures (exact
   match); `BuildPlaylist` bridge method; Playlist screen with live
   creativity / noise / lookback / count controls + Regenerate. *(done)*
6a. **IntentParser (rules)** — `internal/intent/rules` regex/keyword parser;
    `ParseIntent` / `GenerateFromPrompt` bridge methods; Generate screen
    (prompt → parsed-intent chips → playlist). *(current)*
6b. **IntentParser (llama)** — GBNF `schema`; `llama-server` subprocess +
    lifecycle; `modelmgr` first-launch model download; parser switches to
    `llama` when a model is configured.
7. **Enrichment + export** — MusicBrainz client + cache; CSV + Soundiiz handoff;
   match-review screen.
8. **Preview** — Deezer + Spotify-CDN fallback; play control.
9. **Polish & ship** — per-OS installers + portable binaries; signing;
   first-run wizard; expand this document.

[Wails v3]: https://v3.wails.io
[teticio/Deej-AI]: https://github.com/teticio/Deej-AI
[teticio/deej-ai.online-app]: https://github.com/teticio/deej-ai.online-app
