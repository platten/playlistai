# Enhanced Hybrid source translation and matching

Implemented 2026-09-12 on `codex/enhanced-audio-remaining`, as part of consolidated
draft PR #24. This follows the [investigation and remediation plan](enhanced-translation-remediation-plan.md).
The ten development prompts cover classical, Aerosmith, Christian Löffler/Kiasmos,
workouts and a Nine Inch Nails-to-Marilyn Manson journey. They are regression
inputs, not a held-out musical-quality benchmark.

The subsequent [music-context and compositional pre-parser implementation](music-context-implementation-results.md)
adds source-attributed retrieval seeds and further extraction regressions. The
measurements below describe the earlier translation implementation; the follow-up
report records the newer parser and recommendation versions separately.

## Resulting behavior

Explicit wording is extracted before the local language model and reconciled
with its response before validation. Count, time units, artist references,
required recordings, endpoints, negative instructions and reviewed musical
concepts retain source evidence. The model can interpret remaining language;
its suggestions cannot overwrite an independently extracted instruction or
invent mandatory genres from an artist reference.

For example, “mostly instrumental” remains a preference, “no screaming” excludes
that vocal subtype rather than all singing, and “30-minute” becomes a duration
request rather than 30 tracks. “Dramatic 20th-century classical” separates mood,
composition period and genre. An Aerosmith similarity reference can coexist
with an exclusion of Aerosmith's recordings. Conservative spelling resolution
finds Christian Löffler for “christrian loeffler” only when catalog identity is
unambiguous.

Enhanced Hybrid prefers strong matches, then admits close suggestions with
explicit evidence gaps. Unknown evidence cannot establish a strict exclusion
or requirement. A close suggestion needs a relevant requested comparison or
affirmative requested metadata; arbitrary catalog entries cannot fill the list.
CLAP cosine scores, MERT proximity, DSP features, listening taste and retrieval
frequency do not independently certify all requested characteristics.

Artist journeys reserve an actual recording by the starting and ending artists.
These recordings count toward the requested total. Recording deduplication,
explicit adjacent-artist restrictions, stage constraints and exclusions still
apply. Required endpoints may constrain the order of otherwise strong-first
selection. If eligible endpoints cannot be established, the result reports the
shortfall instead of substituting a merely similar artist.

## Implementation and compatibility

| Component | Contract |
| --- | --- |
| Source extractor | `source-atoms/v1`; original UTF-8 source spans, facet, polarity, strength, degree, scope and OR group |
| Embedded dictionary | `music-concepts/v1`, 117 reviewed concepts; exact aliases separate from directional taxonomy parents |
| Intent | Version 9; translation snapshot, explicit start, duration seconds and plural vocal preferences |
| Local parser | `llama/v14`; preserved source facts plus targeted prompt instructions and bounded repair |
| Recommendation | `multichannel/v24`; strong/close evidence tiers, actual endpoints and requested-comparison eligibility |
| Provider queries | Typed CLAP clauses; approved MusicBrainz search spellings; exact AcousticBrainz classifier/class mappings |

The original verbose system prompt remains in place with targeted changes. The
shorter prompt tested during investigation performed worse and was not adopted.
Managed native requests budget output against the configured context using the
runtime's chat template and tokenizer. A bounded conservative UTF-8-byte fallback
is used if those endpoints are unavailable. Oversized requests fail explicitly
rather than silently dropping source facts. Auxiliary proposals use the same
context protection.

The dictionary is an authored translation table, not a trained classifier.
MusicBrainz queries discover candidates; they are not recording-level proof.
AcousticBrainz mappings require the actual named classifier and applicable
output class. Related electronic subgenres are not treated as exact synonyms.
MERT consumes audio and contributes compatible audio similarity; it has no
invented text, mood or genre API. Negative CLAP requests use positive trait
captions with typed scoring direction, not ambiguous negated captions.

Multiple vocal preferences retain their own scope and degree. The legacy
singleton remains readable, with the plural field taking precedence when
present. Old saved intents are normalized without re-extracting the original
prompt. Translation evidence, seeds, endpoint selections and fit labels survive
generation/history round trips. New fields are additive; existing embedding
files remain reusable. Existing Deej-AI-only, AcousticBrainz-first and CLAP-first
selection policies remain distinct.

