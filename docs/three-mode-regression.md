# Three-mode, forty-prompt regression

Historical results. See the [September 11 development rerun](three-mode-rerun-2026-09-11.md)
for measurements after single-genre verification. Its cached-feature protocol
differs from this original live-preview run; the results are not a controlled
before/after quality or latency comparison.

Executed on September 9, 2026 (America/New_York), against the working tree based
on `d8660c16f2a863ae02bc541b62f86dac7198d839`. This is the public
[forty-prompt suite](forty-sample-prompts.md): ten requested tracks per prompt,
at least five distinct recordings, at least three artists and no adjacent artist
repeats, except the explicitly single-artist request. Interpretation and reference
assertions also contribute to a pass. These are acceptance observations, not
held-out listening judgments or proof of musical relevance.

## Results

All **120 cases completed**. Each mode exited `1` because at least one acceptance
assertion failed; results were retained rather than replaced with passing reruns.

| Mode | All assertions pass | At least five tracks | Exactly ten tracks | Reported outcomes |
| --- | ---: | ---: | ---: | --- |
| AcousticBrainz first | 38/40 | 39/40 | 39/40 | 12 fulfilled, 27 partial, 1 needs clarification |
| CLAP first | 38/40 | 39/40 | 39/40 | 12 fulfilled, 27 partial, 1 needs clarification |
| Deej-AI only | 9/40 | 9/40 | 9/40 | 9 fulfilled, 26 needs clarification, 5 unsupported |

There were no recording-duplicate or adjacent-artist assertion failures among
returned playlists. Every nonempty playlist met its artist-count requirement.
Partial results still carry incomplete musical-fit or journey-stage evidence;
passing the count gate does not establish that the requested sound was fulfilled.
The general target of five tracks for **every** example is not met by any mode.

### Failures to address

- **Case 17, spacious electronic / sparkle:** both enhanced modes returned ten
  tracks but failed `preference missing: sparkle`. The interpretation preserved
  `occasional sparkle` under positive `preferences.styles`, while the fixture
  expects it under `textureDescriptions`. This is a field-classification mismatch,
  not a lost phrase. The saved raw interpretation makes it reproducible.
- **Case 32, David Bowie → Talking Heads:** both enhanced modes returned zero
  tracks and `required_track_audio_conflict`. The raw interpretation additionally
  classified the artist names as essential journey styles, despite having no
  required output tracks. The destination recording, Talking Heads — “Walk It
  Down - 2005 Remaster” (`0x36QYsSOfHGf7MWP97aME`), lacked preview evidence and
  blocked generation. Artist-as-style interpretation and destination handling
  need investigation together; the conflict was not bypassed during this test.
- **Engine-only:** 26 cases lacked usable resolved seeds, including the explicit
  Japanese reference `宇多田ヒカル`, which remained unresolved in this catalog-only
  path. Five were unsupported: destination journeys (23, 32, 33), strict no-vocals
  (25), and the artist-only constraint (40). It passed cases 6–12, 30 and 34.
  These guarded outcomes explain the failures but do not satisfy the minimum
  playlist requirement.

There were **zero parser fallbacks in the 40 fresh parses**. The other runs reused
them, so they are not another 80 independent parsing observations. Case 17's
interpretation assertion failed without triggering a fallback.

### Timing and evidence coverage

| Mode | Sum of case times | Median | P95 (nearest rank) | Maximum |
| --- | ---: | ---: | ---: | ---: |
| AcousticBrainz first | 49.6 min | 42.3 s | 195.8 s | 709.8 s |
| CLAP first | 23.1 min | 28.3 s | 70.2 s | 342.4 s |
| Deej-AI only | 27.0 s | 0.584 s | 1.357 s | 1.997 s |

Times exclude process/model/catalog initialization. Fresh initial parsing is
included only in the first run. The strict no-vocals case (25) took 709.8 s then
342.4 s; the first run exhausted its analysis budget. The distorted-guitars/mood
case (36) took 467.0 s then 99.8 s. Both returned ten tracks with partial outcomes.
Warm caches and different workloads prevent interpreting the timing difference
as an intrinsic source-priority speedup.

Each enhanced run selected 390 tracks, of which **270 had eligible analyzed
preview evidence**. Neither run had a selected pick with an available
`acousticbrainz_intent` ranking component or a non-null raw AcousticBrainz
comparison score. This does **not** mean no archive records were fetched:
knowledge snapshots contained 22 available high-level archive record occurrences
in the first run and 71 in CLAP-first. Those are repeated snapshot occurrences,
not unique coverage; archive evidence can also affect eligibility.

Only four enhanced-mode playlists differed in ordered IDs (cases 13, 17, 20, 25);
35 nonempty playlists were identical and both modes shared the empty case 32.
With no selected-track archive comparison scores, changing evidence pools and no
listening labels, this run cannot establish a musical-quality winner between
AcousticBrainz-first and CLAP-first. Source-priority unit tests remain the evidence
for behavior when both sources actually provide scored features.

