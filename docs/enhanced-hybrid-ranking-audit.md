# Enhanced Hybrid retrieval and ranking audit

Investigated 2026-09-12 on `codex/enhanced-audio-remaining`, commit `7e91fc1`.
This is a research report, not an implemented behavior change. The companion
provider and prompt audits cover external ontologies and the native LLM trials.

The user's clarified preference is: prioritize strong matches, then use clearly
labeled close matches to fill the requested count. Their principal scenarios are
classical music, Aerosmith, relaxing electronic music like Christian Löffler,
workout music, and a Nine Inch Nails to Marilyn Manson journey. Explicit
exclusions and genuinely strict requirements must remain binding.

## What actually reaches each component

| Component | Current request representation | What it contributes | Important limitation |
| --- | --- | --- | --- |
| MusicBrainz discovery | Exact positive genre strings and essential genre/style strings | Artist search, catalog-resolved recordings, attributed metadata | Soft `preferences.styles` is omitted from iterative discovery; mood, texture, instrumentation and energy do not create its searches |
| Local Discogs metadata | Same genre strings, lowercased/whitespace-normalized | Release-derived candidate discovery, with catalog identity checks | No shared genre alias expansion; release tags are correctly not promoted to recording suitability |
| AcousticBrainz | Per-clause kind/text to a small, exact classifier-label map | Archived classifier margins, optional veto of essential/strict clauses | Most subgenres, synonyms, instruments and production descriptions are unmapped; its scores are not calibrated musical truth |
| CLAP | Structured clauses, each usually raw text plus a short caption | Preview/text cosine ranking; special instrumental screening | The full prompt is only a fallback when no structured clauses/references exist; lost LLM concepts generally stay lost |
| MERT | Audio from identity-checked previews; positive/negative reference vectors or feedback centroids | Audio similarity and transition preference | No text encoder, genre classifier, or direct mood classifier is used; prompt words cannot be translated into a MERT text query |
| DSP | A small exact production-phrase dictionary | Signed soft preferences for bass, sub-bass, brightness, dynamics and transients | Does not establish mood/genre/energy; its raw-prompt extraction is late and reaches only DSP |

The routing boundary is not one translator today. Similar meanings are handled
by independent exact maps and string branches with different coverage.

## Confirmed mismatches and limiting policies

### 1. Genre versus style placement changes discovery

`internal/enrich/musicbrainz/candidates.go:100` builds discovery queries from
positive `Preferences.Genres` and essential `genre`/`style` criteria. It does not
read `Preferences.Styles`. `internal/core/single_genre.go:5` likewise infers the
single essential category from genre preferences and existing criteria, not soft
styles. Thus a model emitting only `styles: ["melodic techno"]` can lose the
discovery channel that `genres: ["melodic techno"]` would activate. The CLAP
clause builder does read both fields (`internal/audio/session.go:121`).

This is a confirmed contract inconsistency; it does not prove every such prompt
is empty, because inferred anchors and cached audio may still retrieve tracks.
Use one canonical category query plan for discovery, essential criteria and
CLAP. Retain genre/style as source metadata if useful, but do not make retrieval
coverage depend on an LLM's arbitrary choice between them.

### 2. Aliases are inconsistent and compounds are sent to artist tags verbatim

`NormalizeIdentityPart` only lowercases and collapses whitespace
(`internal/core/track.go:34`). `CanonicalStyle` knows a few aliases such as
electronica/electronic (`internal/core/semantic_evidence.go:24`), while
AcousticBrainz does not call it (`internal/reco/multichannel/acoustic.go:55`).
The latter accepts `hip hop` but not `hip-hop`, and maps `relaxing` to `relaxed`
but has no general mood synonym adapter. `drum and bass`, `drum & bass`, `dnb`
and `D&B` do not share a complete cross-provider alias contract.

MusicBrainz first searches an exact artist tag. Its fallback splits every word
of a 2–4-word genre and requires all words as tags
(`internal/enrich/musicbrainz/artists.go:22`). This preserves requested words,
but an adjective/category compound such as `relaxing electronic` can demand a
`relaxing` artist tag rather than use electronic discovery plus relaxed audio
ranking. Genre graph alias resolution later in evidence matching cannot repair
an empty earlier retrieval query. Local metadata also uses exact normalized
genre keys (`internal/metadata/store.go:88`).

Use reviewed equivalence aliases, directional parent relationships and separate
facets. A broader parent is a candidate source, not proof of the child's genre.
For Christian Löffler, a reference resolves identity separately; do not turn an
artist name or an artist-derived style guess into an explicit user classifier.