The shared analysis deadline begins when analysis is first needed, not during
metadata discovery. Required CLAP text/vocal assessments complete before
optional DSP/MERT processing; completed CLAP results survive optional failures
or deadlines. Cache-only refresh never downloads or analyzes a missing preview.
Authorized acquisition retains one fetch/decode where possible and cleans up
encoded audio and PCM immediately.

The dictionary and all runtime translation code compile in Go on supported
release targets. Python is optional offline contributor tooling only. Native
model packs and their existing licenses remain separate release assets; this
change does not introduce new model weights or change those license terms.

## Reproducing interpretation checks

The stronger fixture is
`internal/evaluation/testdata/enhanced-translation-v1.json`. The investigation's
narrow fixture remains unchanged in `docs/data/enhanced-translation-prompts-v1.json`.
Checks inspect the fields consumed by providers, not just the presence of a
source atom. Unit tests also cover unknown phrases, corrupted model fields,
quoted titles, scope/OR combinations, strict evidence gaps, required endpoints,
spelling ambiguity, cache reuse and legacy history.

```powershell
go build -o bin/musiccheck.exe ./cmd/musiccheck
bin/musiccheck.exe -parse-only -server-url http://127.0.0.1:PORT `
  -context-size 4096 -catalog "$env:APPDATA/playlist-ai/catalog" `
  -prompts internal/evaluation/testdata/enhanced-translation-v1.json `
  -output bin/translation-parser.json
bin/musiccheck.exe -parse-only -rules-parser `
  -catalog "$env:APPDATA/playlist-ai/catalog" `
  -prompts internal/evaluation/testdata/enhanced-translation-v1.json `
  -output bin/translation-rules.json
```

Use the actual port of an owned native server configured with the stated context.
`-diagnostics bin/private-diagnostics.json` explicitly opts into local raw
diagnostics; do not commit or attach private prompt/provider reports. For the
pre-existing twelve-prompt regression, substitute
`internal/evaluation/testdata/music-prompts-v8.json`.

## Data retained for future use

The final dictionary source, canonical JSON, reproducible gzip and manifest are
under `C:/Users/pawel/Downloads/playlistai-intent-dictionary-v1`. Its 117 concepts
use 34,056 canonical JSON bytes and 3,284 gzip bytes. Canonical SHA-256 is
`0aa6f18252f684ed5a87cbca6e56511b31940e64e3e0a1237c95d84852fa951d`;
gzip SHA-256 is
`16d93f770eb147bf36661e266aff26c811704861ba08183291e77591a687af1a`.
The [Python preparation guide](intent-dictionary-preparation.md) explains
validation, exact source preservation, compression, provenance and extension.
No additional external dataset is needed for the authored dictionary.

A fixed 24-recording cohort was selected from the local catalog before query
scores: three representatives each for Aerosmith, Nine Inch Nails, Marilyn
Manson, Christian Löffler, Kiasmos, Arvo Pärt, Ludovico Einaudi and Daft Punk.
Authorized identity-matched Deezer preview acquisition produced 15 complete
CLAP/DSP/MERT analyses; nine were unavailable. Acquisition took 158,072 ms.
The native offline evaluation passed with 15 comparable recordings and zero
held-out pairs. This verifies acquisition/compatibility, not recommendation
superiority. No preview audio or PCM was retained.

Selection, acquisition, derived SQLite, frozen evidence and evaluation manifests
are under
`C:/Users/pawel/Downloads/playlistai-enhanced-audio/evaluation/translation-20260912`.
Live recommendation reports and a separate derived cache are under the sibling
`translation-live-20260912` directory. Existing downloaded models, original
archives and five prepared OS/architecture packs remain in
`C:/Users/pawel/Downloads/playlistai-enhanced-audio`.

## Interpretation measurements