## Saved evidence and versions

- [Compact results](data/three-mode-regression-2026-09-09.json): all 120 cases,
  selected public catalog IDs, errors, outcomes, timings, evidence counts,
  snapshot IDs, hashes and environment information.
- [Shared raw parsed inputs](data/three-mode-inputs-2026-09-09.json): all 40 full
  version-8 interpretations and parser diagnostics for replay.
- Full local reports remain at `/tmp/playlist-ai-three-mode-{acousticbrainz,clap,deejai}.json`;
  their hashes are recorded in the compact report. These temporary files are not
  durable repository artifacts; bulky candidate snapshots/caches are not bundled.

The host was Linux/WSL2 with 15,781 MiB system RAM and an RTX 5060 Laptop GPU
reporting **8,151 MiB VRAM**. The model was the installed Qwen3.5-9B Q4_K_M,
with 8,192 context tokens per slot and four runtime slots. CLAP used the native
ONNX Runtime 1.26.0 CPU worker and the installed music/speech model. Catalog
version was `1:956917:1788613313` (956,917 tracks); generation versions were
`multichannel/v18+iterative/v1` and `deejai/v4+engine-only/v1`.

## Protocol

The first run used AcousticBrainz-first and fresh Qwen3.5-9B interpretations.
CLAP-first and Deej-AI-only reused those exact **raw parsed intents**, before
metadata discovery or mode-specific generation. All runs used seed `42` and an
empty taste profile. They shared the same compiled executable and catalog.
The harness now accepts `-mode`, `-server-url` and `-replay-parsed`, records the
raw interpretation and selected mode, and invokes the desktop's engine-only
wrapper for that mode. No ranking behavior or acceptance assertions were changed
to improve these measurements.

Both enhanced modes enabled local Discogs metadata, provider fallback,
AcousticBrainz lookup and the installed native CLAP worker. Engine-only used no
metadata provider, audio worker or personalization. It still received the same
parsed descriptions; this comparison does not measure a fresh engine-only parse.
The enhanced replay retained the LLM server for replacement-anchor proposals.

Existing metadata and derived-feature caches were retained. CLAP-first ran after
AcousticBrainz-first; Deej-AI-only ran concurrently with the beginning of
CLAP-first. The repository check gate overlapped the early first run. Consequently
these are **not controlled cold-cache speed benchmarks**. Reusing parsed inputs
does not freeze provider responses, preview availability, replacement proposals
or the entire candidate pool; differences cannot be attributed solely to source
priority.

## Reproduce

Build with native audio support and start the installed local model:

```sh
go build -o /tmp/playlist-ai-musiccheck ./cmd/musiccheck
/path/to/llama serve --model /path/to/qwen3.5-9b-q4km.gguf \
  --host 127.0.0.1 --port 45314 --ctx-size 8192
```

In another terminal, run each enhanced mode (set `-mode` to
`acousticbrainz_first` or `clap_first`):

```sh
/tmp/playlist-ai-musiccheck \
  -server-url http://127.0.0.1:45314 -mode acousticbrainz_first \
  -replay docs/data/three-mode-inputs-2026-09-09.json -replay-parsed \
  -prompts internal/evaluation/testdata/varied-prompts-v1.json \
  -catalog /path/to/catalog -metadata /path/to/metadata/discogs.sqlite \
  -bundle /path/to/validated/clap-bundle -online \
  -min-tracks 5 -min-artists 3 \
  -analysis-dir /path/to/evaluation-analysis \
  -cache /path/to/evaluation-metadata.sqlite \
  -output /path/to/mode-results.json
```

Omit `-replay` and `-replay-parsed` to measure fresh interpretations instead.
The original first run did this. Run the baseline without auxiliary services:

```sh
/tmp/playlist-ai-musiccheck -mode deejai_only \
  -replay docs/data/three-mode-inputs-2026-09-09.json -replay-parsed \
  -prompts internal/evaluation/testdata/varied-prompts-v1.json \
  -catalog /path/to/catalog -min-tracks 5 -min-artists 3 \
  -output /path/to/deejai-results.json
```

The harness writes observations incrementally and exits nonzero if any case
fails. Do not interpret that exit as missing results or remove strict requirements
to force a passing count. Only derived preview features are retained; audio is
not saved in these report artifacts.

## Validation

`go test ./cmd/musiccheck`, the native runner build and the complete
`scripts/test.sh` gate passed: binding generation, frontend typecheck/build,
`go vet`, pure-Go core checks, race-enabled Go tests and zero lint issues.
Those deterministic/build checks are separate from the live acceptance failures.
No rendered desktop UI test or held-out listening evaluation was performed.
