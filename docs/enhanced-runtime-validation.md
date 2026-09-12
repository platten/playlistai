# Enhanced audio runtime validation

These are measured development results, not musical-quality evidence or release
certification on untested operating systems. Measurements were executed on
2026-09-11 on Windows/amd64, Intel Core Ultra 9 285H, Go 1.27.0, default
GOMAXPROCS 16. Native builds used the existing verified LLVM-MinGW toolchain,
ONNX Runtime 1.26.0 CPU, two intra-op threads and one inter-op thread. They were
run on a shared development machine without CPU or background-load isolation.

The new decode/cache/policy measurements cover the consolidated enhanced-audio
working diff based on `bc9d6f23a7da22be9eb22ee951ffeb349e899774`. M2 DSP values and
the earlier resampler/worker measurements are identified separately below.
No source recording or private library was persisted to prepare a benchmark.

## Component timings

| Component and fixed input | Observed time/op | Allocated bytes/op | Allocations/op |
| --- | ---: | ---: | ---: |
| Original MP3 decode; 1,148 synthetic zero-data frames, 29.99 s, 44.1 kHz stereo | 126,424,567 ns | 85,473,032 | 158,463 |
| MERT sinc resample; 5 s, 48 kHz stereo, 440 Hz sine, amplitude 0.1 | 168,847,000 ns | 2,410,146 | 4 |
| Representation cache read; six 768-D segments plus pooled vector | 344,500 ns | 94,866 | 129 |
| Representation cache insert; same shape, distinct IDs, fingerprint included | 3,158,667 ns | 56,085 | 85 |
| Enhanced rank, 24 candidates | 101,830 ns | 57,571 | 447 |
| Enhanced sequence, 24 candidates | 1,172,996 ns | 662,545 | 28,305 |
| Enhanced rank, 128 candidates | 571,532 ns | 311,155 | 2,287 |
| Enhanced sequence, 128 candidates | 37,121,620 ns | 18,069,691 | 869,639 |

Decode input was constructed from the existing synthetic MPEG-1 Layer III silent
fixture in memory. The original-channel decode allocated about 85 MB in total;
that is allocation churn, **not** simultaneously resident PCM or peak RAM. Its
encoded input is 478,716 bytes and observed throughput was 3.79 MB/s. The
benchmark times the decoder and owned float32 conversion, including cleanup.

Cache benchmarks use temporary SQLite stores with the production representation
adapter. Setup and the read fixture's initial insert are outside the timed
region. Reads include parsing and fingerprint validation. Each timed insertion
has a new track ID, so this measures a real insert rather than an ignored duplicate.
The synthetic normalized vectors are deterministic unit vectors; they have six
5-second segments, 30-second coverage and a 768-dimensional pooled vector.

Rank/sequence fixtures are synthetic compatible two-dimensional model-space
vectors with all candidates having derived evidence. They use seed 42 and no
provider calls or model inference. They measure the policy's CPU/allocation
behavior, not semantic quality or full application latency. The 128-candidate
sequencer's allocations are visible rather than omitted from the report.

Reproduce the component commands:

```powershell
$env:CC = & .\scripts\install-clap-toolchain.ps1 -CheckOnly
$env:CGO_ENABLED = '1'
go test ./internal/audio -run '^$' -bench '^BenchmarkEnhanced' -benchtime=3x -count=1 -benchmem
go test ./internal/audio -run '^$' -bench '^BenchmarkMERTResampleFiveSeconds$' -benchtime=3x -count=1 -benchmem
go test ./internal/reco/multichannel -run '^$' -bench BenchmarkEnhancedPolicy -benchtime 300ms -count 1 -benchmem
```

The previously merged M2 DSP measurement used a 30-second 48 kHz stereo 1 kHz
sine at amplitude 0.5, with opposed channels. Its three observed values were
190,183,900 / 189,793,500 / 191,865,667 ns/op, each 291,536 B/op and 17
allocations/op. That isolated extractor benchmark excluded decode, caches and
neural inference. See [M2 evidence](enhanced-audio-milestones.md).

## Real native model validation

The initial timings in this section used `packs-v1` on the development host.
Use the corrected `packs-v2` command below for reproduction. Final app-local CRT
validation is recorded separately at the end of this report.

The official Windows asset pack's compiled Go worker passed three pinned
PyTorch-reference health fixtures, then canceled an actual inference call and
passed health again after worker restart:

| Native operation | Observed elapsed time |
| --- | ---: |
| Cold worker start, model load and three health fixtures | 2,497 ms |
| Three health fixtures on the resident worker | 1,924 ms |
| Worker reload and health after cancellation | 2,819 ms |

These health batches are not a single five-second segment latency measurement.
They include a full silence input, a full tone and a shorter masked tone.
The graph is pinned by SHA-256
`9d06066836ae4d5001954f1689b89447c3a65119933e34c3f3a8587f9d4f442b`;
model revision is `12af15fef9d0ac838c3f475bfbbf26d2060dd4f5`.
The acceptance tolerance is maximum coordinate difference 1e-4 and cosine at
least 0.9999, with normalized finite outputs required.

