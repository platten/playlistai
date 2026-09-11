// Package app is the composition root. It is the only package that constructs
// concrete implementations and wires them to ports. New builds a *Container;
// the caller owns its lifetime and no application dependency is global.
//
// Orchestration is compiled Go. Optional llama.cpp parses intent; the native
// audio worker analyzes previews with downloaded model assets. Evidence-enabled
// generation may consult cached external providers; Python is offline-only.
//
//	FIRST RUN (user initiated)
//	  catalog archive ───────────────► int8 vectors + SQLite metadata
//	  optional metadata .zst ────────► verify + decompress + activate local genre index
//	  optional llama.cpp + GGUF ─────► GPU probe + VRAM-filtered model choices
//	  optional analysis bundle ─────► verified native worker/model activation
//
//	GENERATION (network evidence depends on the selected policy)
//	  prompt + session ─► IntentParser ─► versioned MusicIntent ─► Resolver
//	                       llama/rules      evidence + constraints     │
//	                                                            typed entities
//	  explicit feedback ─► FeedbackStore ─► reproducible TasteProfile │
//	                                                               ▼
//	  control overrides ───────────────────────────────► RecommendationEngine
//	                                                     │
//	       ┌─────────────────────────────────────────────┴──────────────┐
//	       │ retrieve independently: audio · co-occurrence · taste      │
//	       │ clusters · bounded exploration · optional semantic sidecar │
//	       └────► semantic/audio checks ─► hard eligibility ─► rank ──┤
//	                    exclusions +        component scores   select  │
//	                    recording dedup                              sequence
//	                                                                  │
//	                                                                  ▼
//	       history ◄── playlist + per-pick evidence + structured status
//	                   + catalog/algorithm/intent/profile/RNG versions
//	                   └► displayed-result acknowledgment ─► exposure
//
//	OPTIONAL NETWORK ACTIONS
//	  playlist ─► Deezer preview
//	           └► MusicBrainz enrichment ─► Soundiiz handoff
//	           └───────────────────────────► local CSV export
//
// Catalog loading publishes one complete immutable RuntimeSnapshot. Long-lived
// readers acquire OperationContext leases; Close cancels work and waits before
// releasing mapped vectors and stores. Model revisions prevent a late startup
// from undoing a newer selection. Generated-but-unseen playlists are not exposure.
//
// Invariants: inferred anchors are not user instructions; hard eligibility
// precedes selection; unknown evidence is not a verified match. Reproducibility
// records the versioned catalog/intent/profile/evidence inputs and lossless RNG
// seed. No preview audio is retained after analysis.
package app
