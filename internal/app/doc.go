// Package app is the composition root. It is the only package that constructs
// concrete implementations and wires them to ports. New builds a *Container;
// the caller owns its lifetime and no application dependency is global.
//
// The desktop generation path is local and implemented in compiled Go, apart
// from the optional llama.cpp child process used only to parse natural language:
//
//	FIRST RUN (user initiated)
//	  catalog archive ───────────────► int8 vectors + SQLite metadata
//	  optional llama.cpp + GGUF ─────► GPU probe + VRAM-filtered model choices
//
//	LOCAL GENERATION
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
//	       └──────────► hard eligibility ─► transparent rank ─► MMR ───┤
//	                    exclusions +        component scores   select  │
//	                    recording dedup                              sequence
//	                                                                  │
//	                                                                  ▼
//	       history ◄── playlist + per-pick evidence + structured status
//	                   + catalog/algorithm/intent/profile/RNG versions
//
//	OPTIONAL NETWORK ACTIONS
//	  playlist ─► Deezer preview
//	           └► MusicBrainz enrichment ─► Soundiiz handoff
//	           └───────────────────────────► local CSV export
//
// Invariants: the LLM never selects tracks; hard constraints run before
// ranking; exposures are not likes; the same versioned inputs and lossless RNG
// seed reproduce a generation. Python is used only by offline dataset helpers,
// never by the desktop runtime.
package app
