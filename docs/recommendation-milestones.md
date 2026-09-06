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

## Milestone 5 — Explicit Feedback and Local Taste Profiles

Added append-only, versioned feedback events for explicit like/dislike,
more/less-like, review acceptance/removal, and recommendation exposure. Events
carry track, request/session, interaction context, timestamps, and catalog,
intent, recommendation, and profile versions in local `taste.sqlite` storage.
Exposures remain separate evidence and generated or previewed tracks never
implicitly become likes or dislikes. Durable preferences and request-scoped
“not for this request” evidence are kept distinct.

The reproducible `taste-profile/v1` builder applies a 30-day half-life and
produces positive, negative, request-local, and up to four deterministic taste
cluster centroids in both audio and playlist-co-occurrence spaces. Cold-start
snapshots are stable. Generation identities now record the exact profile
snapshot, while ranking remains unchanged. A tested candidate-affinity API
gives explicit current-request references priority over request feedback and
durable history for the next ranking milestone.

The UI passes current playback/session context into parsing, offers explicit
feedback on playlist rows, records review inclusion changes, summarizes the
local profile, and can clear all local taste data. Saved-playlist reuse starts a
fresh request/session context without changing the saved result.

Next dependencies: integrate affinity and cluster coverage into candidate
ranking, define objective weights and evaluation fixtures, add profile-state
undo/conflict controls, and add component-level tests for feedback interactions.

## Milestone 6 — Multi-channel Retrieval and Personalized Ranking

Added the versioned `multichannel/v1` strategy with explicit retrieval,
eligibility, ranking, and sequencing boundaries. Exact searches run separately
for every resolved representative in audio and co-occurrence space and for each
relevant positive taste cluster. A bounded exploration channel samples only
candidates above a configured relevance floor. Candidate unioning retains
query/channel provenance and uses weighted reciprocal-rank fusion (RRF) because
native channel scores are not calibrated probabilities. Hard exclusions and
provisional artist/title recording deduplication run before ranking.

Ranking exposes audio-reference, co-occurrence-reference, listener-affinity,
negative-match, recent-exposure, and listener-novelty components. Novelty is
defined as distance from positive profile affinity; absent profile or embedding
features remain explicitly unavailable. Current-request evidence takes priority
over durable taste. New generations use a global, seven-day-decayed exposure
map in reproducible `taste-profile/v2` snapshots. Existing feedback and history
remain loadable; saved results retain their recorded algorithm/profile versions.

Channel budgets, candidate bounds, exploration floor/chance, and ranking
weights are configurable under `[recommendation]`. Set `strategy = "deejai"`
to retain the versioned `deejai/v4` walk as an evaluation baseline. Fixed RNG
seeds make retrieval exploration and sequencing reproducible, and every selected
track carries its actual retrieval sources and score-component evidence.

Next dependencies: offline relevance/diversity evaluation fixtures, calibrated
score fusion, canonical recording IDs, exposure policy controls, and catalog
features or a local model for the preserved semantic preferences. ANN and
semantic ranking remain intentionally out of scope.

## Milestone 7 — Diversity Selection and Playlist Sequencing

The `multichannel/v2` pipeline separates candidate selection and playlist
sequencing from retrieval and ranking. Eligibility still owns hard exclusions,
recent-track exclusion, and provisional recording deduplication; none are
converted into score penalties. Required tracks reserve output slots, while
ordered journey waypoints provide embedding-trajectory anchors without becoming
required output implicitly.

Selection uses maximal marginal relevance (MMR). Its relevance floor is
`max(selection_minimum_relevance, best_score - selection_relevance_window)`.
For candidates above that floor, relevance is scaled from the floor to the best
score and `lambda = 1 - (1 - mmr_minimum_lambda) * artist_diversity`. The MMR
score is `lambda * relevance - (1 - lambda) * diversity_penalty`; the penalty is
a configured weighted mean of maximum positive embedding cosine, artist share,
and album share. Audio and co-occurrence cosines use their intent weights.
Album terms are available only when catalog metadata explicitly marks the album
reliable; missing or unverified albums remain unknown.

Ordering greedily combines transition cosine, selected relevance, and
piecewise waypoint-embedding fit, then applies a configured number and window
of improving pair swaps. Required journey anchors remain fixed and ordered.
Soft artist spacing may relax with a structured notice; hard adjacency rules
never relax and can produce a partial result. The trajectory port reports its
embedding evidence, while acoustic energy remains preserved but unsupported.
Continuing-radio requests retain the original intent anchors, add bounded
recent-track retrieval, exclude recent recordings, retain taste-cluster input,
and include continuation context in reproducibility metadata.

Controlled fixtures cover relevance preservation, artist and album behavior,
waypoint placement, counts, duplicate protection, hard/soft spacing,
continuation, determinism, and transition quality versus `deejai/v4`.

Next dependencies: reliable album metadata in the shipped recommendation
catalog, canonical artist/recording/release IDs, offline tuning of MMR and
transition weights, and actual acoustic features before energy matching. Exact
search remains the correctness baseline; ANN and semantic ranking remain out of
scope.

## Milestone 8 — Grounded Semantic Matching

Added the optional semantic sidecar and the `multichannel/v3` pilot. The
schema-v2 sidecar carries canonical artist/recording identities, grounded
descriptive facets, separate original/release-edition dates, provenance, confidence,
missingness, source/model versions, and preview-segment coverage. Catalog,
schema, text-model revision, dimension, and returned track IDs are checked at
load/search time. Without a valid sidecar, the base application and all seeded
retrieval remain available.

