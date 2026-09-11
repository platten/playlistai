# Recommendation Milestones

## Stop after a complete requested playlist (2026-09-11)

`multichannel/v20` stops metadata discovery and musical analysis once the
requested count can satisfy final selection and sequencing. Required tracks
reduce remaining slots; rejected candidates do not count. This supersedes the
metadata oversampling policy noted below without changing recommendation 2N
shortlist preparation, hard exclusions, journey checks, or evidence priorities.
Saved results remain readable; new generations carry the updated strategy version.

## Recommendation shortlist before analysis (2026-09-10)

Implemented `multichannel/v19`: recommendation-based builds prepare up to 2N
candidates before musical-fit checks, then stop when selection and sequencing
can fill the requested N tracks. Required tracks count once. Refills retain the
original intent/seed and accepted continuation tracks without repeating retrieval
after every accepted candidate. Request-local retrieval expansion supports larger
counts; provisional MMR ordering preserves diversity before early stopping.
Missing journey stages keep the search active even when count is met.
AcousticBrainz/CLAP priority, hard eligibility, journey order, cancellation,
bounded partial results and Deej-AI-only isolation
remain intact. Local analysis builds share this collector; direct metadata
discovery retains its separate oversampling policy.

All 11 new regression functions pass, including 20-to-10 selection, rejected-pool
refills, both analysis modes, required-waypoint counts, replay, partially/entirely
excluded retrieval pages and accepted-only continuation. The full Linux
`scripts/test.sh` gate passes: bindings, frontend typecheck/build, vet, pure-Go
core compile, race-enabled tests and lint (zero issues). `git diff --check` passes.
No new live-provider or listening-quality measurement is claimed.
Next dependency: measure rejection rates and latency on
representative real requests. See [behavior and compatibility details](recommendation-settings.md#recommendation-shortlist-and-analysis).

## Review follow-up — Discovery and replay (2026-09-08)

Implemented `multichannel/v13`: bounded overcomplete candidate selection,
stop-aware provider I/O, lazy catalog continuation, input-keyed discovery
replay, provider-specific pick evidence and exact taste-snapshot loading.
MusicBrainz/Discogs use four-lookup buffering windows; Discogs reserves detail
budget for subsequent full search pages. Cache expiration remains strict while
indexed cleanup runs at most once per minute on access. Existing read-only
catalogs get an in-memory artist-to-row index; new catalogs also get a SQL
artist/row index. No runtime dependency or model change was added.

The full repository gate passes. A local 956,917-track catalog microbenchmark
measured median exact artist lookup at 76.94 ms for the old scan and 0.503 ms
for batched indexed reads. This is not end-to-end generation or musical-quality
evidence. See [correctness and compatibility decisions](recommendation-correctness.md#discovery-ranking-and-replay-review-fixes-2026-09-08).
Next dependencies: held-out listening evaluation and live-provider latency
measurement for the larger eligible pool; oversampling can require more CLAP
checks, and the artist index adds startup work and memory.

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

## Post-milestone Storage and Selection Bounds

Generation writes one exposure row per recommended track, so exposure volume
grows with every playlist while explicit feedback grows only with user
interaction. Profile construction previously read the entire exposure history,
which made each generation cost more than the last: measured on this host, a
generation-time profile rebuild took 9.5 ms after 10 generations and 203.3 ms
after 1,000, with the saved snapshot growing in step.

Exposure reads are now windowed to five exposure half-lives and capped at the
same bound as the recent-exposure map they populate, a retention sweep runs at
open, and the map drops entries whose decayed weight can no longer move a
score. The same measurement is now flat at 28–30 ms from 100 through 1,000
generations. Explicit likes, dislikes, acceptance, and removals are
user-authored and are never windowed or pruned. Scoped profile reads no longer
match rows that merely recorded no request or session of their own.

Diversity selection folds each newly chosen track into running redundancy,
artist, and album terms instead of rescanning the whole context for every
candidate on every round, making selection linear rather than quadratic in
playlist length: 25.5 ms to 1.07 ms at 20 tracks and 320.9 ms to 4.9 ms at 80,
over a 512-candidate pool. An oracle test pins the incremental result to the
direct implementation's exact picks and score components.

Zero is again an accepted value for `semantic_weight` and
`semantic_negative_penalty`; both previously validated as zero and were then
silently restored to their defaults, so the components could not be disabled.

## Post-milestone Runtime and Onboarding Updates

The catalog recommendation runtime is now fully compiled Go. Semantic sidecar
schema v3 stores facet completeness and the bounded query vocabulary needed by its Unicode-aware Go
query composer; Python and Sentence Transformers remain offline dataset-builder
dependencies only. The complete test gate includes a `CGO_ENABLED=0` compile of
all packages below the Wails bridge to prevent an interpreter or native library
dependency from entering the core application.

Generate now remains visible in both parser modes. Catalog-only/rules mode
clearly requires a seed artist or track. A ready local LLM may infer a grounded,
non-required starting reference when none is explicit. If model parsing fails,
the rules fallback preserves the category request and reports its fallback; it
does not silently turn requested LLM mode into catalog-only artist lookup.

The curated model catalog now contains pinned Q4_K_M artifacts for Qwen3.5 35B
A3B, Qwen3.5 9B, Mistral Small 3.1 24B, Gemma 3 12B, and Qwen3.5 4B in product
priority order. The first-run wizard asks its selected llama.cpp binary to
enumerate devices and free VRAM, retains 1 GiB for context/KV/compute, and shows
only the largest model whose complete weights fit. With no usable llama.cpp GPU,
it shows the largest model from the bounded CPU recommendation list. Llama 3.2
3B and Qwen2.5 3B stay available
but non-recommended. These five artifacts are not yet covered by the existing
intent benchmark, so their ordering is not presented as a measured quality
result.

Hardware validation now covers every model's exact fit boundary and named
profiles for RTX 5070 Laptop (8 GiB), RTX 5070 desktop (12 GiB), RTX 3090
desktop (24 GiB), and RTX 5090 Laptop/desktop (24/32 GiB). NVIDIA has no RTX
3090 Laptop product, so the matrix does not invent one. The preferred tier
models are Qwen3.5 9B at 8/12/16 GiB and Qwen3.5 35B A3B at 24/32 GiB, subject
to the hard current-free-VRAM fit check. Settings exposes the tier labels for
all curated models. A separate policy benchmark reports model counts and
allocation cost without pretending to measure GPU inference. Intent evaluation
report v2 records the actual llama.cpp device inventory and run settings, and
`-device` can pin a multi-GPU benchmark to one accelerator.

## Milestone 11 — Recommendation Correctness

Intent v6 distinguishes essential musical criteria, soft preferences, hard
exclusions, explicit references, and model-inferred anchors. The rules and LLM
contracts now preserve category-led requests such as “electronic music”; model
anchors resolve to real entities but steer retrieval only after independent
musical-suitability validation. LLM truncation gets one bounded retry, parser
fallback reasons remain structured, and requested model mode no longer becomes
an artist lookup when fallback parsing occurs.

`multichannel/v4` now unions every channel before batch semantic scoring,
applies affirmative essential eligibility and strict exclusions before fixed-
scale ranking, reserves and orders grounded category-journey stages, and
returns `fulfilled`, `partial`, `unsupported`, or `needs_clarification` based on
evidence rather than count. Feature-only sidecars remain connected. Schema-v3
facets declare completeness so an unrelated known tag cannot prove an excluded
style absent.

A seven-track reviewed pilot input validates against all 956,917 catalog IDs;
six rows declare complete style evidence and one is intentionally incomplete.
No generated sidecar is shipped, so production semantic coverage is still
zero and unsupported category requests fail honestly. Next dependencies are a
licensed, independently reviewed full-catalog evidence source, a built and
versioned local semantic index, and held-out blind listening judgments. See
[`recommendation-correctness.md`](recommendation-correctness.md).

### Correctness review follow-up

All nine review findings have focused regressions. Rules and LLM validation
share category interpretation, keep explicit artist-name evidence separate
from style instructions, preserve category-plus-seed requests, and retain
narrow exclusion scope. Uncertain matching facets cannot prove an exclusion's
absence. Runtime and evaluation now share evidence/hierarchy rules and ordered
journey-stage accounting. `multichannel/v5` jointly sequences category stages,
required tracks, waypoints, and hard artist spacing; it returns partial or
clarification outcomes when they cannot all be satisfied. Parser versions
advance to v6 without changing intent-v6 history serialization. Full-catalog
semantic evidence and held-out listening judgments remain the next dependencies.

The category vocabulary now canonicalizes `electronica` to `electronic` and
`ambient electronica` to `ambient electronic`, while retaining explicit artist
context such as “music by Electronica.” This closes the rules-fallback path that
previously surfaced “no seeds are resolved” for “ambient electronica.”

## Milestone 12 — Description and Preview Evidence (implementation, gated)

Intent v7 and `multichannel/v6` add the original description, open-vocabulary
genre expansions, six bounded anchor attempts and candidate-wide preview
eligibility. All retrieval channels pass strict metadata and preview checks
before personalization/ranking. Required tracks and journey stages retain their
original criteria. Unsupported or unknown evidence cannot silently fill a
playlist; strict no-vocals remains unproved by previews.

The owner confirmed Deezer permission for analysis, permanent derived features
and distributed desktop users. Go now owns bounded preview fetches, MP3 decode,
48 kHz preprocessing, RoBERTa tokenization, Slaney log-mel features, SQLite
retention and an isolated CPU ONNX worker. Inference uses music CLAP in its own
aligned embedding space. The optional worker uses cgo/native ONNX Runtime;
the existing core cgo-free compile gate still passes. No Python desktop
requirement or persistent preview file was introduced.

The wizard/settings support checksummed, resumable model/runtime bundles with
parity, policy, platform and native health gates, plus separate analysis/history/
taste clearing. Generate keeps its composer visible, shows an editable request
summary and progressive checked tracks, and supports stop-and-keep. Generation
IDs protect progress/results against stale work. Full intent, string seed,
versions and evidence snapshot remain in history.

Executed on 2026-09-07: the full repository gate and Linux production desktop
build passed; rendered light/dark,
narrow, reduced-motion, keyboard/ARIA, stale, partial, wizard, download/error and
retry states passed. Real model export parity passed ten fixtures (maximum
embedding error `1.043081283569336e-7`, seven exact tokenizer cases, three Go
log-mel fixtures). An authorized, corroborated Four Tet preview passed
Go → ONNX → SQLite in 2,071 ms after worker health, fetching 479,827 bytes for
29.99 seconds. Repeat analysis fetched zero bytes. A separate cached health run
peaked at 1,127,388 KiB RSS on the WSL2 Linux amd64 host.

The implementation is **not a production music-quality milestone**. Public
bundle activation remains gated because development listening calibration and
held-out comparisons have not been performed. The new offline audio review
reporter validates recording/artist split isolation and three ablations, while
preserving unknown judgments. Clean-machine Windows/macOS/Linux installations,
combined LLM memory budgets and human screen-reader validation remain unexecuted.
No model/publication/release/default-LLM change is included. Architecture,
provenance, measurements and reproduction commands are in
[`recommendation-correctness.md`](recommendation-correctness.md).

### Python-free distribution follow-up

Ported analysis-bundle assembly to `cmd/audiopack` and removed the Python helper.
Removed the explicit Python installation from the cross-build Dockerfile; its
existing Node runtime parses the Zig checksum manifest. Python remains only in
offline dataset preparation and model-export/reference-validation tools.
The desktop and downloaded analysis worker have no Python runtime requirement.
The real Go packager and native CLAP audio/text health passed with Python
unavailable on the executable search path; worker memory mappings and Linux
binary dependency scans contained no Python runtime.

### Deezer request spacing follow-up

Added a shared process-wide two-second minimum between Deezer request starts.
Metadata, analysis downloads, playback downloads and redirects share the same
budget. Playback uses a bounded in-memory data URL to prevent browser requests
from bypassing the throttle; queued work is cancellable. Tests cover concurrent
dispatch, cancellation, failures, redirects, separate providers and cache hits.
The analysis deadline includes throttle waiting, so historical pre-throttle
timings do not represent current uncached performance. No release is included.

## Milestone 13 — Open music descriptions and useful partial results

Intent v8 separates genres, styles, moods, textures, vocal preferences and typed
artist/album/track references. The local-model grammar has bounded lists and one
repair attempt. Invalid optional proposals and invented positive references no
longer become listener requirements. Century dates use deterministic arithmetic;
classical periods refer to composition, and named destinations pin an actual
recording last. Production examples describe interpretation structure without
hardcoded prompt-to-track fits.

MusicBrainz metadata retrieval is bounded to 20 requests/30 seconds per generation,
uses its shared limiter, caches positive/negative responses, and permits stale
offline reuse. Genre relationships and aliases come from attributed public pages;
only subgenres imply broader membership. Recording identity ambiguity is retained.
Full prompts and taste profiles are never sent to metadata services. Cached exact
genres work in catalog-only mode; full interpretation needs the optional LLM.

New prompts may produce explicitly labeled best-available suggestions. Proven
category fit precedes personalization and diversity. Known mismatches and artist
exclusions remain filtered; unsupported strict requirements still abstain.
Earlier histories preserve verified-only behavior. Audio-checked tracks stay
distinct from suggestions, with generation IDs, provisional ordering and
stop-and-keep controls. Optional CLAP activation remains gated on the listening
and platform validation described in Milestone 12.

Executed on 2026-09-07: all twelve synthetic prompt contracts generate through
the parser/resolver/engine; genre alias/cycle/relationship, source grounding,
namespace, exclusion, temporal scope, destination, cache TTL/offline, metadata
identity and evidence-priority regressions pass. The complete repository gate
passes, including race tests, pure-Go core compile, lint, generated bindings,
TypeScript and production frontend build. Rendered screenshots pass light/dark,
narrow, reduced-motion, keyboard/ARIA, stale events, suggestions/checked tracks,
partial results and wizard/download/error/retry fixtures.

`scripts/build.sh` passes on WSL2, producing AppImage, DEB, RPM, Arch and a
cross-compiled Windows NSIS installer. The shared AppImage wrapper prevents
linuxdeploy plugin discovery from traversing `/mnt` PATH entries; its regression
test includes the reported ControlD path. macOS packaging and clean-machine
installation remain unexecuted. Distribution has no Python runtime dependency.
Unused helper/icon code and old production example music fits were removed.
No release, deployment, public model bundle or default-LLM change is included.

The real Qwen3.5-4B/catalog run generated playlists for all twelve prompts. Ten
passed every intent/output check in the combined run; two intermittent omitted
fields were fixed and passed targeted live rechecks. Latest results: eleven
20-track playlists and one 14-track exclusion result, with uncertainty preserved.
The actual Miles Davis destination check passes. See
[`data/music-prompts-v8-live.json`](data/music-prompts-v8-live.json) for hashes,
timings, initial failures and recheck identity. These are development prompt
checks, not musical-fit judgments or a single clean twelve-case final run.

### Genre artist grounding follow-up

Added MusicBrainz artist-tag search with 100-result pages, pagination to at least
100 unique artists when available, and explicit coverage/exhaustion reporting.
Saved seeds randomize artist and recording selection, including cache reuse.
Samples require MusicBrainz artist-MBID credits and corroborated local recording
identity; artist tags do not classify an artist's entire catalog. Snapshots retain
the artist pools, source queries and sampled identities.

Moved one-second MusicBrainz spacing to a shared HTTP transport so concurrent
clients and redirects cannot bypass it. Regression tests cover pagination,
seed variation/replay, cache reuse, exclusions, mismatched recording credits,
small pools, production interval clamping, concurrent dispatch and cancellation.

Live verification on 2026-09-07 fetched 100 dubstep artists from 1,417 available
MusicBrainz matches, sampled three artists and six corroborated catalog tracks,
and generated a 20-track playlist. The full repository gate passes. Genre-page
relationships were unavailable during this run; the artist API pool succeeded
independently. This check does not claim recording-level genre or listening fit.

## Settings log window — 2026-09-07

Added a Settings action opening a named, separate Wails log window. Reopening
focuses the existing viewer; closing it preserves the main window and Settings.
Application and Wails slog records are mirrored to a synchronized, memory-only
2,000-record buffer, capped at approximately 8 KiB per record. Existing stderr
output is preserved. The viewer polls incrementally, labels and colors severity,
supports keyboard scrolling, and pauses automatic scrolling when reading older
entries. Logs cover the current session only.

Validation: bounded retention, cursors, detached snapshots, grouped attributes,
severity and concurrent writers tested with Go's race detector. Rendered fixture
checks in scripts/capture-log-window.mjs cover light/dark themes, narrow windows,
scroll/follow behavior, error recovery UI and the close binding. Screenshots are
in /tmp/playlist-ai-log-window. These browser checks do not exercise native
window-manager behavior on Windows, macOS or Linux.

## Wizard model selection and public CLAP bundle — 2026-09-07

The language-model step now offers at most one recommendation: the largest
model in the existing eligible shortlist, preserving GPU memory reserves and
the two-entry CPU shortlist. Settings retains the full model catalog.

Surveyed public CLAP families, paired ONNX exports and specialized alternatives
in [CLAP model candidates](clap-model-candidates.md). The wizard downloads the
full-precision Xenova export of LAION larger CLAP music-and-speech, with pinned
weights, matching tokenizer and official ONNX Runtime 1.26.0 assets. Linux amd64
downloads total approximately 793 MB. Downloads resume; runtime extraction checks
the exact member's size/hash and native inference must pass before activation.
Users can select a custom manifest and follow linked training/export guidance.

Version 2 bundles run a Go-managed native worker inside a child of the desktop
executable, with no Python runtime dependency. Paired artifact fingerprints
prevent incompatible caches from mixing. Legacy bundle compatibility is retained.
The public model has passed runtime validation but has no reviewed musical-fit
calibration policy; installation does not enable automatic fit decisions. The
wizard states this before download and after installation.

Executed validation: ten public-export/reference comparisons, exact tokenizer
checks and Go preprocessing comparisons; actual native installation and desktop
worker health checks with no executable search path/Python setup; the complete
repository gate; browser fixtures covering one language recommendation, both
CLAP paths, retry/cancel/inference states, documentation links, both themes and
a narrow window. Screenshots are in `/tmp/playlist-ai-clap-wizard-ui`.
Reproduction commands and exact parity errors are in the candidate guide.

Platform limits: native inference was executed on Linux amd64/WSL2 only. Pinned
runtime files also cover Linux arm64, Windows amd64/arm64 and macOS arm64;
clean-machine tests are not claimed. Pure-Go application builds reject v2
installation before downloading; Windows needs an explicit cgo build for the
built-in worker. Intel macOS needs a custom compatible runtime bundle. The
2 GiB memory allowance remains provisional. No release or model publication
was performed, and no weights, recordings or user data enter the repository.

## Simplify navigation, export and preview setup — 2026-09-07

Removed the standalone Catalog screen, navigation/fallback routes, seed-build
helper, search/similar-track bridge methods and unused row actions. Generation
still uses the local recommendation catalog; a missing catalog now links to
the setup wizard.

Export preparation reads local track details directly, preserving order and
duplicate entries without calling MusicBrainz. The export page contains track,
artist, album and inclusion controls, with ISRC, match and confidence UI removed.
CSV and Soundiiz handoff remain available without an enrichment service. The
standard CSV schema is retained with an empty ISRC column.

The wizard preview step offers only Deezer and Spotify. It maps a saved off
preference to Deezer, saves the selected provider on Continue, and stays on the
step with an actionable error when saving fails. Settings can still disable
playback previews independently.

Validation: full repository gate passed; Go export tests cover local preparation,
ordering, duplicates, missing IDs and CSV export without an enricher. Browser
fixtures in `scripts/capture-export-ui.mjs` cover navigation/setup recovery,
export selection, retry/empty states, provider persistence and save failures,
both themes and narrow windows. Screenshots use `/tmp/playlist-ai-export-ui`.

## Playlist navigation, previews and request messages — 2026-09-07

Main screens scroll at the right window edge. Scrollbar thumbs, tracks and native
controls follow the light/dark palette, including older WebKit webviews. Setup
uses the same outer scrolling pattern. Narrow headers wrap without clipping.

Completed generation opens Playlist with its existing result, including partial
playlists. Empty results stay with the editable description and show detailed
reasons and next steps above it. Parse failures, generation errors and unresolved
or ambiguous references also appear in a dismissible, announced message. Closing
the message returns focus to the composer; dismissal does not bypass required
reference selection. Cancelled responses cannot navigate over newer generation.

Every playlist row has a labeled Preview button with loading, pause and retry
states. Missing previews explain provider availability and the Settings option.
Retries resolve a fresh URL; ended previews can play again. Intentional pauses
do not report playback-aborted errors. Requests remain on demand through the
existing provider and throttling; preview availability is not guaranteed.
Playlist adjustments sit behind a disclosure so the songs appear immediately.

Validation: `scripts/test.sh` passed (bindings, TypeScript, production build,
Go vet, pure-Go compilation, race tests and lint); the final frontend build also
passed after playback/layout refinements. `scripts/capture-recommendation-ui.mjs`
checks complete/partial navigation without rebuilding, stale responses, detailed
errors, clarification selection/dismissal, preview loading/play/pause/retry/replay
with a synthetic WAV, and themed scrolling at the window edge. Rendered checks
cover light/dark, 390/560/1100px windows, reduced motion and keyboard focus.
Screenshots and the report are in `/tmp/playlist-ai-playlist-ui`. Browser bridge
fixtures do not claim live-provider coverage or native Windows/macOS testing.

## Preserve accented artist references — 2026-09-07

The model-output validator discarded “Arvo Pärt” as invented when the original
description used “Arvo Part”. Source grounding now uses Unicode decomposition
and accent folding with word boundaries, consistent with catalog lookup.
It preserves the original description and source evidence, and still rejects
different names. No artist-specific aliases or musical-fit mappings were added.

The rules fallback also preserves an unfamiliar leading description in
“<description> music like <artist>”, including “classical”, instead of dropping
it and treating artist similarity as full compliance. Tests cover accentless
and decomposed names, distinct names, open descriptions and later modifiers.

The exact prompt “classical music like Arvo Part” generated 20 tracks against
the installed catalog through both the rules parser and a canonicalized model
output fixture. Both retain the artist and classical requirement and report
partial/approximate fit without audio evidence. This is a retrieval regression
check, not a live LLM or listening evaluation. Reproduce with:

```sh
PLAYLISTAI_TEST_CATALOG=/path/to/catalog go test ./internal/reco/multichannel -run TestClassicalArtistPromptBuildsPlaylist -count=1 -v
```

## Missing-artist online recovery — 2026-09-07

Generation now explains a missing catalog artist and looks up a corroborated
MusicBrainz identity, then tries Deezer top tracks in order until a recording
matches the local catalog. Additional MusicBrainz recordings provide a fallback.
It preserves artist/title/version identity, local vectors, original intent and
snapshot provenance. Lookup failures remain actionable clarification outcomes;
same-title covers and unrelated artists cannot silently replace a reference.

The existing MusicBrainz one-second and Deezer two-second application-wide
limits remain in effect. Searches are bounded and cancellable, use cached
metadata offline, and do not run while typing. Sources, limits, cache behavior
and reproduction commands are in `recommendation-correctness.md`.

Validation: full repository gate passed; HTTP/bridge tests cover later-track
success, exhausted lookups, mismatched identities/versions, ambiguity, retries,
budgets, cancellation and replay. Rendered missing/recovered/no-seed states are
in `/tmp/playlist-ai-artist-recovery-ui`. Deezer's top-track response format was
checked live; generation assertions use deterministic fixtures.

## Playlist setting explanations — 2026-09-07

Audio similarity, Discovery, Artist diversity, Playlist-context similarity and
Transition smoothness now have themed help popups on label hover or keyboard
focus. Each explains what the setting controls and the effect of higher and
lower values. Help also opens on click, supports Escape dismissal and remains
within the window. Sliders retain their descriptions for screen readers when
the popup is closed. Artist diversity captions now reflect its implemented
repeat and variety behavior.

Validation: the full repository gate passed, with final frontend typecheck and
production build after the Escape-dismissal refinement. The rendered browser
checks cover all five settings, keyboard navigation, hoverable help, Escape,
screen-reader descriptions, light/dark themes and 390-pixel windows. Screenshots
are in `/tmp/playlist-ai-tooltip-ui/setting-help-{light,dark}.png`.

## Windowless Windows helpers — 2026-09-07

The language-model server, accelerator probe, music-analysis worker and runtime
installer now start with Windows `CREATE_NO_WINDOW` and `HideWindow`, preventing
background console popups during generation and setup. Standard streams,
logging and cancellation retain their existing behavior; other platforms use
unchanged process startup.

Validation: the full repository gate passed on Linux. The affected packages
cross-compiled and passed vet for Windows amd64. A Windows-only subprocess test
checks that the child has no console window and still returns captured output;
its test binary cross-compiled successfully. Native Windows execution and visual
verification remain to be run on Windows.

## Playlist diagnostics in session logs — 2026-09-07

Rejected inferred anchors and successful internal constraint checks no longer
appear as playlist notices. The bridge records their original details, counts
and generation ID in the session logs at the default retained Info level.
Technical scoring limitations are logged in full and shown in plain language;
shortfall, artist-lookup and other actionable notices remain visible. Loading
older saved results applies the same presentation rules, including partial
reasons, without changing the stored history record.

Validation: bridge regression coverage checks that diagnostics reach the
session log once, are removed from both notice lists, and do not hide useful
shortfall or artist-lookup messages. The full repository gate passed.

## Consistent desktop scrollbar colors — 2026-09-07

Chromium/WebView2 and WebKit now use a complete themed scrollbar, including
track, thumb and corner, with a periwinkle hover highlight. Standard scrollbar
properties no longer override those custom parts. Other engines retain the
standard themed fallback, and forced-color mode retains native accessibility
styling.

Validation: the full repository gate and rendered browser checks passed. The
capture script now enables visible scrollbars, asserts exact light/dark colors
and right-edge placement, and captures both themes in
`/tmp/playlist-ai-scrollbar-ui/playlist-{light,dark}.png`. Browser verification
ran in Chromium on Linux; native Windows WebView2 was not available here.

## Preview startup without false failures — 2026-09-07

Preview playback remains Loading until the browser emits `playing`, including
buffering after startup. Each play attempt has an identity so interrupted or
superseded promises cannot overwrite a newer attempt. Abort errors are treated
as interruptions, successful playback clears stale error text, and real media
or permission failures retain actionable retry messages. Resetting a source no
longer performs an unnecessary initial seek.

Validation: the full repository gate passed. Rendered Chromium fixtures
reproduce an interrupted promise followed by playback two seconds later and
check that no error flashes in between. They also cover genuine playback
failure and retry, a late rejection after confirmed playback, pause/resume,
replay, and switching tracks. Screenshots are in
`/tmp/playlist-ai-preview-start-ui`; these are deterministic browser checks,
not a live-provider or native Windows playback test.

## Full-size macOS icon artwork — 2026-09-07

Icon Composer now uses the complete 1024-pixel Playlist AI artwork at 100%
scale, replacing the inset outline layer and light backing. Removed the unused
outline SVG and stale checked-in Assets.car. Icon generation runs once per
build, clears stale compiled catalogs, and bundle assembly clears old copies
before installing freshly generated assets. Older macOS toolchains and cross
builds retain the existing full-size ICNS fallback.

Validation: source checks confirm a centered 100% layer, 1024-by-1024 dimensions
and identical artwork to the shared icon. `wails3 task common:generate:icons`
and the full repository gate passed on Linux. Native Icon Composer compilation
and Dock/Finder visual verification require Xcode 26 on macOS and were not run.

## Wizard GPU-build messaging — 2026-09-07

Language understanding no longer shows a CPU fallback notice when the staged
GPU build is installed but the device probe is inconclusive. For other installs,
the notice says GPU memory could not be confirmed instead of claiming no usable
GPU exists. Confirmed device details and memory-based model sizing are retained.
This avoids a misleading warning on Metal-capable Macs; llama.cpp documents
[Metal as enabled by default on macOS](https://github.com/ggml-org/llama.cpp/blob/master/docs/build.md#metal-build).

Validation: the full repository gate and browser fixtures passed for CPU-only,
installed GPU build with inconclusive detection, and confirmed Apple Metal
device responses. Screenshots are in `/tmp/playlist-ai-wizard-gpu-ui`. The Metal
response is a fixture, not a native macOS hardware test.

## Smallest wizard model option — 2026-09-07

Language understanding includes the smallest known-size GGUF download in the
full catalog (currently Qwen2.5 3B) alongside the hardware-selected recommended
model. Only the selected recommendation receives the badge. If no curated
recommendation fits, the smallest option receives the badge only when it fits
the available memory policy; otherwise it remains an unbadged option with the
memory limitation explained. A model that already fills both roles appears
once. The shared catalog and active model are not modified by listing choices.

Validation: the full repository gate passed. Deterministic tests cover CPU/GPU
selection, insufficient memory, unknown sizes, deduplication and catalog
immutability. Browser fixtures check badge counts, the smaller model's download
action, narrow layouts and both themes. Screenshots are in
`/tmp/playlist-ai-wizard-smallest-ui`. The fixture route now also handles Vite
reload query strings so binding regeneration does not bypass the mocked API.

## CLAP download counters in megabytes — 2026-09-07

Recommended and custom CLAP downloads show downloaded/total megabytes beside
the progress bar, with one decimal place (for example, `123.5 MB / 793.1 MB`).
Unknown totals show the downloaded amount. The card consistently uses decimal
MB, and progress exposes the formatted amount to screen readers while keeping
percentage semantics. Captions wrap safely in narrow windows.

Validation: the full repository gate and focused CLAP wizard browser checks
passed, including MB text, accessibility, custom downloads, retry and narrow
layout. Screenshot: `/tmp/playlist-ai-clap-mb-ui/download-mb-narrow.png`.

## macOS title-bar clearance — 2026-09-07

The main header uses Wails' synchronous macOS detection to reserve 96 CSS
pixels at the left for the native close, minimize and full-screen controls.
The icon and Playlist AI title follow that inset. Other desktop platforms keep
their existing header padding.

Validation: the full repository gate and recommendation, export and log-window
browser checks passed. Platform fixtures assert the macOS inset and existing
Windows/Linux spacing at the desktop minimum width. Light/dark header captures
are in `/tmp/playlist-ai-mac-titlebar-ui/mac-titlebar-{light,dark}.png`.
Native macOS window-control verification remains pending.

## Instrumental seed discovery and CLAP vocal screening — 2026-09-07

`Instrumental, no vocals` now reaches seed discovery without a named artist or
track, including with the rules parser. MusicBrainz recording tags propose
catalog matches; Deezer recording search and local instrumental title matches
provide fallback candidates. Sampling preserves a replay seed. Titles and tags
do not establish musical eligibility. Provider throttles and request budgets
remain in place, with actionable outage and empty-search messages.

An installed parity-validated CLAP bundle now screens every candidate preview
for explicit no-vocals/instrumental requests, independently of the optional
general-fit calibration policy. Every segment must favor instrumental prompts
over vocal and non-musical descriptions with a conservative abstention band.
Vocal, uncertain and unavailable previews are excluded before progressive
display and ranking. Required-track conflicts request clarification. Wizard and
Settings text explain automatic vocal screening, local feature retention and
preview-only coverage. The preview CLI supports native v2 worker dispatch and
`-screen-vocals` for reproducible checks, with no Python runtime.

Validation: deterministic tests cover parsed prompts, strict filtering across
channels, weak comparisons, later vocal segments, missing/invalid evidence,
text failures, cache reuse, model capability gates, paging, randomization,
outages and replay. Full repository and rendered UI checks were run; screenshots
are in `/tmp/playlist-ai-instrumental-ui` and `/tmp/playlist-ai-vocal-wizard-ui`.
A live CPU run on the installed catalog returned two checked tracks for a
three-track request before the 120-second analysis budget expired. A separate
vocal control was rejected. Reproduction commands, exact observations and the
pending listening evaluation are recorded in `recommendation-correctness.md`.

## Genre-to-genre journeys — 2026-09-07

Fixed `A journey from ambient to energetic electronic` in rules and local-model
interpretation. Genre names remain scoped musical categories; energy adjectives
remain requested qualities instead of being mistaken for artist names. Model
category normalization supports open-vocabulary genre names and repairs misplaced
entity destinations. Genre-stage recording lookups run before broader enrichment.
Best-available generation now reserves and orders genre stages and distinguishes
known stage evidence from unknown placement. Existing artist journeys and scoped
date requirements remain covered. The request summary shows energy direction,
and output explains when energy cannot be verified. Parser and recommendation
version identifiers were advanced for cache/history compatibility.

Validation: the full repository gate, generation/lookup/replay regression tests
and rendered recommendation UI checks were run. A live metadata lookup found 34
catalog candidates and generated six tracks with genre-supported endpoints in
44.9 seconds. Energy remains unverified; this is not a musical-quality benchmark.
Screenshots and reproduction details are documented in
`recommendation-correctness.md`.

## Fifteen creative prompts with native CLAP ranking — 2026-09-08

Added a reproducible set of 15 artist, album, track, genre, descriptive-style and
journey prompts in `docs/sample-music-prompts.md` and the evaluation fixtures.
All named artists were active before 2018. The live evaluation uses local LLM
interpretation, Deej-AI candidate retrieval and native CLAP preview comparisons.
No sample-specific artists, genres or recommended recordings were added to
production selection rules.

Parity-validated CLAP bundles now provide advisory description ranking even
without a calibrated general-fit policy. Unknown categorical evidence remains
unknown, strict requirements retain their gates, and no-vocals requests screen
every preview segment. Journey scoring respects stages and uses relative CLAP
similarity for approximate placement only when stronger metadata is absent.
Artist/track-only descriptions receive a description comparison when the LLM
omits structured musical clauses. The wizard explains these capabilities.

Live checks exposed slow representative selection for broad artist matches,
qualified album/track references rejected after model rewording, and unavailable
MusicBrainz album lookup. Fixes rank identities before computing representatives,
honor cancellation, recover source wording only from independently grounded
artist/title parts, and support exact-identity Deezer album recovery with
corroborated catalog recordings. Ambiguous albums remain ambiguous. Provider
request throttles are unchanged.

The evaluation command now supports native worker dispatch, CLAP evidence
checks and replay using cached features with preview retrieval disabled.
Executed results, model provenance, reproducible commands and limitations are
recorded in `recommendation-correctness.md` and the accompanying data report.

Validation completed: all 15 latest live cases produced six checked tracks;
the final cached replay also passed all 15 with zero preview bytes fetched.
The full repository gate and rendered wizard/recommendation checks passed.
All results retain partial-fit status because general CLAP similarities are
uncalibrated; no listening-quality or full-recording compliance claim is made.

## Correctness and security review — 2026-09-08

Restricted preview URLs before WebView playback; bounded declared-size downloads
while streaming and validated resumed ranges. Moved strict metadata/essential
checks before progressive results, preserved selectable required-album members,
retained ambiguity for truncated album searches and prevented CLAP stage
preference from overriding contradictory dates. Recommendation version advanced
to `multichannel/v10`. Corrected the evaluation's automatic GPU-offload label.

Validation: targeted regressions and the full repository gate passed. Go and
frontend dependency audits found no known vulnerabilities. All 15 recorded
creative prompts passed the final native CLAP cached replay with six tracks
per case and no preview downloads. Scope and remaining coverage limits are
recorded in `recommendation-correctness.md`.

## Hardware-specific model badges in Settings — 2026-09-08

Settings and the setup wizard now share the same recommendation selection.
GPU mode recommends the largest curated model whose pinned weights fit measured
free memory on one device, bounded by total device memory and retaining runtime
headroom. A selected model's file size is no longer assumed to be reclaimable
GPU memory. CPU mode, including explicitly forced CPU configuration, recommends
only the smallest catalog download. The full Settings catalog remains available
for manual selection, with static GPU-tier recommendation badges removed.

Validation: deterministic tests cover pinned size versus display estimates,
free/total memory bounds, unknown or exhausted GPU memory, CPU selection,
catalog immutability and Settings/wizard agreement. The full repository gate
passed. Rendered Settings and wizard checks cover GPU, CPU, no-fitting-model,
light/dark and narrow-window states.

## Bounded external API backoff — 2026-09-08

Added shared exponential backoff with jitter for transient MusicBrainz/Deezer
read failures and 429 responses, also used by preview and bundle/catalog
retrieval. Respect `Retry-After`, cancellation and deadlines; bound each URL to
four attempts and decline retries whose wait exceeds 30 seconds. Preserve
provider dispatch throttles and charge retries/redirects to the existing metadata
request budget. Keep side-effecting exports and local inference single-attempt.

Added virtual-time retry, cleanup, throttle and budget regressions, plus an HTTP
download retry/resume/checksum fixture. Updated instrumental lookup fallback
assertions to account for retries. The exact policy and remaining streaming
limitations are documented in `recommendation-correctness.md`.

Validation completed: `bash scripts/test.sh` passed, including regenerated Wails
bindings, frontend typecheck/build, `go vet`, pure-Go compilation, race-enabled
Go tests and golangci-lint (zero issues).

## Startup application updates — 2026-09-08

Added a non-blocking, once-per-session GitHub release check and a themed,
keyboard-accessible update prompt with release notes, MB download progress,
cancellation, retry and dismissible errors. Production version reporting now
uses `build/config.yml`; development builds abstain. Verified packages are
handed to a hidden helper which waits for normal shutdown, applies the update
and relaunches. Portable updates retain a rollback copy; installed Windows apps
use the verified NSIS installer with native UAC approval. macOS verifies the
bundle signature and signing team. Package-managed/read-only installations
receive an actionable release-page fallback. No release or deployment is made.

Architecture, security boundaries, recovery behavior, executable checks and
platform validation limits are documented in `application-updates.md`.

Validation completed: full repository gate passed (race-enabled Go tests,
vet, lint, pure-Go compilation, regenerated bindings and frontend checks).
Rendered startup-update checks passed in light/dark and narrow-window states;
frontend typecheck/build were rerun after fixing focus containment. The production
binary reports `0.7.0` via `--version`. Updater tests compile for Windows amd64
and arm64 and macOS arm64. Actual UAC, Gatekeeper and packaged AppImage upgrade
execution remains host-specific validation, not claimed by these Linux checks.

Follow-up checks: updater lint also passed with Windows and macOS build tags.
Focused race tests cover superseding stale failure notices after a successful
retry and preserving custom configuration paths across the helper restart.

## Centered randomized CLAP samples — 2026-09-08

New preview analyses choose a centered random 22–47-second interval, bounded by
the available provider audio. Shorter previews are analyzed whole. Persist the
selection policy, available duration and exact analyzed coverage without changing
the installed CLAP model's ten-second tensor contract. Cached selections and
legacy embeddings remain reusable; no additional Deezer requests are introduced.
The fixed-preview versus full-recording limitation is explicit in
`recommendation-correctness.md`.

Focused audio/application/evaluation tests passed, including synthetic MP3
analysis, deterministic range/centering checks, cleanup, persistence and cache
reuse. The full repository gate is recorded below after completion.

Validation completed: the full repository gate passed, including race-enabled
Go tests, vet, lint, pure-Go compilation, Wails binding generation and frontend
typecheck/build. An odd-frame regression covers floating-point rounding when
centered sample boundaries are stored in seconds.

## Windows contributor CI repair — 2026-09-08

CI run `34212548266` reached Go tests after successfully installing pnpm and
building the frontend. Its two updater failures were reproduced with a native
Windows test executable using an 8.3 `TEMP` alias and uppercase `PATH`. Canonical
fixture directories now satisfy the existing staging-path checks; helper
environment fixtures use native path separators and normalize variable-name
casing while checking the exact retained PATH. No production security checks
were relaxed and neither failing test was skipped.

Windows setup, CI and release packaging now share `scripts/install-pnpm.ps1`,
which downloads the official PowerShell installer, selects pnpm 9, verifies the
installed executable/version, preserves current toolchain precedence and exports
the environment for subsequent Actions steps. Offline contributor checks cover
download failure, nonzero installer exit, missing executable, temporary-file
cleanup and avoiding PATH/Actions exports after failed installation.

Validation: native Windows updater tests passed under the reproduced CI
conditions; the real official pnpm 9.15.9 installer passed with version, PATH and
Actions-export checks in Windows PowerShell 5.1 and PowerShell 7. Offline
failure-path checks passed in both editions. The complete Linux
repository gate and CI/release actionlint passed. Windows installer validation
used a temporary PNPM_HOME and restored the previous user environment. A hosted
GitHub Actions rerun and full native Windows application packaging were not run
as part of this local repair.

## Music-only CLAP default — 2026-09-10

Changed new wizard installations from LAION Larger CLAP Music + Speech to the
full-precision `laion/larger_clap_music` checkpoint. Both FP32 encoders were
exported from pinned revision `a0b4534a14f58e20944452dff00a22a06ce629d1`,
published as checksummed release assets, and assigned a new bundle and embedding
cache identity. Existing Music + Speech bundles and their cached analyses remain
readable; they are never mixed with music-only embeddings. The desktop binary
still contains no model weights or Python runtime.

The export passed 10/10 numerical parity fixtures (maximum absolute error
`1.0430813e-7`, minimum cosine `1.0`), 7/7 tokenizer cases, 3/3 preprocessing
cases, and the native install/health test. A separate four-prompt text smoke
check found cosine similarities of `0.998960`–`0.999329`; therefore the model
remains uncalibrated for strict musical-fit claims pending held-out retrieval and
listening evaluation.

## Opt-in recommendation diagnostics — 2026-09-10

Settings now provides a detailed-diagnostics checkbox beside the application-log
viewer. When enabled, the in-memory session log records the submitted prompt,
resolved intent and parser fallback status; redacted provider request URLs and
response status/timing; metadata resolution; AcousticBrainz comparisons; CLAP
assessments and full preview-segment audio embeddings; generation outcome and
reproducibility data; and each selected track's ranking evidence and explanation.
AcousticBrainz records include every extracted low-level measurement and
high-level prediction used by the application, with upstream versions and
provenance. This makes parser, evidence-coverage and selection failures
inspectable without changing recommendation behavior.

Detailed records are disabled by default, never written to the process logger or
disk, and removed from the retained log as soon as the checkbox is cleared. The
preference itself persists. HTTP bodies and headers are not logged, and query
parameters whose names indicate credentials are redacted. Full prompts and model
responses can contain private listening interests, so the Settings control and
log window both show a warning while collection is active. Individual diagnostic
records may use up to 64 KiB so complete CLAP vectors survive serialization; the
whole session log remains bounded to 16 MiB and 2,000 records.

Focused tests cover opt-in retention and clearing, preference persistence,
credential redaction, and the presence of selection, AcousticBrainz and CLAP
events. Audio tests assert that newly computed and cached CLAP embeddings reach
the diagnostic sink without truncation. Generated Wails bindings and frontend
typechecking also pass.

## Deej-AI-only generation examples — 2026-09-10

The Generate screen now reads the selected recommendation mode independently of
the active parser. In Deej-AI-only mode it shows exactly four compatible request
examples: two artist-and-count requests and two counted artist-to-artist
journeys. Its placeholder, helper text, mode badge and Surprise action use the
same catalog-reference contract. Genre- or mood-only examples remain available
in evidence-aware modes, where those properties can be evaluated.

The rendered UI regression asserts the four exact examples and the visible
catalog-seed requirement. Frontend typechecking and the production build pass.

## Log-window severity controls — 2026-09-10

The log window now offers DEBUG, INFO, WARN, and ERROR minimum levels. DEBUG
enables the existing persisted diagnostic opt-in; higher levels stop collection
and purge debug records while retaining ordinary records for later filtering.
The viewer distinguishes shown/retained counts and filtered-empty states.
Settings refreshes its checkbox on focus after another window changes the value.

Ordinary Go DEBUG records now reach the in-memory store independently of the
console's INFO threshold, without enabling debug console/file output. Custom
slog levels are handled numerically. Opt-out is rechecked during insertion;
preference persistence and runtime activation are serialized across windows.
Stale local polls cannot undo a selection, and delayed record batches check
consent after retrieval before being displayed. No intent, ranking, or saved
playlist behavior changed. See [application logs](application-logs.md).

Validation: focused logging/bridge race tests and the full `scripts/test.sh`
gate passed, including generated bindings, frontend typecheck/build, pure-Go
compilation, vet, and zero lint issues. The rendered Chromium fixture passed
level filtering, debug persistence, pending/failed saves, concurrent-window
ordering, external opt-out, actual Settings checkbox/focus behavior, polling
failures, scrolling, and empty states. Light/dark and 420-pixel layouts were
captured and inspected using existing temporary browser dependencies. This is
Linux browser/fixture validation, not a native Windows/macOS desktop run or a
musical-quality benchmark.