Native measurements used Windows amd64, Intel Core Ultra 9 285H, Go 1.27.0,
Qwen2.5-3B-Instruct Q4_K_M, temperature zero and a 4,096-token context on the
shared development machine. Catalog: `1:956917:1788613313`, 956,917 tracks.
Model SHA-256:
`9c9f56a391a3abbd5b89d0245bf6106081bcc3173119d4229235dd9d23253f94`.
Native runtime SHA-256:
`57c293efc45195604f35fc3bef60df6fb917fa542f9d8dd0db9bbcb2f86a0802`.

Two final native runs passed all ten expanded interpretation checks:
**10/10 and 10/10**, taking 47,058 and 46,669 ms respectively. They used the
117-concept registry, plural-vocal contract, qualified required-track omission
protection and temporal-span ownership. The deterministic fallback also passed
10/10 expanded checks. The original twelve-prompt fixture passed **12/12 twice**,
taking 31,632 and 32,077 ms, including the previously failing dubstep exclusions.

The final Windows `scripts/test.ps1 -HostCoverage` gate passed: full race-enabled
Go tests with atomic coverage, vet, lint (zero issues), generated Wails bindings,
158 frontend tests, typecheck, production build, pure-Go compilation and installer
helper regressions. Edge fixture checks passed generation, replay, cancellation,
stale events, previews, settings and error/partial states in dark/light themes,
narrow/wide windows and reduced motion. They also assert visible strong/close
labels, scoped vocal preferences and OR alternatives. These are browser fixture
checks, not a native GUI/provider end-to-end run.

Independent integration review found and corrected three issues: canonical
artist exclusions after spelling resolution, preservation of initial taste
centroids during cache refresh, and visible OR semantics in the intent preview.
Focused regressions cover each. The initial gate failed lint; the formatting,
unused declaration and equivalent-condition issues were fixed before the passing
full gate. Hosted checks must be assessed on the published commit separately.