### 3. Enhanced Hybrid inherits an asymmetric AcousticBrainz veto

Enhanced uses the ordinary `.55` AcousticBrainz weight, while default semantic
weight is `.35` (`internal/reco/multichannel/acoustic.go:17`,
`internal/reco/multichannel/config.go:57`). Decisive archived predictions
suppress the overlapping CLAP ranking contribution
(`internal/reco/multichannel/acoustic.go:217`). The archived margin is the
requested class score minus its strongest competing class; `±.5` establishes
the engineering supporting/opposing states (`acoustic.go:111`).

More consequentially, `metadataEligible` rejects opposing/conflicting archived
predictions for essential or strict clauses
(`internal/reco/multichannel/knowledge.go:114`, `acoustic.go:175`). This runs
before preview checking (`internal/reco/multichannel/iterative.go:174`, `:200`).
A CLAP/MERT-supported candidate can therefore never receive a fresh hearing if
archived genre predictions already oppose its essential category. The behavior
is deliberately tested in `TestAcousticOrchestratorEligibilityAndRequiredConflict`.
It is an existing conservative policy, not a race or transport defect.

Positive AcousticBrainz predictions do not independently prove essential fit
(`knowledge.go:23`; `acoustic_test.go:15`). This asymmetric treatment can reduce
recall without having established the classifier's negative predictive value
for this catalog. Do not simply reverse the preference or make positive CLAP
cosines proof. Evaluate per-concept routing: archived predictions should usually
be supporting/conflicting evidence in Enhanced; explicit identity exclusions
and verified strict contradictions remain disqualifying. A disputed essential
category may be a labeled close match only under the chosen fallback policy,
never silently promoted to a strong match.

### 4. Default artist diversity is a hard quantity and quality constraint

`genreArtistDiversity` returns true for every BestAvailable request except
artist/album-only restrictions (`internal/reco/multichannel/artist_policy.go:12`).
The selector then prefers a less-used artist ahead of the MMR score within the
same evidence tier (`internal/reco/multichannel/selector.go:184`). This bypasses
the user's `ArtistDiversity=0` setting. Its existing regression explicitly picks
scores `1.0, .8, .79` over a same-artist `.99`
(`internal/reco/multichannel/artist_policy_test.go:22`).

The sequencer additionally forces `NoRepeatArtistBackToBack=true`
(`internal/reco/multichannel/sequencer.go:43`). Even if the user never requests
hard spacing, a pool concentrated in a few appropriate artists can be truncated.
This is especially pertinent to artist-led listening and constrained journeys.

Under the clarified user preference, make default diversity a bounded soft
preference after match tier and musical relevance. Honor a genuine explicit
no-adjacent-artists constraint. Preserve genre direction and required endpoints;
do not create wrong-genre separators to fill count.

### 5. MERT is not a second text classifier, and it cannot recover missing words

`enhancedScores` uses explicit/inferred reference representations, then falls
back to feedback centroids (`internal/reco/multichannel/enhanced.go:117`). With no
usable reference or centroid, MERT has no request-specific semantic ranking
signal even if candidate representations exist. It can still aid transitions.
Both MERT and DSP weights are capped at `.15` each and applied after base
relevance (`enhanced.go:151`). These are engineering choices, not held-out
quality-tuned weights.

The right dictionary output for MERT is a routing decision such as
`reference_audio_similarity`, not a fabricated class label. Prioritize genuine
user references; retain inferred anchors as weaker, separately attributed
retrieval aids. Consider a compatible cached MERT nearest-neighbor candidate
channel for reference-led requests after coverage and quality evaluation. Keep
MERT and CLAP vectors/model versions separate.

### 6. The existing raw dictionary is too late and too narrow

`enhancedClauses` rescans `OriginalDescription` after parsing
(`internal/reco/multichannel/enhanced_phrases.go:19`). It recognizes production
phrases, masks double-quoted and resolved reference text, handles a narrow
negation window and deduplicates acoustic axes. It intentionally disables raw
extraction for journeys or any scoped clauses. It does not update saved intent,
CLAP queries, metadata searches or AcousticBrainz mapping.

Consequently `deep bass` can survive LLM loss for DSP while missing from CLAP;
`microdetail`, `microdynamics`, `sparkle`, `warm`, `airy`, `gentle`, `driving` and
`workout` have no direct DSP mapping. Some terms are subjective or ambiguous and
must remain descriptive CLAP requests rather than be equated with one measured
axis. `dark mood` is not necessarily low spectral centroid; `dynamic music` is
not necessarily large measured RMS spread.

