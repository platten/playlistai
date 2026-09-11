# Three-mode replay after single-genre verification

Executed September 11, 2026 against the modified working tree based on
`b79c0317a92fa8d5d3cfc8ecb202300c05a85d40`. This is a **bounded cached-feature
replay**, not a fresh LLM/preview benchmark or a held-out musical-quality test.
It must not replace the separately labeled [September 9 live run](three-mode-regression.md).

## Protocol

The public forty-prompt suite requests ten tracks, requires at least five distinct
recordings and three artists (except the explicit single-artist case), and checks
intent, exclusions, destination, and adjacent-artist behavior. Seed is `42`, with
an empty taste profile. All modes replay the exact September 9 raw parsed intents;
there is no new parsing, fallback-rate measurement, or replacement-anchor LLM.

The cgo-enabled runner used the installed 956,917-track catalog, local Discogs
SQLite, and provider fallback. Existing metadata and audio stores were copied with
SQLite backup into an isolated temporary directory; real user stores were not
modified. Enhanced modes loaded the installed **CLAP Music full-precision** bundle
and passed native-worker health, but `-cached-audio-only` prohibited new preview
downloads. Missing or model-incompatible derived features remained unavailable.
Thus this run cannot measure fresh CLAP analysis quality or compare the two
evidence-priority policies under complete coverage.

Each enhanced mode initially had a 160-second wall budget, then resumed
unattempted cases with a 195-second budget. Later batches retried interrupted
cases and continued remaining cases; all interrupted observations were retained
separately, never counted as completed results or included in latency summaries.
The slow punk request was deferred so it would not prevent later cases running.
Engine-only had a 100-second budget. Final batches were serialized to avoid
concurrent provider clients; earlier overlapping stages remain a limitation.
The AcousticBrainz retry had a 220-second budget but was manually stopped after
its repeated punk stall; its remaining tail had 190 seconds. CLAP's final tail had
160 seconds and used a SQLite backup of the warmed AcousticBrainz benchmark
metadata cache. The final Radiohead-only AcousticBrainz case ran separately with
a 20-second budget and finished in 454 ms. No further cases were retried.
Mode runs overlapped each other and repository checks: observed latencies are not
controlled speed comparisons. Times exclude process, catalog, and model startup.

## Results

**105 of 120 mode/cases completed**. Four remained interrupted and eleven were
unattempted. The [compact JSON](data/three-mode-regression-2026-09-11.json)
retains all observations, including seven interrupted attempts: two recovered
on retry, one repeated interruption of the same unresolved punk case, and the
four unresolved mode/cases. Completed-result denominators exclude interruptions.

| Mode | Completed | Interrupted | Unattempted | All assertions / at least five / exactly ten |
| --- | ---: | ---: | ---: | ---: |
| AcousticBrainz first | 38 | 2 | 0 | 11 / 38 for each |
| CLAP first | 27 | 2 | 11 | 7 / 27 for each |
| Deej-AI only | 40 | 0 | 0 | 9 / 40 for each |

AcousticBrainz-first reported 11 fulfilled, 24 partial and three clarification
outcomes; CLAP-first seven fulfilled, 19 partial and one clarification; engine-only
nine fulfilled, 13 clarification and 18 unsupported. None met the original
five-tracks-for-every-prompt target. No completed result triggered recording-
duplicate, adjacent-artist or excluded-artist assertions. Four one-track results
failed artist diversity; empty results also failed count and diversity gates.
The existing `sparkle` field-classification assertion remained in all three modes.

Both enhanced modes returned **zero tracks for “Classical, 10 tracks.” and
“Electronic music. Make a 10-song playlist.”**, with
`single_genre_evidence_exhausted`, rather than returning an unrelated full list.
This demonstrates honest handling of this limited evidence pool, not that a
properly covered live run cannot fulfill those genres.

| Mode | Completed-case time sum | Median | P95, nearest rank | Selected tracks with analyzed preview evidence |
| --- | ---: | ---: | ---: | ---: |
| AcousticBrainz first | 458.734 s | 10.703 s | 31.256 s | 2 / 112 |
| CLAP first | 256.419 s | 2.665 s | 30.466 s | 2 / 72 |
| Deej-AI only | 28.736 s | 0.6245 s | 1.497 s | 0 / 90, disabled |

