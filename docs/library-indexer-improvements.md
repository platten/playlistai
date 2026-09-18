# Paipack recommendation quality: implementation and indexer backlog

## Objective and guardrails

Improve prompt fidelity, reference similarity, listening enjoyment, diversity,
and sequencing together, with wider-catalog discovery as the first priority.
Owned tracks remain eligible: ownership is neither positive feedback nor a
reason to boost, suppress, or impose quotas. Keep Deej-AI, AcousticBrainz-first,
CLAP-first, and enhanced policies distinct. An unknown attribute is not false,
and sampled audio is not full-recording evidence.

This document separates implemented foundations from proposed work. No listening
study has yet established a musical-quality improvement. New extraction models,
recording crosswalks, and a v6 pack format are **not** implemented here.

## Implemented foundations

- Combined catalog overlays preserve optional external-track registration only
  when the base supports it. Library-only remains isolated.
- External CLAP/MERT cache identity stays tied to its base catalog; imported-pack
  identity remains part of the request/history fingerprint. Existing source
  caches can be reused without silently replaying a changed pack or experiment.
- Local feedback and playlist acceptance use a pinned combined evidence view,
  even in library-only output mode. Taste profiles keep local MERT evidence in
  separate, contract-keyed spaces, with explicit feedback, corrections, decay,
  multiple interests, and request overrides. Mere import/exposure is not liking.
- Explicit library reference comparisons now survive enhanced-mode relevance
  selection. This establishes similarity evidence, not verified musical facets.
- Experimental local MERT reference/taste scoring, signed DSP preference scoring,
  MMR redundancy, and sequencing use a separate evidence port. They never inject
  MERT vectors into the Deej-AI space. Sequencing hydrates a request-local cache;
  cancellation reaches database/vector reads.
- DSP uses the supplied empirical percentile distribution, honors known states,
  leaves missing requested axes in the denominator, preserves polarity and
  reduced degree, and does not apply journey-stage preferences globally.
  Negative DSP evidence no longer earns a positive reciprocal-rank bonus.
- Cluster membership alone no longer adds relevance. Repeated local reference
  IDs do not multiply retrieval queries.
- Existing tags become provenance-bearing annotations: genre/style, mood, tempo,
  key, language, edition/original dates, credits, work/movement, disc/track,
  release type, and ReplayGain. Conflicting source tags remain separate; literal
  delimiters are preserved. Legacy `mood_*` predictions retain unknown scales.
  Genre/style criteria use exact aliases and reviewed parent relationships, not
  artist-level generalizations. Other annotations are not automatically promoted
  to hard-constraint evidence. Multiword learned-metadata queries retain phrases.
- Indexer audio cache keys include the sampling contract. Nonfinite MERT windows
  cannot partially mutate pooled sums. Valid recording-ID tag aliases are
  recognized without confusing release-track IDs with recording IDs.
- `recoeval --paipack` compares experimental evidence off/on using production
  overlays in an isolated temporary import. It records composite identity,
  generation version, profile snapshot, and lossless seed. Per-case judgment
  coverage is explicit; pack-mode NDCG is unavailable when returned top-K tracks
  have missing judgments. It does not invent isolated retrieval-stage metrics.

### Activation and compatibility

Existing v5 packs need no rewrite for these consumers. Experimental ranking is
off by default. To evaluate it interactively, add to the app TOML:

```toml
[recommendation]
library_evidence_enabled = true
```

This currently affects enhanced mode only, not the other three mode policies.
Generation identity is `multichannel/v31`, with `+library-evidence/v1` when
enabled; taste identity is `taste-profile/v4`. Old saved results remain data, but
exact regeneration must pass existing catalog/algorithm/profile checks.
The sampling-key fix intentionally invalidates affected indexer audio cache
entries on a later indexing run. This change does not start that run or modify a
running indexer. No models or personal datasets are downloaded or committed.

## Data findings and their limits

The planning audit inspected the running indexer's state rather than assuming
an unfinished bulk pack existed. A bounded 3,000-record processed sample had
approximately 97.5% genre, 99.9% date, and 2.4% conventional mood tags; numeric
legacy mood predictions were also present. A separate bounded identity sample
had valid recording MBIDs on 9.43%, ISRCs on 30.37%, AcoustID IDs on 29.23%, and
at least one on 35.17%. These are snapshot/sample observations, not whole-library
coverage claims. No private track names are required in the backlog.