Move deterministic, high-confidence extraction before the LLM. Preserve
source spans, polarity, strength, scope, and original text. After LLM parsing,
reconcile through explicit precedence and contradictions rather than silently
append competing interpretations. The resulting versioned canonical intent
must drive every adapter and history replay.

### 7. Evidence acquisition and the stopping rule limit both count and choice

Every descriptive candidate currently needs a successful preview comparison to
be eligible, even BestAvailable (`internal/audio/session.go:201`,
`internal/reco/multichannel/iterative.go:200`). Missing/unresolved Deezer previews
cannot be replaced by strong recording-level metadata in this route. This is a
confirmed coverage policy; it is not evidence that missing previews sound bad.
The instrumental screen is intentionally stricter: every sampled segment must
favor instrumental over vocal/non-musical alternatives with an abstention band
(`internal/audio/vocals.go:39`). Keep explicit no-vocals conservative unless a
separately validated alternative source can satisfy it.

Descriptive requests stop comparing when a complete playlist can be made from
roughly `2N` accepted candidates; genre-only requests can stop at `N`
(`internal/reco/multichannel/iterative.go:51`, `:273`). Eligible uncalibrated
scores do not guarantee these are good matches. A new cached CLAP channel helps,
but only over existing compatible analyses (`internal/audio/cached_search.go:55`).
It scans at most 20,000 rows in stable track-ID order; this becomes a recall
limitation for a much larger future cache.

Enhanced DSP/MERT share a 24-track / two-minute budget. The timer starts near
the beginning of generation, before discovery and replacement anchor proposals
(`internal/reco/multichannel/orchestrator.go:588`,
`internal/audio/enhanced_budget.go:9`). New DSP/MERT run before CLAP within each
preview analysis (`internal/audio/service.go:116`). Thus early poor candidates
can consume optional analysis capacity, while later better candidates have only
CLAP. The final Enhanced snapshot is prepared once; refills after an unsuccessful
assembly do not refresh it (`internal/reco/multichannel/enhanced_provider.go:25`).
These are confirmed scheduling mechanisms; actual losses require acquisition
timing/coverage measurements for the native workload.

Use cheap candidate retrieval and early CLAP comparisons to choose a diverse,
relevant measured subset, reserve reference capacity, begin optional inference
budget at its actual work boundary, and keep a separate overall latency bound.
Compare quality/adaptive stopping to the fixed `2N` baseline before increasing
downloads. A cache-only final snapshot refresh can expose evidence already
computed during refills without restarting acquisition or breaking replay.

### 8. Energy intent can be preserved but remain unused

`EnergyTrajectory` is not implemented by Enhanced sequencing. The current
trajectory interpolates catalog embeddings, not measured energy
(`internal/reco/multichannel/trajectory.go:10`); the sequencer reports
`energy_trajectory_unsupported` (`sequencer.go:130`). DSP RMS, dynamics and onset
rate are different physical measurements and should not be mislabeled as one
universal energy classifier. This matters for workout prompts and building
intensity through a journey even when the LLM JSON is accurate.

Plan typed tempo/intensity goals with explicit evidence availability. First use
descriptive CLAP ranking and supported archived rhythm/tempo evidence where
available; only introduce a measured intensity composite after evaluating its
agreement with listener judgments. A request for exact BPM remains unknown if
no reliable tempo estimate is available.

## Recommended implementation order

1. **Trace and baseline the translation contract.** For the original prompts and
   ten realistic additions, record extracted spans, canonical concepts, LLM
   proposals, overrides/conflicts, provider queries, mappings/unsupported routes,
   and candidate attrition by reason. Run fixed-seed cold and warm-cache cases.
   Keep detailed original-prompt logs opt-in/local. Count metrics alone do not
   establish quality.
2. **Introduce a versioned concept dictionary and compiler.** Include aliases,
   safe spelling variants, equivalence versus parent/fusion relations, provider
   projections, natural CLAP captions, and unavailable-capability reasons. Start
   with the user's musical scenarios plus negatives, female vocals,
   instrumentation and common tempo/intensity wording. Require exact span and
   scope confidence for deterministic overrides; quoted/named entities and
   ambiguous words stay protected. Resolve `christrian loeffler` through entity
   matching, not a genre dictionary. Migrate saved intent with an extraction and
   mapping version while preserving original decisions for replay.
