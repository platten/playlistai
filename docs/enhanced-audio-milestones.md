# Enhanced audio implementation and review gates

Base: `main`, `c2b0ea3326e015298587d7ef3027193635c0472a` (2026-09-11).
The initial worktree was clean. Baseline `go test ./...` passed on Windows.

M1 and M2 were reviewed and merged separately. At the user's request, all
remaining milestones are tracked in one consolidated PR, based on M2's merge
`bc9d6f23a7da22be9eb22ee951ffeb349e899774`. That PR requires user approval and
merge; do not self-approve, auto-merge or publish a release. Milestone boundaries
below remain implementation and validation checkpoints inside that PR.

The remaining-work branch is `codex/enhanced-audio-remaining`; it started clean
from `origin/main`. Baseline `go test ./...` passed on Windows. The implementation
extends the existing representation/DSP stores, verified optional asset installer,
native ONNX worker, request-local assembly fingerprint and settings controls.
CLAP's paired encoder contract and all three existing modes remain unchanged.
MERT uses a separate audio-only model identity and optional installation; its
assets retain upstream licensing. Enhanced evidence is frozen before ranking,
serialized for replay, and never used to satisfy hard criteria through proxies.
No further analysis database is introduced.

| Milestone | Deliverable | Acceptance boundary |
| --- | --- | --- |
| M1 | Audio-only representation contracts/cache; pinned source acquisition instructions | Additive database migration, strict identity/coverage validation, CLAP isolation, verified retained source assets |
| M2 | Original-channel decode branch, deterministic Go DSP and cache | Synthetic signal regressions, finite values, missingness, unchanged CLAP samples |
| M3 | Pinned MERT export, reference fixtures, native ONNX worker and model installation | Observed end-to-end parity, cancellation/resource tests, separate model license, platform package checks |
| M4 | Enhanced Hybrid mode, intent mapping, bounded analysis snapshot, ranking and taste | Existing modes unchanged; missing evidence neutral; DSP and MERT change only supported soft preferences |
| M5 | MERT transition sequencing | Bounded continuity influence; journey/required tracks, spacing and deterministic replay preserved |
| M6 | Settings and evidence UI | Install/remove/clear/analyze/cancel states; measured/predicted/proxy/unknown labels; both themes and interaction tests |
| M7 | Unsupervised evaluation and release validation | Bounded preview evaluation, separate held-out adjacency evaluation if usable data exists, measured performance and native OS package evidence |
| M8 (optional) | AcousticBrainz bulk importer | Only after usable exact recording mappings are demonstrated; streamed, resumable, provenance-preserving import |

The optional supervised attribute heads from phase 4 are deferred: this delivery
uses deterministic measurements and pretrained self-supervised representations,
without fitting a new model or collecting manual labels. Music-JEPA is excluded.
Cross-cutting testing, documentation and privacy requirements apply to every PR.

## Inspection and extension points

`ports.AudioAnalyzer` is CLAP's paired text/audio contract. `audio.Service` owns
authorized preview fetching, cleanup and analysis; `audio.Store` persists derived
results in `audio-analysis.sqlite`. `audio.BundleManifest`, `dataset.Download`,
and the compiled `audioruntime` child process already provide verified asset and
native ONNX infrastructure. The existing bundle validator deliberately requires
CLAP preprocessing and paired encoders; MERT must not loosen that validator.

`DecodeMP3` currently combines stereo channels and converts directly to CLAP's
48 kHz input. DSP must branch before this conversion to retain channel power.
MERT needs its own anti-aliased resampling and 24 kHz normalization. M2 must
compare the legacy CLAP path numerically before sharing decode work.

The multichannel ranker has request-wide availability, the sequencer preserves
waypoints and spacing, and `assemblyKey` fingerprints request-local assessments.
Enhanced evidence must join this snapshot/fingerprint boundary before ranking.
Taste currently contains catalog-space vectors; new model-space centroids must
remain separate. AcousticBrainz already projects recording measurements through
MusicBrainz enrichment. Settings use `MusicAnalysisCard` and
`RecommendationSettings`; playlist details expose existing evidence.

## M1 contracts and migration

Add `AudioRepresentationIdentity`, segment and representation records, plus an
audio-only analyzer port. A representation-store adapter shares the existing
SQLite connection and lifecycle, with its own additive table and lookup index.
It never rewrites CLAP rows, paired-model identities or saved generation data.
No existing recommendation or UI path consumes these records in M1.

The complete model identity includes revision, preprocessing, runtime, dimension,
weight SHA-256 and pooling policy. Persisted records also fingerprint catalog and
recording keys, resolved preview identity, audio hash, observed coverage, vectors
and analysis time. Missing rows are explicit misses; legitimate zero vector
coordinates remain zero. Entire zero-length/zero-norm representations are invalid.
Coverage describes observed segments only, never padding or inferred track time.

