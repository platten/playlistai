# Enhanced audio implementation and review gates

Base: `main`, `c2b0ea3326e015298587d7ef3027193635c0472a` (2026-09-11).
The initial worktree was clean. Baseline `go test ./...` passed on Windows.

Each row below is a separate PR. A dependent milestone starts only after its
predecessor has an approving review and is merged into `main`. Do not self-approve,
auto-merge, combine milestones, or publish a release to bypass that gate. Each PR
must record its actual base, checks, limitations, and the preceding merged PR.

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
The next dependent implementation is M2, after M1 approval and merge.