3. **Unify category retrieval and per-provider adapters.** Genre/style aliases
   produce the same discovery query plan. Split mood/instrumentation from genre;
   add bounded alias and parent candidate queries with provenance. Preserve the
   full original concept for CLAP and final comparison. Do not promote artist or
   release tags into recording-level evidence.
4. **Apply the user's strong-first, close-match policy.** Introduce an explicit
   fit tier independent of evidence availability and evidence confidence. Strong
   matches meet calibrated/audited criteria; close matches disclose the exact
   unsupported or weak facet. Never fill with hard-excluded identities, verified
   strict contradictions, reversed journey stages, or missing required tracks.
   Reassess the uncalibrated AcousticBrainz veto and automatic hard artist
   spacing as Enhanced-specific policy changes. A partially unsupported request
   can be useful without being called fulfilled.
5. **Improve the comparison pool and optional model scheduling.** Reserve
   reference analysis, avoid spending MERT on poor early candidates, and refresh
   cached final evidence after refills. Tune stopping using measured strong/close
   yields and rank stability, with an explicit latency/download cap. Add MERT
   cached reference retrieval only when enough compatible vectors exist.
6. **Tune and release only with held-out evidence.** Compare baseline, dictionary
   only, adapter changes, gate/diversity changes, and scheduling/ranking changes
   separately. Prefer per-concept source reliability over an arbitrary global
   CLAP-versus-AcousticBrainz switch. Retain the other recommendation modes.

These are implementation stages in the existing consolidated PR workflow,
not instructions to create multiple milestone PRs.

## Acceptance tests and metrics

- Alias invariance: hip hop/hip-hop, drum and bass/D&B, electronica/electronic
  produce equivalent canonical concepts and appropriate provider-specific
  queries. Parent retrieval never becomes child verification.
- Field invariance: an otherwise identical genre/style input produces the same
  discovery pool, while mood/texture stays distinct from category.
- Protected evidence: exact negative artists, quoted track/artist titles,
  `not only`, `not too`, `less`, journey stages, and genuinely strict no-vocals
  survive extraction, LLM correction, controls and history replay.
- Diversity: zero diversity cannot promote a substantially weaker different
  artist above stronger same-artist matches; explicit hard spacing still holds.
  Default spacing cannot truncate an otherwise eligible requested count.
- Hybrid conflicts: archived classifier conflict is visible; test that a source
  dispute is handled according to strong/close policy and never silently erased.
- Scheduling: cold-cache reference and final-candidate coverage, attempted versus
  successful analyses, inference time, provider requests, downloaded bytes, and
  retained evidence under stop/cancel/refill all have separate measurements.
- Outcomes: report requested/returned count, strong count, close count, strict
  violations, known mismatches, unsupported facets, no-preview rate, catalog
  resolution rate, duplicate rate and journey completion. Do not equate unknown
  with mismatch or classify a track as strong merely because its metadata exists.
- Musical evaluation: blind pairwise preference and per-facet listener ratings,
  precision at requested count, strong-match yield and close-match usefulness.
  Separate original-model/optimized-runtime parity from musical discrimination.
  Use the ten new prompts as evaluation inputs, not as the entire dictionary's
  tuning set; hold out paraphrases and unseen artist references.

## Checks actually executed

Read-only code inspection and existing bounded offline regressions; no model
downloads, native inference, provider requests, production edits or new tests.
The following command passed with `CGO_ENABLED=0`:

```powershell
go test ./internal/reco/multichannel ./internal/audio ./internal/enrich/musicbrainz ./internal/core -run 'Test(Acoustic|EnhancedRules|EnhancedDSP|EnhancedPhrase|EnhancedProvider|SoundSelection|QueryPlan|TypedQueries|RepeatedGenre|CandidateStream|CompoundGenre|Style|SinglePlaylistGenre|WantsInstrumental)' -count=1
```

Package times were 0.464s, 0.168s, 0.327s and 0.053s respectively. These confirm
the documented existing mechanics, not musical quality or the proposed changes.
The following focused selector/spacing policy check also passed in 0.148s:

```powershell
go test ./internal/reco/multichannel -run 'Test(GenreSelectionBroadensArtistsWithoutLoweringRelevanceFloor|GenreBuildBalancesArtistsAndReturnsSafePartial|EvidencePrecedesPersonalizationAndDiversity)' -count=1
```

`git diff --check` passed. Only this report was written by this audit.
The public-model contract review and ten native prompt trials belong to the
coordinating investigation and are not claimed as executed in this audit.