## Data and packaging requirements

Retain original required downloads and checksum/provenance inventories under
`C:\Users\pawel\Downloads\playlistai-enhanced-audio`. Derived runtime assets go
in separate output directories; compression must not overwrite source assets.
Use the existing catalog and pretrained MERT; no training corpus is needed.
Do not download metadata-only audio corpora in anticipation of unavailable PCM.
Any future audio evaluation cohort must have available source audio or an exact,
verified identity with an obtainable authorized Deezer preview (up to 30 seconds).
Preview bytes and PCM remain transient; only intended derived results persist.

All shipped execution must be Go/native ONNX on Windows, macOS and Linux, with
the correct architecture and required native libraries in the distribution or
verified optional asset pack. Python is solely a maintainer preparation tool.
MERT weights stay separately licensed optional assets; GPL-3.0 stays unchanged.
The existing application release allowlist currently excludes model/catalog
archives; M3/M7 must explicitly test and document any distribution layout changes.
Cross-compilation is not a substitute for native inference/package testing.

MERT does not understand the text prompt directly. Preview measurements describe
the analyzed preview, not necessarily the complete recording.

## M1 delivery evidence

Source acquisition ran on Windows with Python 3.13.14. All seven pinned assets
(597,204,046 bytes total) were downloaded and then verified again without network
access. The source inventory remains beside the downloads, outside Git. See
[preparation commands and provenance](enhanced-audio-data-preparation.md).

`go test ./internal/audio ./internal/core ./internal/ports` passed. The new offline
downloader tests (`python -m unittest discover -s python -p
'test_fetch_enhanced_audio.py'`) passed all seven cases. An independent read-only
Go review found no actionable defects and reran the targeted package checks.
The review did not exercise concurrent clear/write or mid-operation cancellation;
the deterministic cancellation regressions use already-canceled contexts.

The first Windows gate attempt had CGO disabled: race testing could not start,
and lint reported non-CGO-only static checks plus existing CRLF formatting
issues. `go fmt ./...` normalized local line endings without changing tracked
content outside M1. The verified LLVM-MinGW compiler was already installed. Rerun the native
gate with the existing compiler (no persistent environment change required):

```powershell
$env:CC = & .\scripts\install-clap-toolchain.ps1 -CheckOnly
$env:CGO_ENABLED = '1'
.\scripts\test.ps1
```

The final native Windows gate passed: PowerShell/installer regressions, Wails
binding generation, frontend typecheck, 150 frontend tests, production build,
`go vet`, pure-Go core compilation, `go test -race -count=1 ./...`, and
`golangci-lint` (zero issues). `git diff --check` also passed. Hosted CI is
reported separately in the PR. Independent review of the downloader found no
actionable defects and reran all seven offline tests successfully.

