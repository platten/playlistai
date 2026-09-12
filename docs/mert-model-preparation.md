# Maintainer MERT preparation

MERT export is offline maintainer tooling. Desktop users run compiled Go with
native ONNX Runtime; they never need Python, pip, PyTorch, Transformers, or a
Python worker. The model is pretrained and this process performs no training or
labeling. All original sources remain in `C:\Users\pawel\Downloads\playlistai-enhanced-audio`.
No audio dataset or Deezer previews are downloaded by these commands.

## Representation contract

The source is [MERT-v1-95M at the pinned revision](https://huggingface.co/m-a-p/MERT-v1-95M/tree/12af15fef9d0ac838c3f475bfbbf26d2060dd4f5).
The exporter verifies every source file against
[`enhanced-audio-sources.json`](../python/enhanced-audio-sources.json) before
importing the retained custom model implementation. It loads checkpoint tensors
with `torch.load(weights_only=True)` and strict state matching, runs evaluation
mode, and exports FP32 ONNX opset 17. The pinned configuration disables the CQT
branch, so the upstream optional `nnAudio` warning does not mean a missing
inference dependency. No remote custom code is fetched during export.

The graph takes `input_values` float32 `[1,120000]` and `attention_mask` int64
`[1,120000]`, and returns `embedding` float32 `[1,768]`. Use mono 24 kHz, five-second
segments. The mask is a contiguous valid prefix followed by zeros. Reject fewer
than 400 valid samples; short remainders are explicitly recorded as unrepresented
coverage by the caller. Never present padding as observed audio.

Preprocessing averages original channels, then applies the Go 64-radius
Hann-windowed sinc with cutoff `min(1,24000/sourceRate)*0.94` and clamped boundary
samples. The output length is `floor(sourceFrames*24000/sourceRate)`. This filter
also applies to 24 kHz input. Normalize **only observed samples**, using their
population mean and variance and denominator `sqrt(variance+1e-7)`, then right-pad
with zero. Accumulate mean/variance in float64 and store inputs in float32.
[`export_mert.py`](../python/export_mert.py) provides the independent Python
reference and emits five native preprocessing fixtures at 8, 24, 44.1, 48 and
96 kHz, including stereo. This resampler is the application contract; it is not
claimed to be bit-identical to torchaudio's default resampler.

The graph uses the final transformer layer (12). It masks temporal pooling using
feature lengths computed successively by `floor((length-kernel)/stride)+1` with
kernels `[10,3,3,3,3,2,2]` and strides `[5,2,2,2,2,2,2]`, then L2-normalizes with
epsilon `1e-12`. The full input produces 374 frames. Multiple segment embeddings
are averaged by their observed duration and L2-normalized again. The identity
records layer, pooling, source revision, graph hash, runtime and preprocessing;
these vectors must never be compared with CLAP or Deej-AI vectors directly.

The [upstream model card](https://huggingface.co/m-a-p/MERT-v1-95M) describes
layer-dependent representations and temporal reduction. Choosing the final layer
is an explicit fixed unsupervised baseline, not a measured assertion that it is
the best layer for every musical attribute. No learned layer aggregator is used.

## Prepare the environment and notices

Use CPython **3.12** (3.11 also has the required wheels, but was not executed in
this run). Python 3.13 is incompatible with the pinned tokenizer build. From the
repository root, with `python` resolving to 3.12:

```powershell
$assetRoot = 'C:\Users\pawel\Downloads\playlistai-enhanced-audio'
python python/fetch_enhanced_audio.py --out $assetRoot
python python/fetch_enhanced_audio.py --out $assetRoot --verify-only
python -m venv "$assetRoot\tools\mert312-venv"
$mertPython = "$assetRoot\tools\mert312-venv\Scripts\python.exe"
& $mertPython -m pip install -r python/requirements-mert.txt
New-Item -ItemType Directory -Force "$assetRoot\derived-mert" | Out-Null
Invoke-WebRequest 'https://creativecommons.org/licenses/by-nc/4.0/legalcode.txt' -OutFile "$assetRoot\derived-mert\CC-BY-NC-4.0.txt"
$ortPackage = "$assetRoot\tools\mert312-venv\Lib\site-packages\onnxruntime"
Get-Content "$ortPackage\LICENSE", "$ortPackage\ThirdPartyNotices.txt" | Set-Content -Encoding utf8 "$assetRoot\derived-mert\ONNX-RUNTIME-NOTICES.txt"
& $mertPython -m pip freeze | Set-Content -Encoding utf8 "$assetRoot\derived-mert\python-environment.txt"
```

The current retained environment used the bundled CPython 3.12 executable at
`C:\Users\pawel\.cache\codex-runtimes\codex-primary-runtime\dependencies\python\python.exe`
to create the venv. The application does not depend on this path.

Weights and the derived graph remain **CC-BY-NC-4.0**, separate from the GPL app.
The generated `LICENSES.txt` includes attribution, source revision, a description
of export modifications, the full Creative Commons license, original model card,
and ONNX Runtime MIT/third-party notices. Retained upstream Python source remains
in the source archive, not in the runtime bundle. Do not imply that GPL licensing
removes the model's noncommercial restriction. A commercial distribution needs a
separately licensed model; no such permission is established here.

## Export and verify the actual model

```powershell
& $mertPython python/export_mert.py `
  --source-root $assetRoot `
  --out "$assetRoot\derived-mert\windows-amd64-v1" `
  --platform windows/amd64 `
  --license-text "$assetRoot\derived-mert\CC-BY-NC-4.0.txt" `
  --runtime-library "$ortPackage\capi\onnxruntime.dll" `
  --runtime-license "$assetRoot\derived-mert\ONNX-RUNTIME-NOTICES.txt" `
  --threads 4
& $mertPython -m unittest discover -s python -p test_export_mert.py
```

Use a new output directory for each validated export; an existing installable
manifest is never overwritten. The exporter refuses output directories inside
Git. It checks ONNX graph validity, runs the **actual** upstream PyTorch model
and CPU ONNX inference on silence, tones, short input, an impulse, seeded noise,
an amplitude envelope and minimum input, and checks duration-weighted pooling of
two segments. Maximum embedding error must be at most `1e-4` and cosine at least
`0.99999`; failures stop before the installable manifest is written. Reference
PyTorch embeddings use the upstream Wav2Vec2 normalizer. Application normalization
must agree at absolute/relative tolerances `1e-6`/`1e-6` before inference.

Outputs include `mert-audio.onnx`, the native runtime library, `LICENSES.txt`,
`health.json`, `mert-bundle.json`, `parity-report.json`, and
`preprocessing-reference.json`. The manifest pins each installed artifact's size
and SHA-256. The Go importer runs native health checks before activation. A report
alone is not permission to skip those checks. Reports and fixtures describe
synthetic signals; they contain no private audio or listening data.

On Linux/macOS use the same Python script with a local Python 3.12 venv, POSIX
paths and the appropriate native ONNX Runtime **1.26.0 CPU** library/notices.
Set `--platform` to `linux/amd64`, `linux/arm64` or `darwin/arm64`; Windows ARM64
uses `windows/arm64`. These are the five targets in the current release workflow.
The flag declares the bundle target; it does not cross-compile or verify a native
library's architecture. Acquire the release library through the application's
pinned runtime paths, inspect its architecture, and execute native Go health
checks on that OS before distribution. Each pack is Go + native library + graph
+ notices; omit the entire venv, original checkpoint and Python scripts.

## Assemble every shipped OS pack

The standard-library pack builder reuses the validated graph for all five
release targets. It downloads each official ONNX Runtime 1.26.0 source archive,
verifies its pinned size/hash, reads only the exact regular library member,
verifies its uncompressed hash, inspects its PE/ELF/Mach-O architecture, and
includes that archive's complete runtime notices. The source pins in
[`mert-runtime-sources.json`](../python/mert-runtime-sources.json) match the
application's `recommendedRuntimes` registry; a regression checks this agreement.

```powershell
python python/prepare_mert_packs.py `
  --export "$assetRoot\derived-mert\windows-amd64-v1" `
  --out "$assetRoot\derived-mert\packs-v1" --zip
python -m unittest discover -s python -p test_prepare_mert_packs.py
```

This operation is authorized acquisition for this implementation, not a dry run.
The retained layout contains `runtime-sources/` with five original archives,
`mert-windows-amd64/`, `mert-windows-arm64/`, `mert-linux-amd64/`,
`mert-linux-arm64/`, `mert-darwin-arm64/`, five compressed ZIPs, and
`pack-inventory.json` with source URLs, bytes and hashes. Existing verified source
archives are reused. Completed bundles are preserved: choose a new output root
for rebuilding, optionally copy the verified `runtime-sources/` there first.
An interrupted runtime download restarts its bounded `.part` file. No downloaded
models, archives or packs belong in Git.

The pack manifest is a local import descriptor; it has no speculative public
download URL. A release may distribute these assets beside the compiled binary
or inside its OS installer, and the app's verified MERT importer can activate
the selected target pack without Python. Do not bundle the Windows library into
another OS package or copy the PyPI library implicitly: official release archives
can differ from the wheel build despite the same ONNX Runtime version.

Execute the native Go command on each target (a working C compiler is needed
only to build the maintainer command):

```powershell
$env:CGO_ENABLED = '1'
go run ./cmd/mertparity "$assetRoot\derived-mert\packs-v1\mert-windows-amd64"
$env:PLAYLISTAI_MERT_PREPROCESSING_REFERENCE = "$assetRoot\derived-mert\windows-amd64-v1\preprocessing-reference.json"
go test ./internal/audio -run TestMERTPreparedPreprocessingReference -count=1
Remove-Item Env:PLAYLISTAI_MERT_PREPROCESSING_REFERENCE
```

Use the corresponding prepared pack on other operating systems. The command
checks real graph outputs, cold/warm worker health, in-flight cancellation and
successful worker restart. Cross-compilation or assembling a ZIP does not replace
this native gate. The preprocessing test covers the independent Python reference
for original-rate mono/stereo conversion, anti-alias resampling and normalization.

## Executed evidence and limits

On 2026-09-11, Windows 11 x86-64, Intel Core Ultra 9 285H, four CPU threads,
PyTorch 2.7.1, Transformers 4.44.2, ONNX 1.18.0, ONNX Runtime 1.26.0 and NumPy
2.2.6: all eight numerical cases passed. Maximum embedding absolute error was
`1.0672956705093384e-6`; minimum cosine was `0.9999999999832396`. Six bounded
Python preprocessing regressions passed. The reference normalizer's largest
absolute difference was `3.0517578125e-5` on the large normalized impulse sample;
the relative tolerance passed and its embedding error was `6.85e-7`.

The graph has **94,371,712 parameters**, is **377,679,573 bytes**, and has SHA-256
`9d06066836ae4d5001954f1689b89447c3a65119933e34c3f3a8587f9d4f442b`.
Measured export time was 4.01 seconds; the seven single-segment CPU inference
calls took 0.364–0.387 seconds each in an already loaded session. These are
single-run timings, not a latency distribution or startup measurement. The
manifest's 2 GiB memory budget is conservative configuration, **not measured peak
RAM**. No quantization was applied. Preserve full-precision sources before
considering compression or quantization, and rerun numerical and musical checks
for any changed graph.

The report measures Python reference/ONNX parity; native Go parity is a separate
gate. The official Windows x86-64 runtime pack also passed native Go health,
in-flight cancellation (1 ms deadline), and successful reload health. One run
measured cold health 2497 ms, warm health 1924 ms and post-cancellation reload
health 2819 ms; health executes three reference inputs, so these are not
single-track latency measurements. All five native Go preprocessing reference
cases passed. All five model ZIPs were unpacked in memory and every member's
size/hash and the exact file allowlist were verified; all native library headers
matched their declared PE/ELF/Mach-O architecture. The original runtime archives
total **201,141,124 bytes**; the five compressed model packs total
**1,194,532,691 bytes** (each is about 237–242 MB). These are asset packs, not
complete application installers. `pack-inventory.json`,
`architecture-inspection.json` and `package-inspection.json` retain that evidence.
Native Linux/macOS/Windows ARM64 execution, complete installer execution on every target,
and held-out musical suitability are not established by this Windows export.
Subsequent Windows worker memory measurements are recorded separately in
[runtime validation](enhanced-runtime-validation.md); they are not all-platform bounds.
The synthetic fixtures establish numerical behavior only. For musical evaluation,
use only accessible source PCM or authorized exact-recording Deezer previews
under the [data eligibility rules](enhanced-audio-data-preparation.md). Do not
download feature-only corpora expecting to derive new audio embeddings from them.

## Exact native install, status and removal commands

The desktop Settings controls perform the same verified import and removal. For
repeatable command-line administration, `cmd/mertpack` requires the managed MERT
directory explicitly; it does not discover or change the application data path.
Close Playlist AI before managing its model directory from the CLI, then reopen
it so the application reloads the selected model. The installed model is optional;
removing it preserves separately stored DSP/MERT derived caches and retained
source packs.

On this Windows installation, the application data directory is
`C:/Users/pawel/AppData/Roaming/playlist-ai`. Its managed MERT subdirectory is
`mert-analysis`; do not supply the parent application directory. If your configured
data directory differs, change the following explicit path accordingly.

```powershell
$env:CC = & .\scripts\install-clap-toolchain.ps1 -CheckOnly
$env:CGO_ENABLED = '1'
go build -o "$env:TEMP/mertpack.exe" ./cmd/mertpack

$mertDirectory = 'C:/Users/pawel/AppData/Roaming/playlist-ai/mert-analysis'
$mertSource = 'C:/Users/pawel/Downloads/playlistai-enhanced-audio/derived-mert/packs-v1/mert-windows-amd64'

& "$env:TEMP/mertpack.exe" status --directory $mertDirectory
& "$env:TEMP/mertpack.exe" install-local --directory $mertDirectory --source $mertSource
& "$env:TEMP/mertpack.exe" status --directory $mertDirectory

# Remove only the managed model installation when it is no longer wanted.
& "$env:TEMP/mertpack.exe" remove --directory $mertDirectory
```

`install-local` validates the local pack, verifies its file sizes and hashes,
copies into the managed directory, executes native reference health checks and
activates only on success. It performs no network access or preview analysis.
`status` reports the active model identity and license after integrity validation;
it does not perform a new inference. `remove` rejects unrelated directories and
source packs that are not recognized managed installations. Source assets remain
in Downloads. The CLI itself is compiled Go; end users need no Python environment.

Source and installation directories may be on different Windows drives or UNC
shares. Before copying any artifact, the CLI records ownership of its exact
version directory using a small `.mertpack-attempt` marker. If the first install
or an upgrade is interrupted during copying or fails its health check, `remove`
can clean up that owned partial version even without a completed bundle manifest
or active pointer. Unknown files and unmarked unrelated directories still block
recursive removal. Re-running `install-local` can also resume verified artifacts.

On Linux/macOS, build `go build -o mertpack ./cmd/mertpack` with the documented
native CGO prerequisites, then run the identical subcommands with explicit local
paths and the matching prepared pack. Windows results do not establish native
operation on other hosts.

### Executed Windows package probe

A native CGO build of the actual desktop package was created in a temporary path:

```powershell
go build -o "$env:TEMP/playlistai-enhanced-validation.exe" .
& "$env:TEMP/playlistai-enhanced-validation.exe" --version
& "$env:TEMP/playlistai-enhanced-validation.exe" --check-audio-worker

$env:PLAYLISTAI_MERT_MAIN_BINARY = "$env:TEMP/playlistai-enhanced-validation.exe"
$env:PLAYLISTAI_MERT_MAIN_BUNDLE = $mertSource
go test ./cmd/mertpack -run TestPackagedDesktopMERTHealth -count=1 -v
```

On 2026-09-11, `--version` returned `dev`, the compiled audio-worker capability
check exited successfully, and the framed MERT health request to this actual
Windows desktop executable passed in 2.70 seconds. That probe starts
`--mert-worker`, verifies the complete returned model identity and successful
three-fixture native parity response, closes stdin and checks a clean exit. It
does not launch the GUI. Native `mertpack` status → install-local → status → remove
also passed against a unique temporary managed directory using the official
Windows pack; removal was verified and the original source pack remained intact.
These are build and native-worker checks, not an installer/signing or graphical
interaction certification.
