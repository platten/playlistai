# Bounded enhanced preview evaluation

`cmd/enhancedpreview` acquires an explicitly authorized, fixed list of exact catalog
tracks. It uses the application's strict Deezer identity resolver, compiled Go
DSP, and the compiled native MERT worker. An existing compatible CLAP pack can
participate in the same preview download and original-channel decode. No Python
is used during acquisition or evaluation.

This is an unsupervised sanity check. It does not train a model, adjust weights,
collect manual labels, or establish that one representation sounds better.

## Prepare and acquire

Prepare a platform-specific MERT pack using [the Python preparation guide](mert-model-preparation.md).
Keep the source catalog, weights and generated packs in Downloads. Use an
existing unpacked application catalog; this tool opens its database read-only.
Choose at most 24 catalog IDs before running. The IDs must exist in that catalog;
missing IDs fail before any provider request. The default limit is 12.

Build with the repository's native toolchain, then run with explicit authorization:

```powershell
$env:CC = & .\scripts\install-clap-toolchain.ps1 -CheckOnly
$env:CGO_ENABLED = '1'
go build -o "$env:TEMP/enhancedpreview.exe" ./cmd/enhancedpreview
& "$env:TEMP/enhancedpreview.exe" `
  --authorized `
  --bundle 'C:/Users/pawel/Downloads/playlistai-enhanced-audio/derived-mert/packs-v2/mert-windows-amd64' `
  --catalog "$env:APPDATA/playlist-ai/catalog" `
  --clap-bundle "$env:APPDATA/playlist-ai/music-analysis/<installed-bundle>" `
  --track-ids '0DiWol3AO6WpXZgp0goxAV,70LcF31zb1H0PyJoS1Sx1r,2kRFrWaLWiKq48YYVdGcm8' `
  --limit 12 `
  --output 'C:/Users/pawel/Downloads/playlistai-enhanced-audio/evaluation/my-cohort'
```

On Linux/macOS, build the same Go command with CGO and the native platform
prerequisites; choose the matching MERT pack. `--clap-bundle` is optional. A
missing or incompatible CLAP pack is recorded as unavailable, without weakening
its existing paired-model, preprocessing or parity requirements. Never point the
output at a real user's application data directory.

The flag confirms authorization to analyze provider previews and retain supported
derived features. It does not change model licensing. MERT remains separately
licensed CC-BY-NC-4.0. The strict resolver must corroborate artist, complete title
and version, and uniquely resolve the provider identity. A mismatch, ambiguous
result, missing preview or provider error remains unavailable; the CLI does not
fall back to arbitrary search results or download full recordings.

Each track has a two-minute context; the complete run has a twenty-minute
context. Ctrl+C cancels native inference and the worker is killed and reaped.
Output is checkpointed after each attempted track. Rerunning the same cohort
reuses compatible derived caches; changed catalog/model identities cause misses.
Existing application data and source assets are not modified.

## Retained files and offline comparison

The selected output directory contains:

- `cohort.json`: frozen model-space identities and intended derived vectors,
  including unavailable entries for the common comparison pool.
- `acquisition.json`: exact catalog metadata, resolved provider identities,
  source hashes, sampled coverage, DSP measurements, segment and pooled MERT
  vectors, elapsed time, download bytes and explicit unavailable outcomes.
- `derived-cache/audio-analysis.sqlite`: the independent CLAP, DSP and MERT
  caches. No encoded audio, PCM, spectrograms or intermediate tensors are stored.

Run the offline evaluator on that exact cohort:

```powershell
go run ./cmd/enhancedeval `
  -input 'C:/Users/pawel/Downloads/playlistai-enhanced-audio/evaluation/my-cohort/cohort.json' `
  -top 3 > 'C:/Users/pawel/Downloads/playlistai-enhanced-audio/evaluation/my-cohort/evaluation.json'