```powershell
go run ./cmd/mertparity C:/Users/pawel/Downloads/playlistai-enhanced-audio/derived-mert/packs-v2/mert-windows-amd64
```

Five Python/Go preprocessing reference cases passed, covering 8, 24, 44.1, 48 and
96 kHz, mono/stereo, anti-aliased resampling and observed-only normalization.
The opt-in fixture command is in [preview evaluation](enhanced-preview-evaluation.md).
The exporter separately measured eight PyTorch/ONNX cases with maximum absolute
error 1.0673e-6 and minimum cosine approximately 0.99999999998324.

An earlier initial Windows pack used the ONNX Runtime 1.26.0 Python-wheel DLL.
Its native worker had an observed peak working set of **525,709,312 bytes**
during two complete health batches, sampled every 50 ms using
`Get-Process.PeakWorkingSet64` on the exact child executable. The console helper
was excluded. This is a measured process working-set peak, not the parent
application's total memory, and not a full-playlist peak. The final official-DLL
pack passed parity and cancellation separately; its peak was not measured.

Worker regression fixtures verify cancellation kills and reaps the child,
crashes/invalid vectors/wrong identities stop it, unload permits clean restart,
permanent close prevents restart and canceled lock waiters exit promptly. The
shared generation budget admits at most 24 fresh tracks across hooks and
pre-ranking; cached compatible evidence is free. Optional deadline expiry does
not cancel CLAP or discard usable CLAP results.

## End-to-end preview acquisition and remaining scope

The [fixed real-preview run](enhanced-preview-evaluation.md) verified eight of
eleven requested catalog recordings, downloaded 3,838,616 transient audio bytes
and completed in 81,150 ms including health, network, DSP, MERT, CLAP and writes.
The same eight complete cases were compared in all four embedding methods.
Three unresolved recordings had no audio download. There were no held-out
adjacency labels; no retrieval-accuracy or quality superiority result is claimed.

Initial MERT inference and pack measurements used Windows/amd64; the subsequent
Linux amd64 WSL2 check is recorded below. Prepared packs for other architectures
are not proof of native execution on those hosts. Cross-compilation, checksummed assets and hosted CI
compilation must remain separate from actual native inference/package evidence.
Full-application startup, full-playlist peak RAM, representative hardware latency
and large independent musical evaluation remain distinct measurements; the
component data above does not fill those gaps.

## Final Windows app-local CRT validation

The final `packs-v2` artifacts include Microsoft Visual C++ Runtime 14.51.36247
dependencies and their separate terms. Original `packs-v1` Windows packs are
retained but superseded. The isolated worker loads ONNX Runtime using its
verified directory and System32 search flags, then verifies each required CRT
module's actual loaded path is the checked pack path. This prevents the developer
machine's installed CRT from masking an incomplete distribution.

On the same Windows amd64 host, a fresh run with v2 measured cold/warm/reload
health at **2,532 / 1,814 / 2,361 ms**, with successful cancellation. A second run
measured **2,454 / 2,042 / 2,617 ms** while observing a worker peak working set of
**525,746,176 bytes**. These are individual three-fixture health batches, not
latency distributions or all-platform bounds. No unrelated benchmarks were rerun.

A freshly compiled desktop binary also passed framed MERT worker health with
v2, including verification of all four loaded CRT paths, in 3.14 seconds. All
five ZIPs were reread: exact manifest/file membership, artifact hashes and native
architectures passed. Windows recursive imports require four bundled CRT DLLs
for amd64 and three for arm64, with no further non-OS DLL dependency. Windows
ARM64, Linux ARM64 and macOS native inference remain untested locally.

## Native Linux amd64 worker in WSL2

An additional native Linux worker check passed in Ubuntu 24.04.4 LTS, kernel
`6.18.33.2-microsoft-standard-WSL2`, on the same x86-64 host. It used Go 1.27.0,
GCC 13.3.0, glibc 2.39 and libstdc++/libgcc 14.2. All ELF dependencies resolved.
The actual Linux ONNX worker passed reference health, cancellation and reload:
**4,672 / 1,549 / 4,654 ms** for cold/warm/reload health. The v2 graph hash is
unchanged. These are individual health batches, not independent hardware
measurements or evidence of Linux GUI/installer behavior.

Reproduce with Go 1.27.0 and GCC available in Ubuntu WSL:

```sh
cd /mnt/c/Users/pawel/Documents/GitHub/playlistai
CGO_ENABLED=1 CC=/usr/bin/gcc go run ./cmd/mertparity /mnt/c/Users/pawel/Downloads/playlistai-enhanced-audio/derived-mert/packs-v2/mert-linux-amd64
```

The executed run used a verified official Go SDK extracted in a unique `/tmp`
directory, with an isolated build cache; it changed no system installation or
shell configuration. Its exact command and environment are retained in
`derived-mert/packs-v2/linux-wsl-validation.json` under Downloads. The original
70,523,269-byte SDK archive and `tools/go-linux-sdk-source.json` are retained;
SHA-256 is `675c26c449cbb18fc24b74650de1eabbae6e16f64326fd85a283fb3b58280685`.
Native macOS and ARM inference remain unverified.