Exact normalized artist/title nominations against the roughly 957k-track base
catalog yielded 42.77% unique nominations in the sampled comparison, 0.10%
ambiguous, and 57.13% none. These are **candidate nominations, not verified
recording identities**. The base catalog lacks a general MBID/ISRC crosswalk.
Current audio describes roughly 30 sampled seconds per analyzed track, not the
entire recording. Existing packs already carry window-level DSP measurements;
duplicating those measurements is not an extraction upgrade.

## Priority 1: verified links and wider discovery

Still to implement, before claiming that imported audio improves external
discovery beyond restoring existing discovery interfaces:

1. Build a versioned local recording-link index, keyed by base version, pack
   generation, and matching-policy version. Store verified/provisional/conflict
   status, evidence, recording-versus-release identity, duration compatibility,
   and source alternatives. Never merge on artist/title alone.
2. Nominate base candidates with bounded normalized artist/title lookup. Verify
   against recording IDs using the existing cache-only enrichment path first.
   Online enrichment remains opt-in, cached, rate-limited, and budgeted. Neither
   Spotify-ID assumptions nor an absent offline crosswalk establishes identity.
3. For a verified local/base recording pair, retain local analysis on the same
   canonical recording while using the base's native Deej-AI vectors for wider
   retrieval. Preserve alternative playback/export locations after deduplication.
   The current duplicate suppression still discards duplicate local evidence;
   fixing it requires this explicit alias/evidence mapping.
4. For unmapped local references, retrieve nearby local MERT recordings with
   verified base links, choose at most five diverse proxy anchors, then retrieve
   in the native Deej-AI space. Normalize votes per original reference, not per
   proxy. Record the full route and confidence/coverage. Proxy similarity must
   never transfer an anchor's genre, vocals, language, or other attributes to a
   candidate. Return a partial outcome when evidence is insufficient.
5. Test ambiguous/live/remaster/cover cases, contradictory IDs, duration outliers,
   multiple locations, pack replacement, cache invalidation, canceled requests,
   and all output modes. Measure candidate yield and false-link rate separately
   from listening quality. Do not enable approximate links merely to raise yield.

## Priority 2: richer consumption and evaluation

Still to implement: typed multivalue metadata normalization across container
formats; conflict-aware tempo/key/language/date consumers; full journey-scoped
DSP scoring; evidence-family fusion that cannot reward duplicate sources;
cluster-informed exploration without ownership quotas; explicit local-feedback
retrieval as well as ranking; and constraint-aware recording/source alternatives.

Use the new production-path comparison as the starting point:

```sh
go run ./cmd/recoeval \
  --catalog /path/to/base-catalog \
  --dataset /private/paipack-cases.json \
  --paipack /path/to/library.paipack \
  --library-mode combined \
  --output /private/report.json --markdown /private/report.md \
  --left library_evidence_off --right library_evidence_on \
  --blind-output /private/blind.json --blind-key /private/key.json
```

Local IDs use `local:library:<pack-track-id>` in this CLI. At least five timestamped
cases are required by the existing temporal-split contract. Use fixed resolved
intents to isolate recommendation changes; CLI rules parsing is not a substitute
for evaluating the desktop model parser. Import/index scratch space is temporary
and may be large; the original pack and active user library are untouched.
Current catalog-coverage diagnostics use dense base rows, so do not interpret
that number as imported-library coverage. No external providers are configured
by this CLI comparison. Fully frozen external cache/provider snapshots remain
additional harness work.

Run a private 24-case blind pilot spanning owned/external references, mapped and
unmapped tracks, multiple tastes, negative references, niche/non-Latin metadata,
missing analysis, strict criteria, journeys, and cold starts. Judge the union of
retrieved alternatives independently of which variant selected them. Keep blind
identity keys separate, randomize presentation with a fixed recorded seed, and
do not commit judgments/listening history. Rate prompt fit, similarity, enjoyment,
discovery, diversity, and transitions separately; report judgment coverage,
constraint violations, duplication, unsupported/partial outcomes, and latency.

Freeze tuning before held-out listening. Missing judgments are not zero ratings.
Use paired uncertainty estimates and stratified failure review; a 24-case pilot
is for finding defects and refining the rubric, not proving general superiority.
Promote experiments only after held-out gains without constraint/identity
regressions, and measure runtime/memory against the unchanged baseline.

## Priority 3: playlist-indexer and paipack v6 proposal

Implement only with reader/writer compatibility and bounded-resource tests:

