# Enhanced Hybrid: models-off investigation and remediation record

Investigated 2026-09-13 initially against `main`, HEAD `988d3d3`. The user then
authorized implementation, which was rebased onto `main` at `4d19f89` in
`codex/fix-enhanced-hybrid-models-off`. Existing unrelated `.tmp/` and `datasets/`
content remains untracked and unchanged by the implementation.

The branch now protects repeated artist exclusions and short artist-only syntax,
keeps output bans out of negative sound affinity, adds reviewed ambient discovery
terms and a gentle-pulse CLAP caption, makes worker mutex waits cancellable,
shares fixed vocal-query encodings, discovers compatible cached instrumental
recordings, continues instrumental-tag discovery, reports progress before vocal
screening, and bounds submitted generation to two minutes. When MusicBrainz,
Deezer, and local title lookup yield no instrumental lead, one cached, bounded
Wikipedia links request may propose exact catalog artists; catalog identity and
CLAP still decide whether any recording is usable.

The reported configuration is Enhanced Hybrid with the local intent language
model, DistilBERT, and MiniLM disabled. Other modes were not exercised by the
reporter. For the instrumental request, the reporter waited **300 seconds**
without a message, error, or candidate. Whether Cancel responded is unknown.
The follow-up report `Aerosmith only` was investigated on the same date against
`codex/settings-reset-generation-controls`, HEAD `13d719b`. Its findings and the
implemented short-form parser regression are recorded below.

## Findings and confidence

### 1. Aerosmith exclusion: reproduced parser defect

For `like Aerosmith without Aerosmith 10 tracks`, an offline rules-parser probe
produced this interpretation:

```text
positive artist = "Aerosmith without Aerosmith 10 tracks"
negative references = none
hard constraints = none
artistsExclude = none
count = 10
trackCountExplicit = false
```

This is sufficient to establish that the explicit exclusion is lost before
ranking. The user's actual returned playlist was not reproduced with their
catalog and installed application.

The positive entity boundary omits `without` and the trailing quantity. The
negative entity then consumes `Aerosmith 10 tracks`, which fails name recognition.
Meanwhile the legacy parser's exclusion evidence points to the first occurrence
of Aerosmith. Reconciliation treats that evidence as belonging to the positive
reference and removes the exclusion. The malformed reference also overlaps and
removes the explicit count atom. See `internal/intent/rules/rules.go:68,157,169`,
`internal/intent/lexicon/extract.go:24,225,249,347`,
`internal/intent/lexicon/composition.go:19`, and
`internal/intent/lexicon/reconcile.go:70`.

Punctuation does not reliably rescue this: a comma before `without` still loses
the exclusion; commas around the excluded name can create a spurious excluded
artist named `10 tracks`. Correctly constructed artist exclusions are enforced
by both multichannel and Deej-AI eligibility tests. The bug is in shared parsing,
so it must be fixed and tested across all four recommendation modes.

There is a second semantic concern: `exclude_artist` also creates a negative
reference (`lexicon/reconcile.go:180`), which contributes negative audio/vector
affinity (`multichannel/vectors.go:44`, `ranker.go:78,168`, `enhanced.go:131`).
The desired meaning here is **use Aerosmith's sound as a reference and exclude
their recordings**. Penalizing that sound as well would undermine the request.

### 2. Ambient electronica: interpretation survives; downstream cause unresolved

The exact prompt parses as five tracks, an essential `ambient electronica`
genre, and a preferred `gentle pulse` texture, with no inferred artist anchors.
Both phrases already exist in the 117-concept registry. The disabled language
models do not disable deterministic extraction or the shared dictionary.

`internal/audio/query.go:20` compiles these CLAP queries:

```text
ambient electronica
Music in the style of ambient electronica.
gentle pulse
Music with gentle pulse.
```