An offline builder embeds supplied descriptions and a bounded query vocabulary
with compatible Sentence Transformers document/query encoders. The sidecar
stores normalized phrase, adjacent-pair, and term query vectors. Runtime Go
code selects an exact phrase or composes known vectors, then bounded exact
cosine search contributes an independent semantic channel. No Python or model
runtime is required by the desktop application. Positive and negative text
evidence have separate transparent ranking terms. Seedless semantic intent is
supported when the index returns real catalog tracks. Seeded requests retain
explicit fallback behavior. Existing schema-v1 sidecars remain feature-only;
regenerating them enables schema-v2 retrieval.

Semantic hard eligibility is limited to declared style/tag and vocal facets.
Unknown evidence fails a strict constraint; it is never converted to a weak
match. Other attributes remain preserved and unsupported. The checked catalog
has 956,917 remote preview URLs but no stable local audio corpus, so aligned
audio/text inference is intentionally not claimed. Pilot coverage and footprint
are generated from the exact input using `--report`; no bulk MusicBrainz calls
occur during playlist generation.

Next dependencies: curate and license a representative evidence set, measure
phrase-composition coverage and prompt/facet precision before increasing the
5,000-track pilot, and consider ANN only after full-catalog coverage warrants
it. A CLAP-style audio pilot also requires licensed local audio, deterministic
segment selection, and coverage auditing.

## Milestone 9 — Evaluation and System Tuning

Added the versioned `recoeval/v1` offline harness and `cmd/recoeval`. It scores
labeled intent/negation and resolver cases, candidate Recall@K, output NDCG@K,
hard violations, recording duplicates, artist concentration/diversity, catalog
coverage, recent-exposure repetition, adjacent-vector transition quality, and
parse/retrieval/ranking/sequencing/total latency. Reports retain per-case
generation inputs, intent/context fingerprints, optional semantic model
identity, and normal-approximation uncertainty intervals.

Recommendation cases use a chronological 60/20/20 train/development/test
split. Profiles exclude future events; parameters are chosen on development by
lexicographically minimizing hard violations and maximizing NDCG, frozen, then
evaluated once on held-out cases. Ablations cover audio-only,
co-occurrence-only, `deejai/v4` blended walk, multi-channel retrieval,
personalization, optional semantic matching, and diversity/sequencing. A
deterministically randomized blind A/B export keeps its identity key separate.

No real relevance judgments or listening outcomes are checked into the
repository, so Milestone 9 makes no musical-quality claim and does not retune
defaults. The synthetic fixture validates execution and leakage guards only.
Next dependencies are consented pseudonymous interaction exports, independent
listening judgments covering all documented cohorts, a compatible semantic
sidecar for its ablation, and enough development/test cases for stable
uncertainty estimates and evidence-backed defaults.

## Milestone 10 — Retrieval Performance and Local Models

Profile-guided work retained exact retrieval and parallelized large catalog
scans with deterministic shard-local top-K heaps, exact merging, and per-shard
cancellation. A 2026-09-06 rerun on the 956,917-track production catalog
measured exact K=64 search at 81.5–84.0 ms serial versus 9.81–10.05 ms parallel,
and full 20-track generation at 438.0–440.2 ms versus 111.3–125.3 ms.
Serial/parallel scores and order match, so
the optimization changes latency rather than recommendation quality. The exact
engine adds only 7.66 MB of derived norms; ANN was therefore not implemented.

Added `cmd/intenteval` and a versioned, human-labeled intent suite covering
references, negation, required tracks, semantic nuance, hard/unsupported
requirements, contextual feedback, non-Latin text, ambiguity, and evidence.
Reports pin the parser/schema, model size and SHA-256, runtime build, per-case
results, latency, and peak RSS. Current llama.cpp compatibility now disables
thinking output for structured parses and handles SSE completion without
waiting for connection close.

Across three runs per case, Qwen3.5 0.8B, Qwen2.5 3B, and Llama 3.2 3B all
failed the documented correctness gate; the strongest reached only 57.1%
field accuracy and exceeded the 15-second P95 target. No measured model was
promoted at that point, and rules parsing remains the no-model fallback. No LLM
reranker was attempted without a configured grounded sidecar and real held-out
listening judgments.

Implemented capabilities are exact parallel retrieval, reproducible production
benchmarks, artifact-aware local parser evaluation, and current-runtime chat
compatibility. Future experiments depend on broader independently labeled
intent data, consented temporal listening judgments, representative multi-host
profiles, and grounded descriptor coverage. ANN or an LLM reranker should be
reconsidered only when those measurements show a concrete need and benefit.

## Post-milestone Runtime and Onboarding Updates

The desktop recommendation runtime is now fully compiled Go. Semantic sidecar
schema v2 stores the bounded query vocabulary needed by its Unicode-aware Go
query composer; Python and Sentence Transformers remain offline dataset-builder
dependencies only. The complete test gate includes a `CGO_ENABLED=0` compile of
all packages below the Wails bridge to prevent an interpreter or native library
dependency from entering the core application.

Generate now remains visible in both parser modes. Catalog-only/rules mode
clearly requires a seed artist or track. A ready local LLM may infer a grounded,
non-required starting reference when none is explicit; if model parsing fails
and generation falls back to rules, the catalog seed requirement is restored.

The curated model catalog now contains pinned Q4_K_M artifacts for Qwen3.5 35B
A3B, Qwen3.5 9B, Mistral Small 3.1 24B, Gemma 3 12B, and Qwen3.5 4B in product
priority order. The first-run wizard asks its selected llama.cpp binary to
enumerate devices and free VRAM, retains 1 GiB for context/KV/compute, and shows
only models whose complete weights fit. With no usable llama.cpp GPU, it shows
the two smallest recommended models. Llama 3.2 3B and Qwen2.5 3B stay available
but non-recommended. These five artifacts are not yet covered by the existing
intent benchmark, so their ordering is not presented as a measured quality
result.