Enhanced modes had nine and eight derived-feature cache hits respectively;
neither selected any pick with an available `acousticbrainz_intent` scoring
component. Preview bytes fetched were zero. These figures do not establish that
no archive metadata was consulted or that CLAP-first is faster: case sets,
metadata-cache warmth, interruptions and concurrent workloads differed.

### Performance blocker discovered

“Punk rock, 10 songs.” repeatedly stalled at “Finding starting points.” A native
Go stack captured while terminating the bounded retry showed
`candidateStream.nextMusicBrainz → addKnowledgeRecording → ResolveReference →
resolveTrack → trackSearchRows → scanTracks → normalizeUnicodeSearch`. This
identifies a catalog-resolution scan on the provider-candidate path, not merely
network waiting. The retry was still CPU-active after cancellation; one CLAP-mode
stage required the timeout's forced-kill grace. A single captured stack is not a
full profiler attribution, but it establishes a concrete follow-up for bounded,
cancellation-aware resolution. No production fix was mixed into this benchmark.

The final CLAP “drum and bass” attempt reached “Checking candidates for the final
selection” but was canceled after 140.203 seconds of case time when the process
budget expired. That timing is not a completed-generation latency. The remaining
AcousticBrainz interruption was the blues → soul → funk journey. CLAP did not
attempt cases 30–40 in its final stage; Radiohead-only was completed separately
only for AcousticBrainz-first.

The Japanese artist example also exposed a resolution limitation: Deezer returned
an ambiguous artist lookup, followed by a metadata time limit after checking
seven MusicBrainz recordings. The result stayed empty. Two retained metadata
notices reported **MusicBrainz HTTP 503** during instrumental searches; no HTTP
429 was observed. Provider notices are included in the compact report rather
than attributing all delays to rate limits.

## Reproduction

Build the current runner, then run each mode using the same saved interpretations:

```sh
CGO_ENABLED=1 go build -o /tmp/musiccheck ./cmd/musiccheck
# Create temporary directories and SQLite backup copies; do not reset real data.
/tmp/musiccheck \
  -catalog "$BENCH_CATALOG" \
  -replay docs/data/three-mode-inputs-2026-09-09.json -replay-parsed \
  -prompts internal/evaluation/testdata/varied-prompts-v1.json \
  -count 10 -min-tracks 5 -min-artists 3 \
  -mode acousticbrainz_first -online -metadata "$BENCH_METADATA" \
  -cache "$BENCH_TEMP/metadata.sqlite" \
  -bundle "$BENCH_BUNDLE" -analysis-dir "$BENCH_TEMP" -cached-audio-only \
  -output "$BENCH_TEMP/acousticbrainz.json"
```

Repeat with `-mode clap_first` and a separate metadata-cache backup. For
`-mode deejai_only`, omit online, metadata, cache, bundle, analysis-directory and
cached-audio flags. The CLI exits `1` for acceptance failures and retains results;
GNU `timeout --signal=INT --kill-after=10s 160s` produced `124` for bounded stages.
Resumed stages used a temporary fixture containing only unattempted original cases.

Host: Linux/WSL2, Intel Core Ultra 9 285H, 16 logical CPUs, 15,781 MiB RAM,
RTX 5060 Laptop reporting 8,151 MiB VRAM; Go 1.27.1. CLAP was the native
ONNX Runtime 1.26.0 **CPU** implementation, not GPU inference. Model revision,
weights, binary hash, source/input versions and per-case evidence are retained in
[compact results](data/three-mode-regression-2026-09-11.json).

## Repository validation

The coordinating validation run of `scripts/test.sh` passed: Go vet, race-enabled
Go tests, lint, generated bindings, frontend typechecking, all **150 frontend
tests**, and the production build. JSON denominator/record checks and
`git diff --check` also passed. These automated checks are distinct from the
acceptance failures, interrupted runs, provider errors and missing evidence above;
passing the repository gate does not make this a passing forty-prompt benchmark.

## Interpretation and remaining work

Single-genre requests now exclude tracks with unknown or mismatching evidence.
An empty or shorter result is an evidence-coverage failure, not permission to
substitute an unrelated playlist. Engine-only explicitly cannot verify a genre.
The five-track requirement remains an acceptance target, not a reason to waive
genre eligibility. Full output counts and reported fulfillment are not independent
musical judgments.

This replay has no held-out listening labels, new parse accuracy measurements,
or complete track-level genre ground truth. A fresh preview
run with sufficient compatible cache coverage and a reviewed listening set is
still needed before claiming improved musical quality or a winning source priority.
