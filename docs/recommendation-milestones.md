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
