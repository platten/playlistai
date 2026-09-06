# Recommendation Milestones

## Milestone 1 — Correctness

Implemented against baseline `4c100e7`. Recommendation walks now keep reference
tracks separate from required output tracks, interpret `count` as the total
playlist length, distribute journey intermediates across segments, and use the
same normalized artist/title recording identity in similar and journey modes.
Hard artist and seed-artist exclusions are never bypassed. Exhausted candidate
sets return a partial playlist with an `eligible_tracks_exhausted` notice;
required-track exclusion conflicts and counts below the required waypoint count
return explicit errors.

Intent and bridge request version 2 add explicit required/reference fields.
Version 1 `seeds` and `seedIds` migrate to both roles, preserving the historical
behavior of saved requests. Upstream journey golden files remain checked in as
a documented parity baseline but are skipped because their count means
intermediates per segment.

Next dependencies: canonical recording IDs from richer catalog metadata,
clearer required-track authoring controls, and product decisions about placing
required tracks that are not journey waypoints. Semantic models and
personalization remain out of scope.

## Milestone 2 — Intent Preservation

The version 3 intent contract carries typed positive/negative artist and track
references, distinct required tracks, semantic preferences, explicit hard
constraints, journey and energy intent, source evidence, unsupported strict
requirements, and independent generation controls. The bridge now rebuilds
from the complete resolved intent plus explicit control overrides, so slider
changes cannot discard parser meaning. History loading migrates v1/v2 request
and intent JSON into v3; saved v3 records replay without re-parsing prompts.

Current execution support is declared on every normalized intent. Positive
catalog references, required tracks, total count, exact artist exclusions, discovery
variation, and audio/co-occurrence weighting are supported. Transition
smoothness is limited to walk memory/interpolation; artist diversity is limited
to the separate no-back-to-back constraint. Style, mood, instrumentation,
vocals, free-text texture, and energy trajectories are preserved with evidence
but are not scored or advertised as enforced.

Next dependencies: catalog features or a local semantic scorer for preferences
and energy, canonical recording/artist identities, and a diversity-aware
selection objective, including general negative-reference scoring. These capabilities must update their declared status when
implemented.

## Milestone 3 — Reliable Resolution

The version 4 intent contract persists typed catalog resolution: selected
entities, confidence and match evidence, ranked alternatives, catalog version,
and weighted representative tracks. Artist and track namespaces resolve
separately. Exact accent-folded or Unicode-preserving matches and explicit
artist aliases precede prefix/token fallback; arbitrary trailing words are no
longer discarded. Ambiguous top matches remain unresolved until the user picks
an alternative, while high-confidence matches continue automatically.

Artist references use up to four deterministic medoids selected from a bounded
128-track embedding sample. Cluster membership supplies representative weights
to the similarity walk. Results and medoids are cached per catalog version;
clustering cost is bounded independently of an artist's full track count.
Existing catalogs retain Latin search behavior and use a slower metadata scan
for non-Latin queries; newly generated catalogs add Unicode search columns,
indexes, and optional artist aliases.

Next dependencies: stable canonical artist and recording IDs, source alias data
for production catalogs, and background/indexed migration of older catalog
files. Popularity and genre ranking remain unavailable and are not inferred.

## Milestone 4 — Generation Lifecycle

The version 5 intent contract represents every 64-bit RNG seed as a decimal
string across Go, JSON, generated TypeScript, and history; numeric v1-v4 seeds
still load with their original bit pattern. Each result records a reproducible
generation identity derived from the catalog and recommendation algorithm
versions, normalized resolved intent, explicit no-profile snapshot/version, and
seed. History now stores the complete result and migrates existing databases,
so new saved playlists and the result returned by `GenerateFromPrompt` open
without an immediate duplicate build.

Completed parses are reused only when prompt, parser/model identity, schema
version, and session context match. Frontend sequence guards and cancellable
Wails calls supersede stale previews and builds; the bridge independently
rejects superseded results. Cancellation reaches the recommendation walk and
periodically interrupts brute-force similarity and filter scans. Responses now
include complete/partial state, structured partial reasons, parser fallback
status, and per-stage timings; logs contain stage and result metadata but omit
prompt and taste text. Ranking and control weights are otherwise unchanged.

Next dependencies: real profile snapshot/version inputs if personalization is
introduced, persistent parse caching if startup latency warrants it, and a
frontend test harness for direct component-level ordering tests. The current
in-memory parse cache is process-local, and timing is intentionally coarse.