No MERT ONNX graph, MERT parity result, runtime install command, music-quality
evaluation or performance benchmark is claimed in M1. No macOS/Linux package
execution was performed locally. Existing opt-in real-provider/model tests need
their documented environment/assets and are not evidence of MERT validation.
M1 was merged as [PR #22](https://github.com/platten/playlistai/pull/22), commit
`bf915c4aa8fab35e2838c785770753c6f01b297c`, after the user reported approval/merge.

## M2 implementation note and delivery evidence

Base: `origin/main` at `bf915c4aa8fab35e2838c785770753c6f01b297c`;
branch `codex/enhanced-audio-m2-dsp`, initially clean. Baseline `go test ./...`
passed. The existing decoder, CLAP preprocessing arithmetic, analysis store,
preview authorization, interval sampling and service were inspected before edits.

The original int16 decoder is shared. Legacy `DecodeMP3` preserves its existing
mono/linear-resampling/quantization arithmetic; a separate original-channel
float32 branch retains power for DSP. `Service.DSPStore` is opt-in and defaults
to nil; no app composition, recommendation mode, scoring or UI behavior changes.
When enabled with CLAP, a single fetch/decode supplies both paths and all PCM is
cleared after use. Explicit `AnalyzeDSPPreview` works without an installed model
and reuses cache before provider resolution. Neither path persists audio.

The ten [versioned measurements](dsp-measurements.md) distinguish finite known
values from unknown values with reasons. `DSPAnalysis` records source format,
verified preview identity/hash, version and observed source-frame coverage.
The additive `dsp_analysis` table/index shares `audio-analysis.sqlite`; its
find/put/usage/clear adapter is isolated from CLAP and audio representations.
Changing DSP extraction, decoding or sampling invalidates DSP independently.
CLAP/MERT model and ranking changes do not enter the DSP version.

The shared service rounds selected CLAP-frame interval boundaries inward to whole
original frames. Cached DSP retains its own coverage even if a later CLAP call
chooses another interval. Once preview bytes have been fetched for CLAP, matching
DSP version, complete preview identity and audio hash reuse the existing DSP row.
Changed audio/identity causes new measurement; no timestamp-only duplicate is
written on a compatible CLAP reanalysis. This policy was tightened after an
independent review finding and regression-tested.

Focused checks passed: audio/core/ports package tests, targeted race tests, and
CLAP comparison against frozen pre-M2 arithmetic across nine source rates and
edge/random int16 PCM. Regressions cover original-channel anti-phase power,
single-fetch combined analysis, model-free cached DSP, legacy database migration,
all cache identity fields, corruption, zero/missing distinction, clear isolation,
and cancellation while waiting for the sole database connection. Synthetic
extractor tests additionally cover signal directions, silence, low sample rates,
partial durations, deterministic repetition and cancellation during FFT work.

No new dataset, model weight, Python environment, native library or application
dependency is needed for DSP. M1's retained source assets remain in Downloads.
There is no held-out musical-quality evaluation or MERT inference claim in M2.
At the M2 boundary DSP was not exposed through the UI. M2 has since been approved
and merged as PR #23; the consolidated implementation below connects generation
and user controls.

The final Windows gate (`scripts/test.ps1`, CGO enabled with the existing verified
LLVM-MinGW compiler) passed: Wails bindings, frontend typecheck, 150 frontend
tests, production build, vet, pure-Go core compilation, race-enabled full Go suite
and lint (zero issues). Independent review verified the cache fix and found no
remaining actionable findings. Opt-in real-model/provider tests were not activated.
The following package cross-compilations also passed with `CGO_ENABLED=0`:

```text
GOOS=linux  GOARCH=amd64 go test -c ./internal/audio -o <temporary-output>
GOOS=darwin GOARCH=arm64 go test -c ./internal/audio -o <temporary-output>
```

These are compile checks, not native execution, installer inspection or full
application packaging on those hosts. Other target architectures were not
independently cross-compiled locally in this milestone.

DSP benchmark, executed 2026-09-11 on Windows/amd64, Intel Core Ultra 9 285H,
Go 1.27.0, default GOMAXPROCS 16 but one sequential extraction at a time:

```text
go test ./internal/audio -run '^$' -bench '^BenchmarkMeasureDSP$' -benchtime=3x -count=3 -benchmem
190183900 ns/op   291536 B/op   17 allocs/op
189793500 ns/op   291536 B/op   17 allocs/op
191865667 ns/op   291536 B/op   17 allocs/op
```

Input is a synthetic 30-second 48 kHz stereo, 1 kHz sine at amplitude 0.5 with
opposed channels; no random seed/model/runtime is involved. Algorithm identity is
`audio.DSPVersion` in this M2 diff. Allocation totals exclude the prebuilt input
and are not measured peak RAM. Decode, cache I/O, model inference, cold/warm start,
and full playlist performance were not measured by this isolated extraction
benchmark. These numbers are not musical-quality evidence.

## Consolidated M3–M7 delivery

The remaining implementation is based on M2 merge
`bc9d6f23a7da22be9eb22ee951ffeb349e899774`. It adds the separately identified
native MERT worker and verified pack installation, Enhanced hybrid ranking and
sequencing, bounded shared preview acquisition, explicit-feedback content
centroids, immutable saved evidence, settings/evidence UI, and offline evaluation.
The original three modes retain their separate policies. No training was run.

The exported FP32 MERT graph passed eight PyTorch/ONNX reference cases; the native
Windows worker passed reference health checks, cancellation and reload. Packs
were assembled and their binary architectures verified for Windows amd64/arm64,
Linux amd64/arm64 and macOS arm64. None contains Python. Actual native inference
was exercised on Windows amd64 and subsequently Linux amd64 in Ubuntu WSL2.
Other native inference hosts, Linux GUI behavior and complete installer execution
remain release-validation requirements.

Eight of eleven preselected catalog tracks had uniquely verified Deezer previews.
The evaluation retained derived measurements/vectors, not audio. No eligible
held-out adjacency corpus was available, so no musical-quality superiority or
learned-weight claim is made. Optional M8 remains deferred pending usable exact
recording mappings and an eligible corpus.

See the [delivery report](enhanced-audio-delivery.md),
[model preparation](mert-model-preparation.md),
[real preview evaluation](enhanced-preview-evaluation.md), and
[measured runtime results](enhanced-runtime-validation.md) for reproducible
commands, identities, validation boundaries and retained artifact locations.