There is a discovery coverage gap worth addressing: `ambient electronica` has
only its exact MusicBrainz spelling in `internal/musicconcepts/concepts.json`.
`ambient electronic` is a separate concept, not an alias. These distinctions
are intentional in current tests; broadening retrieval must not silently make
broader genres proof of a narrower requested category.

A synthetic, offline orchestrator probe reproduced the same generic
`eligible_tracks_exhausted` result under two different conditions: five
candidates with unavailable previews, and five cached candidates with weak
CLAP comparisons. A separate synthetic case with stronger comparisons returned
five close suggestions. These are contract checks with fabricated scores, not
measurements of CLAP accuracy or recommendations to change its threshold.

Consequently the user's error does **not** establish that synonyms are the
root cause. Discovery misses, missing preview coverage, worker failures,
contradictory evidence, and the relevance floor need separate reporting.
The generic outcome is emitted at `multichannel/orchestrator.go:1039`.

CLAP compares audio with text. MERT currently compares audio representations
with reference audio and compatible feedback; it has no text-to-genre or
text-to-texture interface. A dictionary can improve retrieval and CLAP captions,
but cannot turn MERT into a text classifier. This agrees with the publishers'
[CLAP interface](https://github.com/LAION-AI/CLAP) and
[MERT model card](https://huggingface.co/m-a-p/MERT-v1-95M).

### 3. Instrumental: discovery and latency gaps; native hang not reproduced

The rules parser preserves `instrumental` and the hard `exclude_vocals`
constraint. Seedless instrumental discovery already exists:

1. Search MusicBrainz instrumental-tagged recordings, at most three pages of 100.
2. If no catalog candidates were found, try Deezer and local title search.
3. Propose up to three catalog-resolved recordings as inferred anchors; check
   their musical suitability afterward.

However, `OpenCandidates` requires a genre. This prompt supplies none, so it
gets no continuing discovery stream when the initial candidates are unsuitable.
The fallback tests catalog availability before audio suitability, leaving a gap
when some catalog recordings exist but every one fails the vocal screen.
See `internal/enrich/musicbrainz/instrumental.go` and `candidates.go`.

There is also a **reproduced local-cache recall bug**:
`internal/audio/cached_search.go:72` skips every strict clause and returns no
candidates without a remaining positive query. This exact prompt has only strict
audio clauses. An isolated regression demonstrates a cached recording passing
`Session.Check` as eligible while `CachedCandidates` returns zero candidates.
Thus even existing compatible instrumental evidence cannot provide a seed here.

Several confirmed implementation details can produce a long wait:

- Metadata preparation has a 30-second budget, but the enclosing bridge
  operation uses cancellation without an overall deadline
  (`bridge/lifecycle.go:86`, `app/lifecycle.go:11`).
- The audio session gets a **15-minute** budget whenever a candidate source is
  configured, even if this request has no usable discovery stream
  (`multichannel/orchestrator.go:653`, `iterative.go:14`).
- The first usable instrumental preview initializes **12 text embeddings**
  sequentially. They are cached only for that session
  (`audio/vocals.go:20,64`). Each CLAP worker call allows up to 60 seconds;
  several slow successful calls can therefore exceed the reported 300 seconds.
- Anchor checking precedes candidate callbacks and provides no per-anchor
  update (`multichannel/orchestrator.go:245`). An empty candidate list does not
  establish that no seed was found.
- CLAP's worker acquires an ordinary mutex before checking cancellation or
  starting its call timeout (`audio/worker.go:98`). A waiting request cannot
  promptly honor cancellation. An isolated test reproduced this: an already
  cancelled call remained blocked until the mutex was released. The MERT worker
  already has a context-aware
  acquisition pattern (`audio/mert_worker.go:54`).

Catalog reference resolution is synchronous and does not accept a context, so
its individual calls also need review when implementing a whole-operation
deadline. A timer around the caller alone cannot interrupt blocking work.

The current frontend should display a fallback status and elapsed time while
generating; generation IDs are connected correctly in the inspected code.
Existing cancellation and stale-progress tests pass. Thus the completely blank
native status remains a separate, unconfirmed failure requiring reproduction
against the installed build; it should not be explained away as normal discovery.

### 4. Aerosmith only: reproduced missing artist restriction

The exact rules-only prompt `Aerosmith only` produces:

```text
positive artist = "Aerosmith only"
hard constraints = none
source atoms = none
count = 20 (default)
```

`only Aerosmith`, `Aerosmith only 10 tracks`, and `10 tracks Aerosmith only`
also omit `require_artist`. `songs by Aerosmith only` correctly produces the
constraint, but appending `10 tracks` immediately after `only` loses it again.
`music only by Aerosmith` correctly produces an Aerosmith reference and restriction.

The legacy matcher requires forms such as `music only by Artist`
(`internal/intent/rules/artist_only.go:9`). The source extractor's three patterns
require a music/songs/tracks noun and `by`; its suffix form additionally requires
punctuation or end-of-input after `only`
(`internal/intent/lexicon/composition.go:28`, `:204`). Neither recognizes the short
phrase. Reconciliation deliberately drops `require_artist` unless a protected
artist-only atom rebuilds it (`internal/intent/lexicon/reconcile.go:67,130`).
Therefore improving only a model response or the legacy matcher is insufficient.

The downstream multichannel restriction is present: it resolves the required
artist's identity and rejects all other artists at `artist_only.go:25` through
`knowledge.go:135`. Its existing test passes when supplied a correct typed intent.
The actual user playlist and exact-prompt native generation were not reproduced;
the confirmed defect is the missing restriction before resolution/ranking.

This prompt means an exclusive output artist, not a similarity suggestion. Add
it to the highest-priority intent fix alongside the exclusion failure. Artist
diversity, close-match filling, refills, and Wikipedia discovery must never widen
an explicit artist restriction. An insufficient artist catalog should yield a
partial result rather than tracks by someone else.

## Recommended delivery order

### Step 1 — Protect explicit intent

Owner: intent/core implementation; read-only review by a separate reviewer.
Files: `internal/intent/rules/`, `internal/intent/lexicon/`, affected core reference
semantics, and consumers of negative reference affinity.

- Use shared, quote-aware boundaries for references, exclusions, and quantities.
  Track exact occurrence offsets rather than locating the first matching word.
- Retain the positive Aerosmith reference and independently grounded output
  exclusion. Do not create a negative similarity signal from an output-only ban.
  Preserve actual negative sound references such as “not sounding like X.”
- Recognize `Artist only`, `only Artist`, and count-bearing variants as a
  protected `require_artist` constraint with a separate clean artist reference.
  Preserve the restriction through reconciliation, overrides, and selection.
  Keep `like Artist`, additive `not only Artist`, genre-only requests, and literal
  entity names containing `Only` distinct. Do not strip the word globally from
  quoted titles or names such as `The Only Ones`.
- Preserve explicit count and protect real names containing numbers, conjunctions,
  unit words, or negation words: `like Men Without Hats 10 tracks` and
  `like Men Without Hats without Men Without Hats 10 tracks` must retain the
  correct identity. Use context and corroborated identity rather than a global
  split at `without`; keep ambiguous spans available to resolution. Keep
  required-output conflicts explicit.
- Bump affected parser/translation and ranking identities. Invalidate derived
  interpretations and assessments as appropriate; retain compatible raw audio
  embeddings. Load old saved intent without silently reparsing its prompt.
  Snapshots already affected by this bug have no exclusion to preserve: retain
  their saved result and reproducibility, and use the corrected parser on an
  explicit regeneration. If an affected snapshot is identified, explain that
  regeneration is needed instead of claiming its existing tracks were repaired.

Acceptance: every returned track excludes Aerosmith and its resolved aliases in
all four modes; positive similarity guidance remains; count remains explicitly
10; refill and required-track conflicts preserve the exclusion. History checks
cover new fixed snapshots and old snapshots that actually retained the constraint;
previously lost instructions are recovered only through explicit regeneration.
For artist-only requests, Enhanced Hybrid, CLAP-first, and AcousticBrainz-first
must return only the resolved artist's recordings or an honest partial/clarification
outcome. Deej-AI-only currently declares `require_artist` unsupported; preserve
that explicit outcome rather than silently ignoring the restriction or adding
new capabilities without a separate decision. `Aerosmith only, without Aerosmith`
must report a conflict. Diversity and exhaustion must never introduce outsiders.

### Step 2 — Bound generation and expose why it stopped

Owners: audio/engine implementation and bridge/UI implementation, with one owner
per file. Dependencies: no Wikipedia work required; can proceed alongside Step 1.

- Give the submitted operation one deadline spanning preparation, waiting for
  the worker, anchor screening, discovery, and candidate analysis. Nested budgets
  may shorten but never extend it. Separate user cancellation from budget expiry:
  cancellation discards the operation; expiry may return already eligible tracks.
- Proposed initial interactive target: a terminal result or actionable timeout
  within **120 seconds**, validated against cold and warm native Windows runs.
  This is a proposed product target, not measured achievable performance. Reserve
  completion/cleanup time and use fake clocks for deadline tests.
- Make CLAP worker acquisition cancellable. Share and bound immutable vocal
  text-query embeddings by complete model/tokenizer/preprocessing and vocal-policy
  identity; coalesce concurrent initialization. Do not cache failures as evidence.
- Report stage transitions before expensive work: finding recordings, preparing
  vocal screening, checking a starting point, and checking candidates. Show elapsed
  time and a periodic liveness update even when zero candidates qualify.
- Return reason codes and counts for no catalog identities, exhausted candidate
  sources, no matching previews, unknown vocal evidence, explicit mismatches,
  worker failure, budget expiry, and below-floor musical fit. Keep detailed
  identities/prompts behind existing diagnostic opt-in.
- Keep Cancel visible and responsive. “Stop and keep checked tracks” is useful
  only after eligible tracks exist; an empty provisional list must still show
  the active stage and a bounded path to completion. Preserve stale-event guards.

Acceptance: an offline fake reproducing slow/no-response providers terminates
within its configured deadline; cancel interrupts worker waits; no late result
replaces a newer request; terminal errors distinguish unavailable evidence from
poor fit. Inspect these states in the native host and small/large windows in both
themes, with keyboard access and reduced motion.

### Step 3 — Improve seedless supply and provider translation

Owners: music knowledge/retrieval and concept/audio adapters. Depend on the shared
budget and reason accounting from Step 2.

- Extend the existing candidate stream to support instrumental-only requests.
  Continue after candidates fail musical screening, not just after identity lookup
  fails. Prefer cached compatible evidence, then existing metadata/recording
  providers; track rejected recordings and artists to prevent repeat loops.
- Repair cached retrieval for strict instrumental requests. Use an appropriate
  positive instrumental query to nominate candidates, then apply every strict
  predicate through normal screening. Do not make strict clauses optional or
  confuse a vocal exclusion with a positive request for vocal recordings.
- Extend the existing registry with reviewed exact aliases, separate broader or
  related retrieval terms, and useful CLAP captions. For this ambient request,
  seek ambient/electronica neighborhoods while preserving the original genre and
  gentle-pulse requirement for assessment. Do not replace texture with invented BPM,
  energy, or a MERT label.
- Aggregate alternate captions as one facet, not independent votes. Preserve
  polarity, required/preferred strength, and explicit unknown evidence.
- Keep strong/close tiers and hard constraints intact. Change relevance thresholds
  only after an oracle-intent replay and held-out listening comparison isolate
  them as the problem; do not fill five slots with unrelated tracks.

Acceptance: exact approved aliases yield equivalent plans; related terms expand
candidate supply without certifying genre fit. If eligible evidence exists, the
fixtures return five relevant tracks; otherwise the result states the actual
shortfall. Instrumental requests try a new bounded batch after vocal/unknown
rejections and never accept a vocal or unassessed track merely to reach N.

### Step 4 — Add Wikipedia as a bounded discovery fallback

Recommendation: use the user's proposal after the preceding fixes. Wikipedia is
a source of artist leads, not track-level acoustic evidence. Existing Wikipedia
support follows MusicBrainz/Wikidata links for artist/album context; it does not
search for a new seedless pool.

Implement through the existing music-knowledge port and candidate stream:

1. Search Wikipedia from extracted public musical concepts only when cached and
   existing provider discovery cannot supply suitable anchors. Preserve local
   prompts and listening data; document the additional provider handoff.
   Respect explicit artist restrictions throughout: this fallback cannot replace
   an artist-only request with other artists discovered on a page.
2. Use bounded relevant pages/sections and links to identify musical artists.
   Corroborate each artist through Wikidata/MusicBrainz and catalog identity;
   reject ambiguous names and unrelated article links.
3. Sample catalog recordings deterministically using the generation RNG, biased
   toward available identity-matched previews and relevant metadata. A random
   recording from an artist is not presumed instrumental.
4. Apply the same hard exclusions and preview/fit checks. Try another recording,
   then another artist, retaining rejection history and attribution. Promote a
   suitable recording to an inferred anchor, never a required output track.
5. Stop at the shared deadline/request budget and return an honest partial or
   unsupported outcome. Suggested starting caps for evaluation: three pages,
   five artists, three recordings per artist, with all HTTP requests also counted
   against the existing provider/request budget. Respect rate limits, caching,
   Retry-After, response-size bounds, and an identifying User-Agent.

MediaWiki supports [page search](https://www.mediawiki.org/wiki/API:Search),
[outgoing links](https://www.mediawiki.org/wiki/API:Links), and
[page properties](https://www.mediawiki.org/wiki/API:Pageprops) for this approach.
Use serial, cached requests and bounded backoff under its
[API etiquette](https://www.mediawiki.org/wiki/API:Etiquette); the proposed caps
above are application limits, not claims about Wikimedia's service limits.

Cache source URL, revision, derived artist identity, and applicable attribution;
avoid storing full article text when only structured leads are needed. Freeze
discovery evidence for replay so changing pages or search order cannot silently
change saved results. All desktop behavior remains Go; no new model download,
supervised classifier, or Python runtime is necessary.

Acceptance: a fixture's first artist yields vocal tracks, the next supplies an
eligible instrumental recording; ambiguous identities, no catalog overlap,
missing previews, cycles, throttling, offline operation, and cancellation all
terminate within budget. Same seed plus frozen evidence reproduces the result.

## Validation and release gates

Start with all four exact prompts and paraphrases in rules-only mode. Add missing
commas, count placement, repeated artists, aliases/diacritics, `not only`, quoted
names with numbers, `mostly instrumental`, and conflicting required recordings.
Run shared parsing and identity exclusions through Enhanced Hybrid, CLAP-first,
AcousticBrainz-first, and Deej-AI-only. Preserve Deej-AI-only's catalog-only policy:
seedless descriptive requests should return its intended actionable outcome,
not gain Wikipedia or preview dependencies. Later test optional model combinations
to ensure advisory outputs cannot overwrite explicit facts.

Use deterministic offline providers and fake workers for correctness. Independently
measure native cold/warm startup, first status, first candidate, terminal outcome,
provider requests, preview availability, rejection counts, and cancellation
latency with installed model/catalog identities recorded. Use the current
`cmd/musiccheck` replay tools to separate parsing from retrieval and scoring.
Synthetic vector checks do not establish musical quality; compare caption and
retrieval changes with held-out recordings and listening review.

After implementation, run targeted packages, frontend interaction tests, rendered
checks using existing `scripts/capture-*.mjs`, `scripts/test.ps1`, and
`git diff --check`. Regenerate bindings if contracts change. Report native host
coverage separately from cross-compilation. Implementation was subsequently
authorized in this task. No commit, PR, release, or deployment has been performed
as part of this record.

## Investigation checks performed

- Offline exact-prompt parser and synthetic orchestrator probes under
  `.tmp/exclusion-investigation/` and `.tmp/semantic-investigation/` reproduced
  the findings above.
- Existing focused rules, lexicon, core, resolution, Deej-AI, multichannel,
  concept-registry, audio, and discovery tests passed. These tests omit the newly
  identified exact boundary and slow cold-start failures.
- Two isolated Go-overlay regressions **failed as expected**, reproducing defects:
  `TestDiscoveryInvestigationCanceledCLAPMutexWait` (0.03 seconds), and
  `TestDiscoveryInvestigationStrictInstrumentalCachedSeeds` (0.02 seconds).
  Overlay source files live only in `.tmp/discovery-investigation/`; production
  package files were not edited.
- Focused bridge generation, cached-preview reuse, history, and stop/progress
  tests passed. An initial attempt encountered a concurrently edited test's
  compile error; the same command passed after that external edit was corrected.
- Three frontend cancellation/progress tests passed, 56 unrelated tests were
  skipped by the name filter. The pnpm wrapper first attempted an install and
  aborted; tests were run directly with the installed Vitest entry point.
- No native application reproduction, live Wikipedia/provider query, full
  repository gate, or musical-quality benchmark was performed. Those are
  implementation validation work; no real user data was reset for investigation.

Targeted bridge command:

```powershell
go test ./internal/bridge -run 'TestGenerationProgressAndStopAreScoped|TestMatchingPreviewIntentIsReusedForGeneration|TestGenerateFromPrompt|TestEngineOnlySettingsGenerateAndHistoryReplay|TestHistoryRetainsProtectedIntentAndCloseMatchExplanation' -count=1 -timeout=90s
```

Frontend command, from `frontend/`:

```powershell
node node_modules/vitest/vitest.mjs run src/App.test.tsx src/components/useProgress.test.tsx -t 'cancels submitted generation|keeps candidate progress|unwraps progress'
```

Defect-reproduction commands, from the repository root, with workspace-local
build cache:

```powershell
$env:GOCACHE = Join-Path $PWD '.tmp\discovery-investigation\go-cache'
go test -overlay .tmp/discovery-investigation/overlay.json ./internal/audio -run '^TestDiscoveryInvestigationCanceledCLAPMutexWait$' -count=1 -timeout=20s
go test -overlay .tmp/discovery-investigation/overlay.json ./internal/reco/multichannel -run '^TestDiscoveryInvestigationStrictInstrumentalCachedSeeds$' -count=1 -timeout=20s
```

Implementation should first record the reporter's installed app/model versions
and capture the native stage trace for the 300-second symptom. Current source
and passing mock UI tests do not explain why their status was completely blank.

Follow-up artist-only checks:

```powershell
$env:GOCACHE = Join-Path $PWD '.tmp/go-cache'
go run ./.tmp/artist-only-investigation
go test ./internal/intent/rules ./internal/intent/lexicon ./internal/intent/schema ./internal/reco/multichannel ./internal/reco/deejai -run 'Test(ArtistOnlyContractAcrossParsers|ArtistOnlyRequiresAttachedOutputRestriction|ArtistOnlyShieldsGenreLikeArtistNames|CompilerPreservesMeasuredRequestSemantics|ExplicitArtistOnlyProducesTenTracksWithoutOtherArtists|EngineOnlyRejects.*)$' -count=1 -timeout=60s
go test ./internal/reco/deejai -run '^TestEngineOnlyBlocksUnsupportedConstraintsAndMissingSeeds$' -count=1 -timeout=30s
```

The offline probe reproduced the missing constraint and count-placement variants.
Selected existing tests passed; the rules package had no tests matching that
filter. Those tests cover recognized long forms and correctly constructed intents,
so they do not establish correct parsing of the newly reported short form.
