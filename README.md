# Playlist AI

A cross-platform desktop app that builds music playlists by *listening* to your
local library. Go + [Wails v2](https://wails.io) backend, React (Vite + TS)
frontend.

- **Fully local.** Audio analysis, vector search, and natural-language
  understanding all run on your machine. The only network call the app ever
  makes is a one-time, user-initiated LLM download on first launch.
- **The LLM only parses your prompt** into a structured `MusicIntent`. It never
  picks or ranks tracks — that is deterministic Go over local audio embeddings.
- **Swappable backends** behind four interfaces: `IntentParser`, `AudioEncoder`,
  `SimilarityEngine`, `RecommendationEngine`.

The core recommendation technique is derived from
[`teticio/Deej-AI`](https://github.com/teticio/Deej-AI), ported to ONNX + Go.

## Documentation

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — full architecture: boundaries,
  ports, data-flow diagram, MusicIntent schema, Deej-AI porting plan, milestones.

## Status

Pre-implementation. The architecture document is the current deliverable.