```

Comparisons use the same complete-case candidate pool for catalog, CLAP, MERT and
the frozen hybrid transition proxy. The report records omitted tracks rather than
comparing methods against different candidates. Source coverage can differ:
CLAP/DSP retain the established sampled interval, while MERT uses up to the first
30 observed seconds. Similarity is not a probability or a categorical musical
judgment. Do not interpret these results as full production ranking performance.

## Observed Windows run, 2026-09-11

This historical acquisition used the original `packs-v1` graph/runtime on the
development host. Fresh runs must use `packs-v2`, which adds verified app-local
Windows CRT dependencies. The graph and representation identity are unchanged.

The retained run is
`C:/Users/pawel/Downloads/playlistai-enhanced-audio/evaluation/real-preview-20260911`.
An eleven-track convenience cohort was selected from exact catalog metadata
before analysis. Catalog version: `1:956917:1788613313`. Eight uniquely verified
previews were analyzed; three unresolved recordings downloaded no audio. The
successful cohort contains One More Time, Creep, Everything In Its Right Place,
Naima, Teardrop, Glory Box, Avril 14th and Firestarter. So What, Come As You Are
and Nothing Else Matters were unavailable under the exact version-matching rule.

The acquisition downloaded **3,838,616 bytes** total (479,827 bytes per successful
preview), retained no audio and completed in **81,150 ms**, including native
health checks, network activity, inference and cache writes. The shared original
MP3 decode supplied DSP, MERT and the existing verified CLAP bundle. Hardware was
Windows/amd64, Intel Core Ultra 9 285H, Go 1.27.0, ONNX Runtime 1.26.0 CPU with two
intra-op threads and one inter-op thread. This is one observed run, not a latency
service guarantee or a benchmark against other machines.

Pinned identities:

- MERT graph SHA-256:
  `9d06066836ae4d5001954f1689b89447c3a65119933e34c3f3a8587f9d4f442b`.
- MERT complete model identity fingerprint:
  `012ef65b763622de543946724b8d32b4096f64f07a2243ca76abe555b8bcd829`.
- Existing CLAP complete model identity fingerprint:
  `0a33d8c98fd8e995de53134f84a2f41eb5451d47f01f8e69a8a6d1f2c7db8e4a`.
- Frozen input cohort SHA-256:
  `8bd3076b60968ee2227f3a6f62021978cbfde7ff96ad70b9800bfe3c04fabbf9`.
- Evaluation policy SHA-256:
  `4b8b986ad574dee507c85d0d1a8c326e730446cc0a5d6e0ef828b03723dae9e4`.

All four methods had eight comparable tracks and excluded the same three missing
previews. No held-out adjacency labels were available, so no MRR/recall or
superiority result is reported. Neighbors differed: for One More Time, catalog,
CLAP and the frozen hybrid proxy selected Firestarter, while MERT selected Creep.
For Naima, all four selected Avril 14th. These are recorded observations, not
manual correctness labels; they do not justify tuning weights on this cohort.

## Native correctness and resource evidence

`go run ./cmd/mertparity <pack>` performs real native health comparisons against
pinned PyTorch references, cancels an actual inference call, and verifies health
again after worker restart. With the official Windows runtime pack, the observed
three-fixture health passes took 2,497 ms cold, 1,924 ms warm and 2,819 ms after
cancellation/reload. The coordinate tolerance is 1e-4 and cosine threshold 0.9999.
Five optional Python/Go resampling and normalization references also passed:

```powershell
$env:PLAYLISTAI_MERT_PREPROCESSING_REFERENCE = 'C:/Users/pawel/Downloads/playlistai-enhanced-audio/derived-mert/windows-amd64-v1/preprocessing-reference.json'
go test ./internal/audio -run TestMERTPreparedPreprocessingReference
```

A separate initial pack using the ONNX Runtime 1.26.0 Python-wheel DLL had an
observed native-child peak working set of **525,709,312 bytes**, sampled using
Windows `Get-Process.PeakWorkingSet64` every 50 ms during two complete health
passes. This is that process's observed peak, excludes the parent application,
and is not a total full-playlist memory measurement. The final official-DLL pack
passed parity and cancellation separately; its peak memory was not measured.

The deterministic Go resampler benchmark for five seconds of 48 kHz stereo,
440 Hz sine at amplitude 0.1 observed 168,847,000 ns/op, 2,410,146 B/op and four
allocations/op (`-benchtime=3x`). Input creation was outside the timed region.
Synthetic parity and these resource measurements establish implementation
behavior, not musical quality. Native macOS/Linux inference remains a separate
release validation requirement; Windows results do not substitute for it.
