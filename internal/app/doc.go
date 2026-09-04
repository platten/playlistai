// Package app is the composition root. It is the only package that constructs
// concrete implementations and wires them to the ports. Nothing here is a
// global: New builds a *Container and the caller owns its lifetime.
//
// ┌──────────── Playlist AI — data flow (catalog-based, no local audio) ─────────────┐
// │                                                                                 │
// │  FIRST LAUNCH (one-time, user-initiated, with progress bars):                    │
// │    • llama GGUF  ───► OS data dir     • catalog (int8 vecs + SQLite) ───► data dir│
// │                                                                                 │
// │  ┌──────────── local, offline: the recommendation core ────────────┐             │
// │  user prompt        ┌───────────────┐   MusicIntent   ┌──────────────────────┐   │
// │  "upbeat like  ───► │ IntentParser  │ ──── JSON ────► │ RecommendationEngine │   │
// │   Justice, 20"      │ llama / rules │  (GBNF-          │  deejai walk:         │  │
// │                     └───────────────┘   constrained)  │   Σ last N vecs →      │ │
// │   UI sliders: creativity / noise / lookback / count ──►│   blended cosine kNN → │ │
// │   (override intent, re-run without re-parsing)         │   + Gaussian noise →   │ │
// │                        ┌───────────────┐  vectors      │   dedup (artist/id)    │ │
// │                        │  Catalog      │◄──────────────┤                        │ │
// │                        │  int8 vecs +  │  Resolve()    │  SimilarityEngine      │ │
// │                        │  SQLite meta  │  token match   │  brute force, 2×100   │ │
// │                        └───────────────┘               └──────────┬───────────┘  │
// │  └───────────────────────────────────────────────────────────────┼────────────┘  │
// │                                                                  ▼               │
// │  optional, online, user-initiated (progress-tracked):   ┌──────────────┐          │
// │   ┌──────────────┐  artist+title   ┌──────────────┐     │  Playlist    │  ► play  │
// │   │ Enricher     │◄────────────────┤ (per track)  │◄────┤ Artist-Title │──► Preview│
// │   │ MusicBrainz  │─► ISRC, album,  └──────────────┘     │ + Spotify id │  Deezer→ │
// │   │ 1 req/s,cache│   year, artists         │            └──────┬───────┘  Spotify │
// │   └──────────────┘                         ▼                   │           CDN    │
// │                                   ┌──────────────┐   ┌──────────────┐             │
// │                                   │ Exporter     │   │ Exporter     │             │
// │                                   │ Soundiiz API │   │ CSV / TXT    │─► user ─►   │
// │                                   │ (opt, token) │   │ (download)   │  Soundiiz ─► Qobuz
// │                                   └──────────────┘   └──────────────┘             │
// │                                                                                 │
// │  INVARIANT: prompt → playlist is 100% local. Enrich / export / preview are        │
// │  optional and online. The LLM only does text → MusicIntent, never track choice.  │
// └─────────────────────────────────────────────────────────────────────────────────┘
package app