| Upgrade | Additional information | Contract and verification requirements |
| --- | --- | --- |
| Partial-window MERT recovery | Successful segments plus failed/silent/short window states | Attempt independent windows, retain successes, exclude failed duration from pooling; canceled/source-changed work must not publish; no reuse that falsely implies another file's coverage |
| Segment embeddings | Window vectors, requested/observed offsets, quality state | Full model/graph/preprocess/sampling/pooling identity; explicit dimension/dtype; indexed binary resource and checksums, not large JSON blobs |
| Coverage summaries | Requested/observed/analyzed seconds, missingness, window locations | Distinguish complete, partial, unsupported, failed, and absent; expose to ranker and UI without implying whole-track certainty |
| Typed metadata | Original multivalue tags, normalized values, units, aliases, conflicts, origin | Retain raw source tags, schema version, recording/work/release separation; never interpret undocumented legacy predictions as calibrated labels |
| Richer DSP summaries | Robust medians/spread and early/late trends from existing windows | Duration weighting, known-state masks, minimum coverage, contract-specific empirical distributions |
| Optional audio/text space | Paired music-audio/text embeddings and query encoder identity | Separate from MERT/Deej; model hashes, tokenizer/preprocessing, license, native runtime/platform support; cosine is not probability |
| Rhythm/harmony analysis | Tempo candidates/confidence, beat stability, key alternatives, uncertainty | Validate half/double-time and ambiguous key; no fabricated precision; measured extraction costs and coverage |
| Loudness/dynamics | Integrated/short-term loudness, true peak, loudness range | Measured analysis separate from ReplayGain tags; verify packaged FFmpeg capability on each host before using filters |
| Calibrated musical heads | Vocal/instrument/mood evidence with uncertainty | Licensed model/data, held-out calibration by domain, thresholds and abstention; do not promote raw logits into hard constraints |

V5 readers enforce exactly three archive members. Do not silently append files
or relabel changed model/sampling contracts under v5. Design v6 with a manifest
resource registry, optional analysis tables and bounded binary vectors, strict
path/size/checksum validation, independent channel versions, explicit missingness,
and export/import/combine tests. New readers should retain v5 support. Old readers
must reject v6 clearly, not misread it. Provide a reversible migration/export path
and preserve old generations while requests/history hold leases.

Segment storage is material: six 768-dimensional float32 vectors require 18,432
raw bytes per track, roughly 2.39 GB for 129,783 tracks or 18.4 GB per million,
before indexes/metadata/compression. Benchmark quantization and selective retention
against ranking/transition quality before choosing a default. These are size
calculations, not measured performance or compression results.

Model/runtime research must use primary documentation and record exact versions,
license, resource cost, and native Windows/macOS/Linux packaging. Initial sources:
[MERT-v1-95M model card](https://huggingface.co/m-a-p/MERT-v1-95M),
[music CLAP model card](https://huggingface.co/laion/larger_clap_music), and
[FFmpeg ebur128 documentation](https://ffmpeg.org/ffmpeg-filters.html#ebur128-1).
MERT's license and task-dependent layer choice require explicit review; installing
a new extractor is not justified solely by its availability.

## Completion gates

1. Foundation: deterministic regressions, race/lifecycle tests, generated
   bindings, frontend checks, and full repository gate.
2. Link/discovery: audited recording identity, bounded retrieval and cancellation,
   no duplicated evidence or inferred suitability from proxies.
3. Listening: private blind pilot, completed union judgments, frozen held-out
   evaluation; activate only the variants supported by evidence.
4. Extraction/v6: benchmark a bounded fixture, verify licenses and native runtime
   capabilities, round-trip/migrate/combine safely, then offer an explicit reindex.

Do not interrupt a running indexer or mass-reindex user files as an incidental
step in recommendation development.

### Executed validation (2026-09-18)

On the available Linux workspace, `./scripts/test.sh` passed: shell checks,
Wails binding generation, frontend typecheck, 23 frontend test files / 221 tests,
production frontend build, `go vet`, pure-Go core compile, full race-enabled Go
suite, and `golangci-lint` (zero issues). `git diff --check` also passed.
Independent review findings were fixed and covered by regressions, including
source-cache identity, feedback/history lifecycle, request cancellation, local
criterion metrics, required-reference fallback, and CLI blind defaults.

These are software correctness checks, not listening-quality measurements.
No native Windows/macOS execution, full-library extraction benchmark, new-model
packaging test, or private held-out listening study was performed in this change.