Published implementation `c671fdb` passed every hosted job in
[CI run 34702827198](https://github.com/platten/playlistai/actions/runs/34702827198):
Linux lint/race tests, macOS and Linux amd64/arm64 builds/coverage, and Windows
contributor gate, amd64/arm64 installers and coverage. The following documentation
update records these results without changing the tested application source.

The previous native parser passed only 2/10 and 3/10 of the narrower checks;
manual full-meaning review found 0/10 fully faithful in each baseline run.
Different rubrics must not be treated as a directly measured numerical quality
gain. Failed development iterations and the failed shorter-prompt ablation are
retained in ignored local artifacts. The new ten prompts remain development
data after using their failures to improve the implementation.

## Ten-prompt recommendation checks

All ten interpreted requests were replayed through Enhanced Hybrid. The frozen
run used published source `c671fdb`, an isolated SQLite backup of the acquired
CLAP/metadata cache and the fixed 15-recording DSP/MERT snapshot. It made no new
preview downloads. The live development run allowed catalog-matched preview
acquisition and metadata requests; its binary preceded the final exclusion,
feedback-refresh and preview review fixes. The published-source journey check
separately verified actual endpoints and spacing. Exact run/binary/report hashes
and case results are in
[the compact measurement record](data/enhanced-translation-implementation-results-v1.json).

| Request | Requested | Frozen output | Live output | Remaining limit |
| --- | ---: | ---: | ---: | --- |
| Relaxing classical, piano/strings, no singing | 15 | 4 | 12 | Search/evidence budget; instrumentation and mood not all verified |
| Dramatic 20th-century classical, no film scores | 12 | 0 | 0 | Strict film-score exclusion unsupported |
| Aerosmith-like workout, excluding Aerosmith | 15 | 15 | 15 | Full count; sound/mood not independently verified |
| Early Aerosmith, bluesy hard rock, raw feel | 12 | 12 | 12 | Relative artist era and some audible traits unverified |
| Relaxing electronic, misspelled Löffler | 20 | 20 | 20 | Full count; requested traits not all verified |
| Löffler/Kiasmos, mostly instrumental | 15 | 15 | 15 | Full count; soft vocal/mood evidence incomplete |
| Lifting weights, no slow ballads | 20 | 0 | 0 | Strict slow-ballad exclusion unsupported |
| Running arc, no harsh vocals | 30 minutes | 0 | 0 | Strict vocal subtype unsupported; duration/energy not enforced |
| Actual NIN → actual Marilyn Manson | 15 | 15 | 15 | Endpoints/count/spacing pass; transition quality unverified |
| Industrial rock, no Manson or screaming | 15 | 0 | 0 | Strict screaming exclusion unsupported |

Both commands therefore exited nonzero: **6/10 minimum-output/contract checks
passed, five full counts, one partial count, four unsupported requests**. The
test's default minimum is one track; a minimum-output pass does not mean the
requested count or musical quality was fulfilled. No acceptance assertion was
relaxed to hide these failures. All 81 frozen and 89 live returned tracks were
labeled **close**, with zero strong matches. The Aerosmith exclusion and actual
journey endpoints passed their explicit checks. The separate published-source
journey contained 15 tracks, began with NIN's “Suck,” ended with Manson's
“Putting Holes In Happiness,” and had no adjacent repeated artists.

The live classical case took 930,372 ms, including approximately 30 seconds of
resolution and the 15-minute search budget. It recorded 226 new-analysis
attempts, 81 cache hits and 52,781,512 transient fetched bytes; attempts do not
mean that all recordings produced usable analyses. The resulting coverage was
retained as derived data only. Other successful live cases used the cache and
took 0.735–13.141 seconds. The frozen ten-case run took 6.169 seconds and fetched
zero bytes. These shared-host, cache-dependent observations are not general
latency guarantees or a paired before/after quality comparison.

The live CLI exercises CLAP and metadata. The frozen run also supplies the
compatible DSP/MERT snapshot, but this does not establish that selected tracks
have useful MERT coverage. Native fixed-cohort evaluation and service regressions
establish that those paths work; listening benefit remains unmeasured.

To repeat against a chosen cache and previously saved parser report:

```powershell
bin/musiccheck.exe -mode enhanced_hybrid -online `
  -catalog "$env:APPDATA/playlist-ai/catalog" -bundle CLAP_BUNDLE_DIRECTORY `
  -analysis-dir DERIVED_CACHE_DIRECTORY -cache METADATA_SQLITE_PATH `
  -prompts internal/evaluation/testdata/enhanced-translation-v1.json `
  -replay bin/translation-parser.json -replay-parsed `
  -output bin/translation-live.json
```

Supply actual existing paths. This command opts into identity-matched Deezer
previews; their audio is transient. For the frozen comparison omit `-online`,
add `-cached-audio-only -enhanced-evidence FROZEN_ENHANCED_EVIDENCE_JSON`, and use
isolated metadata/derived-cache copies. Preserve the original source/cache and
record hashes before each run. The retained frozen run is under
`C:/Users/pawel/Downloads/playlistai-enhanced-audio/evaluation/translation-frozen-final-20260912`.

## Remaining limits

Duration targeting, numeric energy trajectories and relative artist career
eras are preserved but not enforced as verified fulfillment. Specific vocal
subtypes need applicable evidence; an absent calibration cannot prove “no harsh
vocals” or “no screaming.” Composition dates and recording-level labels can
remain unavailable. Unsupervised audio similarity does not supply these facts.

The sparse fixed cohort has little overlap with some retrieved candidate pools.
Three cache-only exploratory replays returned zero tracks with zero cache hits.
Three separate live development checks subsequently filled 12/12, 20/20 and
15/15 tracks, all labeled close rather than strong. Those runs changed coverage,
so they are not a paired baseline comparison or a listening-quality result.
The live `musiccheck` path exercises CLAP and metadata; DSP/MERT are validated
separately by the fixed-cohort native experiment and integrated service tests.

Held-out listening comparisons, representative artist/recording splits,
per-facet calibration and a paired same-evidence ablation remain necessary
before claiming improved musical quality. The recommended music-only CLAP
bundle's previously documented discrimination concern is not resolved by this
translation work. Native inference and complete installers were not rerun on
every OS locally; hosted CI assesses compilation/packaging separately.
