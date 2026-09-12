# Consolidated enhanced audio delivery report

Base: `bc9d6f23a7da22be9eb22ee951ffeb349e899774` (`main`, merged M2).
Branch: `codex/enhanced-audio-remaining`. M3–M7 share one PR requiring user review
and merge. All CI runners remain GitHub-hosted.

## Implementation and files

The principal additions are `internal/core/enhanced_audio.go`,
`internal/app/enhanced.go`, `internal/bridge/enhanced.go`,
`internal/taste/content.go`, the `internal/audio/mert_*` and `enhanced_*` files,
`internal/audioruntime/mert_*`, and `internal/reco/multichannel/enhanced*`.
Existing orchestration, ranking, sequencing, configuration, history and main-worker
dispatch are extended. Focused regressions live beside these implementations.
The PR diff supplies the complete file inventory.

`EnhancedAudioCard`, `EnhancedAudioEvidence`, recommendation controls, Settings,
Playlist and their tests expose the feature. `scripts/capture-enhanced-audio.mjs`
exercises rendered states. Go maintainer commands provide pack management,
native parity, bounded preview acquisition, offline comparison and prompt replay.
Python export/pack scripts and their tests are offline maintainer tools only.

DSP and MERT reuse the existing analysis database and M1/M2 additive tables;
this PR introduces no further database. CLAP's paired encoder contract remains
separate. A bounded preparation stage freezes compatible evidence before ranking.
Saved request/result DTOs include that snapshot, model/policy identity and content
centroids; generation fingerprints include the snapshot. Legacy history defaults
to no enhanced evidence. Replaying against a different catalog is rejected.

## Model, DSP and policy identities

MERT-v1-95M revision: `12af15fef9d0ac838c3f475bfbbf26d2060dd4f5`.
The FP32 ONNX graph is **377,679,573 bytes**, SHA-256
`9d06066836ae4d5001954f1689b89447c3a65119933e34c3f3a8587f9d4f442b`.
It uses opset 17, float32 `input_values[1,120000]`, int64
`attention_mask[1,120000]`, and float32 `embedding[1,768]`: 24 kHz mono,
five-second segments, observed-only normalization, masked final-layer temporal
pooling and L2 normalization. Partial segments are padded, never counted as
observed coverage. Segment outputs use observed-duration-weighted pooling.
The [preparation guide](mert-model-preparation.md) defines the exact resampler,
masking, dependencies, pooling identity and validation commands.

MERT is separately licensed **CC-BY-NC-4.0**; ONNX Runtime is MIT. The application
remains GPL-3.0. Optional packs carry upstream notices and do not expand model
rights. No model weights or audio are committed. All five release targets have
prepared native packs; the desktop needs no Python installation.

DSP algorithm identity is
`dsp-original-channelpower-hann-pow2ge100ms-hopquarter-band20to12000-rms400ms-p95p10-fluxpower-mad3-refractory100ms/v1`.
Its ten measurements and precise missingness/framing definitions are in
[DSP measurements](dsp-measurements.md). Cache identity additionally includes
original decoding and preview-sampling versions. Model, graph, preprocessing,
pooling, catalog, recording and audio identities prevent incompatible reuse.
Ranking-weight changes do not force re-inference; CLAP updates do not invalidate
DSP/MERT. Clearing enhanced derivatives preserves CLAP, models and history.

Policy `enhanced-hybrid/v1` adds bounded DSP soft preferences, compatible MERT
reference/taste affinity and MERT transition continuity. Fixed request-wide
denominators preserve missing-evidence neutrality. Unsupported attributes stay
unknown and cannot satisfy hard criteria. Explicit request feedback precedes
durable history; exposure is not preference. See [user behavior](enhanced-audio.md).

## Commands, retained assets and validation

Exact Python preparation, pack installation/removal and native parity commands
are in [model preparation](mert-model-preparation.md). Exact acquisition and
evaluation commands are in [preview evaluation](enhanced-preview-evaluation.md)
and [offline comparison](enhanced-audio-evaluation.md).

Retained root: `C:/Users/pawel/Downloads/playlistai-enhanced-audio`.
Original source assets remain intact. `derived-mert/packs-v1` holds five verified
pack directories/ZIPs, official runtime archives and provenance inventories.
Runtime archives total 201,141,124 bytes; ZIPs total 1,194,532,691 bytes.
The real eleven-track evaluation retained eight verified derived results,
downloaded 3,838,616 bytes transiently and took 81,150 ms. Three ambiguous
recordings remained unavailable. No training corpus was needed or downloaded.

Measured native parity, startup/reload, worker memory, DSP, resampling, decoding,
cache, ranking and sequencing results with hardware and exact commands are in
[runtime validation](enhanced-runtime-validation.md). The observed Windows
worker peak working set was 525,709,312 bytes; it is not an all-platform bound.
Synthetic timing fixtures and numerical parity do not establish musical quality.

The four-mode [prompt replay check](enhanced-prompt-regression.md) used a freshly
recorded built-in-rules parse because historical raw LLM intents were unavailable.
It reproduced 11/12 expectation failures in every mode; it is not a passing
musical acceptance suite. Enhanced preserved the baseline ordered output where
compatible cohort evidence did not overlap selected tracks. Separate deterministic
regressions verify active DSP/MERT ranking and MERT sequencing contributions.

## Executed checks

| Check | Result |
| --- | --- |
| `scripts/test.ps1`, Windows amd64, CGO enabled with verified LLVM-MinGW | PASS: installer tests, generated bindings, TypeScript, 154 frontend tests, production build, vet, pure-Go core compilation, full race-enabled Go suite, lint with zero issues |
| Python `test_export_mert.py` / `test_prepare_mert_packs.py` | PASS: six preprocessing and four pack-safety regressions |
| Actual PyTorch/ONNX export | PASS: eight numerical reference cases |
| Native Go MERT and preprocessing references | PASS: parity, cancellation/reload, five preprocessing cases |
| Windows desktop CGO build and actual binary worker health | PASS: version, worker capability and framed MERT health with the prepared official pack |
| Temporary native pack installation/status/removal | PASS: source assets preserved |
| Edge rendered controls | PASS: dark/light, narrow/wide, install/remove/cancel states |
| `git diff --check` | PASS |
| Four-mode rules-parser prompt expectations | FAIL: 11/12 cases in each mode; identical failing-case set, limitations above |
| Native Linux/macOS/Windows ARM inference; all-target complete installers | NOT RUN locally |
| Held-out adjacency quality | NOT RUN: no eligible held-out corpus |

The default gate skips opt-in real-model/provider tests without their environment;
the bounded native and real-preview checks above were run separately. Hosted CI
results are recorded in the PR rather than represented by local passes.

## Limitations and deferred work

Native Linux/macOS/Windows ARM inference and complete installer execution on
every target remain unverified locally. Architecture inspection and compilation
are reported separately from execution. Real-model/provider checks are opt-in;
ordinary regressions are offline. The bounded convenience cohort has no held-out
adjacency labels, and therefore supplies no superiority claim or calibrated
weights. Optional AcousticBrainz bulk import awaits usable mappings and data.
No supervised heads, Music-JEPA or retraining are included. Model packs are
prepared for distribution but no release has been published by this task.
