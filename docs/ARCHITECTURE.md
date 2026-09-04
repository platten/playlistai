# Playlist AI — Architecture

A cross-platform desktop app that builds music playlists by *listening* to your
local library. Go + [Wails v2] backend, React (Vite + TS) frontend.

- **Fully local.** Audio analysis, vector search, and natural-language
  understanding all run on the user's machine. The only network call the app
  ever makes is a one-time, user-initiated model download on first launch.
- **The LLM is a translator, not a recommender.** A local 3–4B GGUF model (via
  llama.cpp) does exactly one job: `natural language -> MusicIntent JSON`. It
  never sees the catalog, never names or ranks tracks. Track selection is
  deterministic Go over local vectors.
- **Everything swappable.** Four ports — `IntentParser`, `AudioEncoder`,
  `SimilarityEngine`, `RecommendationEngine` — each with a production impl and an
  in-memory fake. Implementations never import each other; they meet only in the
  composition root.
- **Minimal global state.** No package-level singletons, no `init()` side
  effects. One `Container` built in `app.New(cfg)`. `context.Context`,
  `*slog.Logger`, and config are passed explicitly.

The core recommendation technique (audio-embedding + similarity walk) is derived
from [`teticio/Deej-AI`], ported to ONNX + Go. See [§9](#9-porting-deej-ai-to-onnx--go)
and the licensing decision in [§13](#13-key-risks--decisions).

---

## Table of contents

1. [Philosophy, boundaries, constraints](#1-philosophy-boundaries-constraints)
2. [Pipeline diagram](#2-pipeline-diagram)
3. [Package layout](#3-package-layout)
4. [Core domain types](#4-core-domain-types)
5. [The four ports](#5-the-four-ports)
6. [MusicIntent schema](#6-musicintent-schema)
7. [Component notes](#7-component-notes)
8. [First-launch model setup](#8-first-launch-model-setup)
9. [Porting Deej-AI to ONNX + Go](#9-porting-deej-ai-to-onnx--go)
10. [State & composition root](#10-state--composition-root)
11. [Build, models, packaging](#11-build-models-packaging)
12. [Testing strategy](#12-testing-strategy)
13. [Key risks & decisions](#13-key-risks--decisions)
14. [Milestones](#14-milestones)

---

## 1. Philosophy, boundaries, constraints

### Philosophy

- **Local-first, no exceptions.** The app is fully functional with zero network
  access. Airplane mode changes nothing about how it works after setup.
- **The LLM is a translator, not a recommender.** Its only output is a
  `MusicIntent`. This keeps a small model's hallucinations out of the playlist,
  and makes every recommendation reproducible and debuggable.
- **Everything swappable.** The four ports isolate every hard dependency (ONNX,
  llama.cpp, the vector index, the selection algorithm) behind an interface with
  a fake.
- **Minimal global state.** One composition root. Nothing reaches for a
  singleton.
- **Graceful degradation.** Before the model is downloaded — or on a machine
  that can't load it — a rule-based parser produces a coarser `MusicIntent` so
  the app still builds playlists.

### Hard boundaries

| Boundary | Rule |
|---|---|
| LLM ↔ catalog | LLM input is user text + optional seed track *references*. Output is a `MusicIntent`. It receives no track lists, no metadata dumps, no embeddings. |
| Intent ↔ selection | `RecommendationEngine` is the *only* component that produces an ordered track list. Given `(intent, index, catalog, seed)` it is deterministic. |
| Core ↔ frameworks | `internal/core` imports nothing from Wails, ONNX, SQLite, or HTTP. Frameworks live at the edges. |
| App ↔ network | No component makes a network call except the one-time, user-initiated model download in `internal/intent/modelmgr`. |

### Constraints

- Go + Wails v2 backend; React (Vite + TS) frontend.
- Audio analysis local, via a pre-trained encoder exported to **ONNX**, run
  through ONNX Runtime.
- Embeddings stored **locally** (SQLite for metadata + a float32 blob store for
  vectors).
- LLM: **llama.cpp** with a **3–4B GGUF** instruct model. Recommended default
  *Llama 3.2 3B Instruct Q4_K_M*; alt *Qwen2.5-3B-Instruct Q4_K_M*. The weights
  are **downloaded by the user on first launch** — never shipped in the
  installer.
- Cross-platform: macOS (arm64/amd64), Windows (amd64), Linux (amd64;
  WebKitGTK).
- Core recommendation tech ported from [`teticio/Deej-AI`] (GPL-3.0 — see §13).

---

## 2. Pipeline diagram

This block lives verbatim in `internal/app/doc.go`.

```go
// Package app is the composition root. It is the only package that constructs
// concrete implementations and wires them to the ports.
//
// ┌───────────────────────────── Playlist AI — data flow (local-only) ─────────────────┐
// │                                                                                    │
// │  music files          ┌──────────────┐  metadata   ┌───────────────┐               │
// │  (mp3/flac/m4a/…) ───► │ LibraryScan  │ ──────────► │ MetadataStore │◄────┐         │
// │        │               │ + tag read   │             │  (SQLite)     │     │         │
// │        ▼ decode/resample 22.05k mono   └───────┬─────┘               │ trackID │    │
// │  ┌───────────────────────────────────────────┐ │ path               │ ↔ vec   │    │
// │  │ AudioEncoder (port)                       │ ▼                     │         │    │
// │  │  decode ► STFT+mel+log-norm (in ONNX) ►   │  ┌───────────────┐    │         │    │
// │  │  ONNX encoder (ORT, opset17) ► sliceVecs ─┼─►│ EmbeddingStore│────┤         │    │
// │  │  aggregate (mean | tfidf) ► trackVec      │  │ (f32 blobs)   │    │         │    │
// │  └──────────────────────────────────────────┘  └───────┬───────┘    │         │    │
// │                                                        │ load       ▼         │    │
// │  user prompt                                           │   ┌────────────────────┐  │
// │  "like this but 90s,      ┌───────────────┐            │   │ SimilarityEngine   │  │
// │   upbeat, no vocals" ───► │ IntentParser  │ MusicIntent│   │  kNN (brute → HNSW)│  │
// │                           │  llama.cpp    │ JSON       │   └─────────┬──────────┘  │
// │   NowPlaying / recent ──► │  (GBNF-       │  │         │             │ neighbors   │
// │   (as TrackRefs only)     │   constrained)│  │         │             │             │
// │                           │  ── or ──     │  ▼         ▼             │             │
// │        ▲ never sees        │  rules (fallback)  ┌──────────────────────────┐       │
// │        │ catalog / tracks  └───────────────┘    │ RecommendationEngine     │◄──────┘
// │        │                                        │  deejai-walk:            │        │
// │  (no cloud. no network after first-run          │  seed → running centroid │  MetadataStore
// │   model download.)                              │  (lookback) → kNN →       │  ──► filter
// │                                                 │  novelty noise →          │  (year/genre/
// │                                                 │  dedup(ε) → constraints   │   artist/…)
// │                                                 └───────────┬──────────────┘        │
// │                                                             ▼                       │
// │                                                    ┌──────────────┐                 │
// │                                                    │  Playlist    │ ─► UI / .m3u    │
// │                                                    │  + rationale │                 │
// │                                                    └──────────────┘                 │
// │                                                                                    │
// │  INVARIANT: text→MusicIntent is the LLM's entire role, and the LLM is always local. │
// └────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 3. Package layout

```
playlist-ai/
├── cmd/playlistai/            main.go — Wails bootstrap, flags, calls app.New
├── internal/
│   ├── core/                  domain types ONLY. zero framework imports.
│   │     track.go  embedding.go  intent.go  playlist.go  errors.go
│   ├── ports/                 the interfaces + fakes (fake_*.go)
│   │     intentparser.go  audioencoder.go  similarity.go  recommendation.go
│   │     library.go  metadatastore.go
│   ├── app/                   composition root. Container, wiring, lifecycle. doc.go (diagram).
│   ├── config/                load/validate TOML → immutable Config. no I/O elsewhere.
│   ├── audio/
│   │     decode/              mp3, flac, wav, ogg (pure Go); ffmpeg sidecar adapter
│   │     dsp/                 resample; (optional) pure-Go mel front-end + librosa-parity tests
│   │     onnxencoder/         AudioEncoder impl: ORT session, slice loop, aggregator
│   │     aggregate/           mean pooling + tfidf strategies over slice vectors
│   ├── intent/
│   │     llama/               local IntentParser: llama-server subprocess + GBNF + prompt + lifecycle
│   │     rules/               regex fallback IntentParser (year ranges, "no vocals", counts, …)
│   │     schema/              MusicIntent JSON schema, GBNF generator, validator, defaults/clamps
│   │     modelmgr/            first-launch model catalog, download+resume+sha256, "use my own GGUF"
│   ├── similarity/
│   │     brute/               exact cosine (v1, also the correctness oracle)
│   │     hnsw/                approximate index (v2)
│   │     store/               persistence (mmap float32 + id map, or SQLite BLOB)
│   ├── reco/
│   │     deejai/              deejai-walk strategy (ported)
│   │     radius/  journey/    alt strategies
│   │     constraints/         metadata predicates (year, genre, artist, explicit, bpm…)
│   ├── library/               fs scan, fsnotify watcher, dhowden/tag reader
│   ├── store/                 SQLite (metadata, playlists, settings)
│   └── bridge/                Wails-bound structs. Thin. Maps use-cases ↔ frontend. No logic.
├── frontend/                  React + Vite + TS
├── models/                    manifest.json (catalog of downloadable GGUF/ONNX) — NOT the blobs
├── python/                    export/parity tooling (not shipped): export_onnx.py, parity_check.py
├── build/                     Wails per-OS config, ORT + llama-server binary staging
└── docs/                      this document
```

---

## 4. Core domain types (`internal/core`)

```go
type TrackID string

type TrackRef struct { // all the LLM path is allowed to touch
    ID     TrackID
    Title  string
    Artist string
}

type Track struct {
    ID         TrackID
    Path       string
    Title      string
    Artist     string
    Album      string
    Year       int
    Genres     []string
    DurationMS int
    BPM        float64 // 0 = unknown
    Explicit   bool
    AddedAt    time.Time
}

type Embedding struct {
    ModelID string    // encoder identity; mismatch → stale, must re-encode
    Vec     []float32 // L2-normalized
}

type SliceEmbeddings struct { // persisted so aggregation is recomputable without re-decoding audio
    ModelID string
    HopSec  float64
    Slices  [][]float32
}

// MusicIntent — the ONLY thing the LLM produces. See §6.
type MusicIntent struct { /* ... */ }

type StepReason struct {
    TrackID TrackID
    Kind    string // "nearest" | "noise-jump" | "constraint-skip" | "dedup-skip"
    Detail  string
}

type Playlist struct {
    Tracks    []TrackID
    Strategy  string
    Seed      int64 // RNG seed → reproducible
    Rationale []StepReason
    Intent    MusicIntent
}
```

---

## 5. The four ports (`internal/ports`)

Every interface here has (1) a production impl and (2) an in-memory fake. No
implementation package imports another; they meet only in `internal/app`.

```go
// ── IntentParser ────────────────────────────────────────────────────────────
// natural language → MusicIntent. No catalog access. No track output.
// Local backends only: "llama" (subprocess) or "rules" (regex fallback).
type IntentParser interface {
    Parse(ctx context.Context, in IntentInput) (core.MusicIntent, error)
    Info() ParserInfo
}

type IntentInput struct {
    Prompt       string          // raw user text
    NowPlaying   *core.TrackRef  // resolves "like this"
    RecentTracks []core.TrackRef // resolves "keep it going"
    Locale       string
}

type ParserInfo struct {
    Name    string
    Backend string // "llama" | "rules"
    Ready   bool   // false until a model is downloaded & loaded (llama); UI badge
}

// ── AudioEncoder ───────────────────────────────────────────────────────────
// one audio file → one embedding. Impl owns decode→resample→slice→ONNX→aggregate.
// Internally a SliceEncoder (ONNX) + an Aggregator; slice vectors are persisted
// so the aggregation strategy can change without re-decoding.
type AudioEncoder interface {
    Encode(ctx context.Context, src AudioSource) (core.Embedding, core.SliceEmbeddings, error)
    Spec() EncoderSpec
}

type AudioSource struct {
    Path string // file path; impl decodes
    // or: Reader io.ReadSeeker + MIME, for non-file sources
}

type EncoderSpec struct {
    ModelID     string  // "deejai-mp3tovec-2023"
    Dim         int     // 100
    SampleRate  int     // 22050
    SliceSec    float64 // ~5
    Aggregation string  // "mean" | "tfidf"
}

// ── SimilarityEngine ───────────────────────────────────────────────────────
// persistent vector index over the local library. pure math + storage.
// knows nothing about intents or the LLM.
type SimilarityEngine interface {
    Upsert(ctx context.Context, recs ...EmbeddedTrack) error
    Delete(ctx context.Context, ids ...core.TrackID) error
    Search(ctx context.Context, q SearchQuery) ([]Match, error)
    Vector(ctx context.Context, id core.TrackID) (core.Embedding, bool, error)
    Stats() IndexStats
}

type EmbeddedTrack struct {
    ID  core.TrackID
    Vec []float32
}

type SearchQuery struct {
    Vector   []float32                    // query point (may be a synthesized centroid)
    K        int
    Exclude  map[core.TrackID]struct{}
    Filter   func(core.TrackID) bool      // cheap metadata predicate; nil = all
    MinScore float32
}

type Match struct {
    ID    core.TrackID
    Score float32 // cosine, 1.0 == identical
}

// ── RecommendationEngine ───────────────────────────────────────────────────
// the ONLY component that turns a MusicIntent into an ordered playlist.
// deterministic given (intent, index, catalog, seed). uses SimilarityEngine +
// MetadataStore. contains no AI calls.
type RecommendationEngine interface {
    Build(ctx context.Context, intent core.MusicIntent) (core.Playlist, error)
    Strategies() []string // "deejai-walk" | "radius" | "journey"
}
```

Supporting ports (same file conventions, each with a fake): `Library`
(scan/watch), `MetadataStore` (CRUD + query for constraint filtering).

---

## 6. MusicIntent schema

The contract between the LLM and the deterministic engine. `llama` output is
constrained to this shape by a **GBNF grammar** generated from the schema. The
parser then runs a validator that clamps ranges and fills defaults — the engine
never trusts raw model output.

```jsonc
{
  "version": 1,
  "seeds": {
    "track_refs": ["current"],       // "current" | "recent" | explicit IDs the UI supplied
    "text": "dreamy 90s shoegaze"    // free-text mood; used only if the encoder has a text path, else dropped
  },
  "count": 25,                        // clamp 1..500
  "strategy": "deejai-walk",          // "deejai-walk" | "radius" | "journey"
  "trajectory": {
    "mode": "similar",               // "similar" | "journey" | "wander"
    "from_ref": null, "to_ref": null, // journey endpoints
    "waypoints": [],
    "novelty": 0.25,                  // 0..1  → Deej-AI "drunk"/noise
    "coherence": 0.7,                 // 0..1  → inverse of ε dedup radius
    "lookback": 3                     // running-centroid window
  },
  "constraints": {
    "year_min": 1990, "year_max": 1999,
    "genres_include": [], "genres_exclude": [],
    "artists_exclude": [], "artist_max_repeat": 2,
    "instrumental": true,             // filter by vocal-presence tag/heuristic
    "explicit_allowed": false,
    "duration_sec_min": null, "duration_sec_max": null,
    "bpm_min": null, "bpm_max": null
  },
  "ordering": "flow",                 // "flow" | "similarity" | "shuffle"
  "notes_for_user": "Upbeat, instrumental, 90s-leaning."  // shown in UI; NOT used for selection
}
```

Design notes:

- Every field has a safe default; a bare `{}` is a valid intent
  ("more like what's playing").
- No field can name or rank a track for output. `track_refs` only *seed* the
  vector math.
- `notes_for_user` is the one place model prose surfaces — clearly labelled,
  never fed back into selection.

---

## 7. Component notes

### AudioEncoder (`internal/audio/onnxencoder`)

- ONNX Runtime via `github.com/yalue/onnxruntime_go` (wraps the ORT C shared
  lib — ship it per platform in `build/`).
- Flow: decode → resample to 22050 mono → window into 5 s slices (hop matched to
  the exported graph) → feed float32 PCM to the ONNX session → `[nSlices, 100]`
  → L2-normalize each slice → aggregate.
- **Put STFT + mel + log-norm inside the ONNX graph** (opset 17 `STFT`, mel as a
  constant `MatMul`). Go then only needs decode + resample, and parity is
  defined against *the graph you ship*, not librosa.
- Aggregator strategies (over persisted slice vectors, switchable without
  re-decoding):
  - `mean` — mean of L2-normalized slice vectors, renormalized. Default. No
    corpus dependency.
  - `tfidf` — Deej-AI's scheme: down-weight slices whose "sound" recurs across
    many library tracks (silence, generic beats), up-weight distinctive ones.
    Needs a library-wide pass; recompute incrementally on library change.
    Milestone 5+.
- `ModelID` is embedded in every `Embedding`; on load, a mismatch marks the
  track stale and schedules a background re-encode.

### SimilarityEngine (`internal/similarity`)

- v1 `brute`: exact cosine over a contiguous `[]float32` matrix. Simple, and
  stays as the correctness oracle for the HNSW tests. Fine to tens of thousands
  of tracks.
- v2 `hnsw`: pure-Go approximate index; falls back to `brute` below a size
  threshold.
- Persistence: single mmap'd `vectors.f32` + `ids.idx` sidecar, or a SQLite BLOB
  column. Round-trip tested.
- The `Filter` predicate lets the engine push cheap metadata filters
  (year/genre) into the scan instead of over-fetching then discarding.

### RecommendationEngine (`internal/reco/deejai`)

Ported walk:

1. Resolve seeds → seed vector(s). A `text` seed uses the encoder's text
   embedding path if the model has one, else it is dropped with a warning.
2. Maintain a running centroid of the last `lookback` picks (weighted,
   recent-heavy).
3. `Search` kNN around the centroid, excluding already-picked and
   `artists_exclude`, applying the constraint `Filter`.
4. Inject `novelty` noise: with probability ∝ novelty, jump to a lower-ranked
   candidate.
5. ε-dedup (`coherence`): skip candidates within ε cosine of an existing pick.
6. Enforce `artist_max_repeat`, `explicit_allowed`, duration/bpm/year via the
   `constraints` package.
7. Repeat to `count`. Record a `StepReason` per pick. Seeded RNG ⇒ reproducible.

`journey` mode interpolates the centroid along seed → waypoints → target, N
picks per segment (this is Deej-AI's `Join_the_dots`).

### IntentParser — `llama` (`internal/intent/llama`)

- Spawn the bundled `llama-server` (llama.cpp, MIT) as a child process on a
  random loopback port; talk `/v1/chat/completions`. No cgo.
- Constrain output with a **GBNF grammar** generated from the MusicIntent schema
  ⇒ structurally valid JSON always. The validator then clamps ranges and fills
  defaults.
- System prompt: role = "translate the request into a MusicIntent; you know no
  songs; never invent track names; leave unknown fields null." 4–6 few-shot
  examples.
- Lifecycle owned by `app.Container`: **not started until a model is present**;
  lazy start on first parse, health-check, restart on crash, kill on `Close`.
- If no model is configured, `Info().Ready == false` and `app` routes parsing to
  `rules`.

### IntentParser — `rules` (`internal/intent/rules`)

- Pure Go, always available, never fails. Handles the common cases: `"90s"` /
  `"from the 80s"` → year range; `"no vocals"` / `"instrumental"` →
  `instrumental:true`; `"more/less upbeat"`, `"chill"`, `"energetic"` → nudge
  `novelty` / `ordering`; `"25 songs"` → `count`; `"like this"` → seed =
  `NowPlaying`.
- Unknown phrasing → `{}` (= "more like what's playing"). Also the deterministic
  baseline used in tests.

---

## 8. First-launch model setup

`internal/intent/modelmgr`.

- **Bundled vs downloaded.** The `llama-server` binary ships in app resources
  (per OS/arch, CPU build, MIT). The **GGUF weights do not ship** — the user
  obtains them on first run, directly from the source. Keeps the installer small
  and means the app never redistributes model weights.
- **Catalog** (`models/manifest.json`, updatable without an app release):

  ```jsonc
  [
    { "id": "llama-3.2-3b-instruct-q4km", "label": "Llama 3.2 3B Instruct (Q4_K_M)",
      "params": "3B", "size_bytes": 2019377152, "ram_estimate_gb": 4,
      "url": "https://huggingface.co/.../resolve/main/Llama-3.2-3B-Instruct-Q4_K_M.gguf",
      "sha256": "…", "license_url": "https://…/LICENSE", "recommended": true },
    { "id": "qwen2.5-3b-instruct-q4km", "label": "Qwen2.5 3B Instruct (Q4_K_M)", "...": "..." }
  ]
  ```

- **Flow on first run:**
  1. App opens fully usable in `rules` mode; a non-blocking banner: "Download an
     AI model for smarter prompts."
  2. Setup screen lists catalog entries with size + RAM estimate + a link to
     each model's license.
  3. User picks one → **must accept that model's license** (shown inline) →
     download starts. The app fetches from the model's own source; it is not a
     redistributor.
  4. Download manager: streamed to the OS data dir, HTTP range resume,
     pause/cancel, progress %, sha256 verify on completion. On success: write
     `{model_id, path}` to config, start `llama-server`, flip `IntentParser` to
     `llama`.
  5. **"I already have a GGUF"** → file picker → quick load probe → store the
     path.
  6. Failure / offline / cancel → stay in `rules` mode, "Retry" button, no
     crash.
- Re-openable any time from **Settings → AI model** (switch model, re-download,
  point at a file, delete to reclaim disk).
- Optional GPU `llama-server` builds (Metal/CUDA/Vulkan) are a separate optional
  binary download from the same screen; CPU is the default.

---

## 9. Porting Deej-AI to ONNX + Go

One-time tooling in `python/` (not shipped).

1. **Pin the source of truth.** Read `notebooks/Get_spectrograms.ipynb` +
   `train/train_mp3tovec.py` + `train/calc_mp3tovecs.py` and record exact
   params: sample rate (22050), `n_fft`, `hop_length`, `n_mels`, slice length in
   frames, spectrogram normalization (per-slice max), output dim (100), output
   L2-norm. **Do not guess — copy them.**
2. **Obtain weights.** `huggingface.co/teticio/audio-encoder` (PyTorch). Verify
   the model-card license and the training-data terms; record both in `NOTICE`.
3. **Export ONNX.** `python/export_onnx.py`: load the PyTorch encoder, `eval()`,
   `torch.onnx.export(..., opset_version=17, dynamic_axes={"pcm": {0: "batch"}})`.
4. **Prepend the front-end.** Build a torch/torchaudio module: framing → `STFT`
   → mel filterbank → `log`/normalize, matching step 1 as closely as torchaudio
   allows. Export it, then `onnx.compose.merge_models(frontend, encoder)` → one
   `encoder.onnx` taking float32 PCM, emitting `[batch, 100]`.
5. **Regenerate reference vectors from the shipped graph.** Run ~50 short
   royalty-free clips through the *merged ONNX* (onnxruntime-python) and save
   `{clip → sliceVecs, trackVec}` as test fixtures. These, not Deej-AI's
   originals, are the parity target (the front-end is not bit-identical to
   librosa, and that's fine — self-consistency is what matters).
6. **Go parity harness.** `internal/audio/onnxencoder` runs the same clips; CI
   asserts mean per-slice cosine ≥ 0.999 vs fixtures and identical slice counts.
   Guards against decode/resample drift.
7. **Port the aggregator.** `mean` first. Then `tfidf`: port `calc_tfidf.py` /
   the `MP3ToVec` weighting; for a personal library compute IDF globally once,
   recompute incrementally. Document the small-library caveat (IDF is noisy
   under a few hundred tracks) and default to `mean`.
8. **Port the walk.** Translate `Deej-A.I.py`'s playlist loop and
   `Join_the_dots.py` into `internal/reco/deejai` with the parameter mapping in
   §6 (`novelty` ← drunk, `coherence` ← ε, `lookback` ← keep-on). Golden-playlist
   tests with a fixed seed and a fixed 200-track fixture library.
9. **Text seed (optional).** If you also want `seeds.text`, you need Deej-AI's
   Track2Vec text side (Word2Vec over track names) — larger scope; defer, and
   have the parser drop `text` with a UI note until then.

---

## 10. State & composition root

```go
// internal/app
type Container struct {
    cfg    config.Config   // immutable snapshot
    log    *slog.Logger
    db     *sql.DB
    parser ports.IntentParser
    enc    ports.AudioEncoder
    sim    ports.SimilarityEngine
    reco   ports.RecommendationEngine
    lib    ports.Library
    meta   ports.MetadataStore
    llama  *llamaproc.Handle // lifecycle-managed subprocess; nil until a model exists
}

func New(ctx context.Context, cfg config.Config, log *slog.Logger) (*Container, error) { /* wire everything */ }
func (c *Container) Close() error { /* stop watcher, kill llama-server, close db, flush index */ }

// SetModel downloads if needed, starts llama-server, and hot-swaps `parser`
// under a mutex. Called from Settings.
func (c *Container) SetModel(ctx context.Context, idOrPath string) error
```

- Constructed once in `main`. Handed to `bridge` structs. Nothing reaches for a
  global.
- `Container.parser` is chosen at startup: `llama` if
  `cfg.AI.ModelPath != "" && fileExists`, else `rules`.
- Config is loaded once, validated, then read-only. Changing settings builds a
  new sub-component and swaps the field under a mutex — no live mutation of
  config structs.
- Logging: `*slog.Logger` passed down; child loggers via `.With("component", …)`.
- Concurrency: library scan / encode runs on a bounded worker pool owned by
  `Container`; `context` cancellation propagates on `Close`.

Config:

```toml
[library]
roots = ["/home/user/Music"]

[ai]
model_id   = ""    # set after first-run download; blank → rules mode
model_path = ""     # absolute path to the GGUF (managed or user-supplied)
n_ctx      = 4096
n_threads  = 0      # 0 = auto
gpu_layers = 0      # >0 if a GPU llama-server build is installed

[reco]
default_strategy = "deejai-walk"
```

---

## 11. Build, models, packaging

- **ONNX Runtime:** bundle the platform shared lib in the Wails app resources;
  `onnxencoder` loads it by path from the resource dir. CPU EP default;
  CoreML/DirectML/CUDA EPs optional later.
- **llama.cpp:** bundle the CPU `llama-server` per OS/arch in resources (MIT).
  GPU builds (Metal/CUDA/Vulkan) are an optional download, not shipped by
  default.
- **Model blobs are not in the repo or the installer.** `models/manifest.json`
  lists `{id, label, url, sha256, size, license_url}` for the encoder ONNX and
  the GGUF catalog. Fetched on first run into the OS data dir, checksum-verified,
  with resume. The user can also point at a local file.
- **Audio codecs:** pure-Go decoders for mp3/flac/wav/ogg; detect a
  system/bundled `ffmpeg` for m4a/aac/alac/opus. If neither is present, those
  files are listed as "unsupported" rather than failing the scan.
- **Wails packaging:** per-OS `wails.json` targets; code-sign macOS (+ document
  the Gatekeeper path) and Windows.
- **`NOTICE`:** lists bundled binaries' licenses (ORT: MIT; llama.cpp: MIT).
  Model weights are covered by whatever license the user accepted at download
  time; the app records which model id/license was accepted.
- **CI:** `golangci-lint`, `go test ./...`, the parity harness (committed
  fixtures + a cached ORT lib), `wails build` matrix for the three OSes,
  frontend `tsc` / `vitest`.

---

## 12. Testing strategy

| Layer | Approach |
|---|---|
| `core` | Pure table tests. Intent validator: clamping, defaults, malformed input. |
| `IntentParser` | **Contract suite** run against `fake`, `rules`, and (integration tag) real `llama`. Golden files: `prompt → MusicIntent`, exact match on structured fields, fuzzy on free-text. GBNF guarantees structural validity, so tests focus on semantics ("no vocals" ⇒ `instrumental:true`, "from the 90s" ⇒ year range). |
| `AudioEncoder` | Parity vs committed ONNX-reference fixtures (cosine ≥ 0.999). Determinism (same file twice ⇒ identical). Corrupt/truncated file ⇒ typed error, no panic. |
| `SimilarityEngine` | Property test: HNSW recall vs `brute` oracle ≥ threshold on random vectors. Persistence round-trip. Upsert/Delete consistency. |
| `RecommendationEngine` | Fixed 200-track fixture library + fixed seed ⇒ golden playlist. Constraint assertions: no excluded artist, year range honored, `artist_max_repeat` respected, count met or shortfall documented. Empty library ⇒ empty playlist, no error. `novelty=0` ⇒ pure nearest-neighbour; `novelty=1` ⇒ high spread. |
| `modelmgr` | Download resume, sha256 mismatch → reject, cancel mid-stream → clean state, "use my own GGUF" validation. Served by a local test HTTP server. |
| `bridge` | Mapping tests only (use-case result ↔ frontend DTO); logic lives below it. |
| `frontend` | Vitest for intent-form → request mapping and playlist rendering; Wails calls mocked. |

Every port has an in-memory fake so any layer is testable in isolation with no
ONNX / llama / SQLite.

---

## 13. Key risks & decisions

| # | Risk / decision | Notes |
|---|---|---|
| 1 | **Deej-AI is GPL-3.0.** | Copying its code or a close port of its algorithm makes the derivative GPL-3.0 — that would cover the whole app. Options: (a) accept GPL-3.0 for Playlist AI; (b) **clean-room reimplement** the walk + aggregation from the public README description without copying source; (c) contact the author (`teticio@gmail.com`) about a license grant. Also check the **pretrained weights** license (HF model card) and the **Spotify-derived training data** terms separately. Decide before milestone 5. |
| 2 | Pretrained encoder weights license / provenance | `teticio/audio-encoder` on HF — confirm redistribution is allowed, or fetch-at-runtime from the original source with attribution in `NOTICE`. |
| 3 | Spectrogram parity | librosa ≠ torchaudio ≠ pure-Go exactly (mel norm, padding, power vs amplitude). Mitigation: define parity against the ONNX graph you actually ship (§9 step 5), not Deej-AI's originals. |
| 4 | ORT + `llama-server` binary distribution | Adds tens of MB per platform and a signing/notarization surface. CPU-only defaults; GPU variants as optional downloads. |
| 5 | m4a/aac decoding in pure Go | Weak. Plan: ffmpeg sidecar for those formats; degrade gracefully if absent. |
| 6 | 100-d Spotify-2023 vector space is out-of-distribution for obscure local music | Inherent to using the pretrained encoder; acceptable for v1. Retraining is out of scope. |
| 7 | TF-IDF aggregation needs a corpus | Noisy on small libraries. Default `mean`; enable `tfidf` past a size threshold. |
| 8 | 3–4B model intent-parsing quality | GBNF forces valid JSON, not correct semantics. Invest in a golden-file eval set. `rules` mode is the floor, not a place users should get stuck. |
| 9 | First-run model download UX | Large file over possibly flaky connections — resume + verify + clear failure states. The app must be genuinely useful in `rules` mode so the download is never a hard gate. |

---

## 14. Milestones

1. **Skeleton & boundaries** — layout, `core` types, `ports` + fakes,
   `Container`, config loader, Wails hello-world, CI green (lint / test /
   cross-compile).
2. **Library & metadata** — fs scan, `dhowden/tag`, SQLite store, `fsnotify`
   watcher, React library view.
3. **Audio encoder** — `python/export_onnx.py` + merged front-end graph,
   reference fixtures, Go ORT wiring, decode/resample, parity harness,
   batch-encode library → `EmbeddingStore`, "find similar to this track" in UI.
4. **Similarity** — `brute` impl + persistence; then `hnsw` behind the same port
   with recall tests.
5. **RecommendationEngine** — `deejai-walk` port, `constraints` package,
   novelty/coherence/lookback, `.m3u` export, golden-playlist tests. Then
   `tfidf` aggregator + `journey` mode.
6. **IntentParser** — `rules` parser first (unblocks end-to-end playlists from
   prompts). Then `schema` + GBNF generator + validator; `llama` subprocess +
   lifecycle; `modelmgr` first-launch flow (catalog, download/resume/verify,
   "use my own GGUF", Settings re-entry); UI prompt box + backend badge.
7. **Polish & ship** — Wails packaging per OS, signing/notarization, first-run
   wizard, optional GPU `llama-server` download, licensing / `NOTICE`
   finalized, user + architecture docs.

---

[Wails v2]: https://wails.io
[`teticio/Deej-AI`]: https://github.com/teticio/Deej-AI
