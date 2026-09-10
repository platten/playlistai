# CLAP candidates and compatible custom bundles

Reviewed 2026-09-10. This is a practical survey of publicly documented CLAP
families and deployment candidates, not an exhaustive list of every fine-tune.
CLAP is a paired audio/text model. A training dataset or a file of previously
computed embeddings cannot replace its encoders.

## Candidates

| Candidate | Focus and available format | Compatibility and deployment assessment |
| --- | --- | --- |
| [LAION larger CLAP music](https://huggingface.co/laion/larger_clap_music) | Music-focused checkpoint; PyTorch; about 776 MB weights | **Wizard default:** project-hosted FP32 audio/text ONNX exports total 778,209,534 bytes. The paired export passes the repository's tokenizer, preprocessing, and numerical parity gate and uses a distinct cache identity. |
| [LAION larger CLAP music and speech](https://huggingface.co/laion/larger_clap_music_and_speech) | Music and speech checkpoint; PyTorch; about 776 MB weights | Same large architecture as the music checkpoint, but a different embedding space. Public paired ONNX export available below. |
| [LAION larger CLAP general](https://huggingface.co/laion/larger_clap_general) | General audio, music and speech; PyTorch | Another checkpoint in the large 512-dimensional family. Requires its own matching audio/text weights and validation. |
| [Xenova larger CLAP music and speech](https://huggingface.co/Xenova/larger_clap_music_and_speech/tree/e9fd5ac1dbf3280936a7fc3ec8a020453ff184db/onnx) | FP32, FP16 and quantized ONNX; separate audio/text encoders | Previous wizard default. Existing installations remain readable, but new recommended installs use the music-only checkpoint. |
| [Xenova larger CLAP general](https://huggingface.co/Xenova/larger_clap_general) | Paired ONNX conversion of the general checkpoint | Same architecture class; alternative requiring independent reference validation. No assumption of shared embeddings with music/speech. |
| [LAION CLAP HTSAT unfused](https://huggingface.co/laion/clap-htsat-unfused) | General audio/text retrieval; non-fusion processing | Smaller architecture class. A paired export can use the custom bundle contract if its exact preprocessing passes validation. |
| [Xenova CLAP HTSAT unfused](https://huggingface.co/Xenova/clap-htsat-unfused) | Public ONNX export of the unfused checkpoint | Smaller deployment candidate; validate its own audio/text pair and cache it separately. |
| [LAION CLAP HTSAT fused](https://huggingface.co/laion/clap-htsat-fused) | Variable-length audio with feature fusion | Not a direct replacement for this worker's single-segment non-fusion input. Requires a different preprocessing/inference adapter. |
| [Microsoft CLAP 2022 and 2023](https://github.com/microsoft/CLAP) | General audio/text models; checkpoints and inference code | Separate CLAP family, not interchangeable with LAION. Larger checkpoint files do not make it compatible with this worker or its cached embeddings. Requires its own export, tokenizer, preprocessing and adapter. |
| [Microsoft CLAPCap](https://github.com/microsoft/CLAP#clap-weights) | Caption generation using the 2023 encoders | Captioning package rather than a drop-in paired retrieval model. Not selected merely for having a larger download. |
| [setweavr CLAP music ONNX](https://huggingface.co/setweavr/clap-music-onnx/tree/8b5e1d0) | Public export described as LAION music; repository contains an audio graph | Incomplete standalone bundle: no matching text graph, tokenizer or numerical reference report in the inspected files. |
| [BioLingual](https://huggingface.co/davidrrobinson/BioLingual) | CLAP-derived bioacoustic model | Domain-specific alternative, not a music default. Different learned weights require a separate embedding namespace and validation. |
| [CLAP-MusicGen](https://huggingface.co/yuhuacheng/clap-musicgen) | Music model trained using synthetic music-caption pairs | Research alternative with a different training distribution; not validated against this application's worker contract. Its model-card size is not evidence of better playlist fit. |
| [LAION CLAPv2 scaling project](https://github.com/LAION-AI/open-clap-scaling/blob/main/WP1_CLAPv2.md) | Documented next-generation research plan | A plan is not a downloadable, validated checkpoint. Reassess when paired weights and an inference contract are released. |

The selected checkpoint is the full-precision, music-trained LAION large model.
The project exports both encoders from pinned source weights and publishes the
validated graphs as release assets because the upstream checkpoint is PyTorch-only.
The wizard downloads just those two FP32 encoders, tokenizer, and native CPU
runtime; it does not download all precision variants or duplicate combined graphs.
The previous Music + Speech bundle remains loadable, but its cached embeddings
cannot be mixed with the new model.

The [LAION project](https://github.com/LAION-AI/CLAP#pretrained-models) reports
different music benchmark results for the large checkpoints despite their shared
architecture. A playlist listening evaluation is still necessary to compare
their usefulness for this application.

The 2026-09-10 export smoke check also found very high cosine similarity
(`0.998960`–`0.999329`) between the music-only checkpoint's text embeddings for
four unrelated music prompts. Numerical export parity does not make that a
musical-quality result. Until a held-out listening/retrieval evaluation supports
general thresholds, this bundle keeps the existing uncalibrated-policy behavior:
it may contribute best-available evidence but cannot claim strict musical fit.

## What compatible embeddings means

The worker accepts matching audio and text encoders producing normalized
512-element vectors. Equal dimensions alone do **not** establish a shared
embedding space. Both graphs, tokenizer, source checkpoint and preprocessing
must be paired. Fine-tuning changes that space; never combine a fine-tuned audio
encoder with unrelated text projection weights.

Version 2 bundles include a fingerprint of the audio graph, text graph, vocabulary
and merges checksums in the stored model identity. Different artifacts cannot
reuse another bundle's cached embeddings, even if their advertised name and
revision are the same. The older recommendation catalog's vectors are also a
different space and are never compared directly with CLAP text embeddings.

## Create a custom bundle

External starting points:

- [LAION training and reproducibility instructions](https://github.com/LAION-AI/CLAP#reproducibility)
- [Hugging Face CLAP model and processor documentation](https://huggingface.co/docs/transformers/model_doc/clap)
- [Exporting ONNX models with Hugging Face Optimum](https://huggingface.co/docs/optimum-onnx/onnx/usage_guides/export_a_model)
- [ONNX Runtime model compatibility](https://onnxruntime.ai/docs/reference/compatibility.html)

These guides explain training and exporting. Their generic output is not itself
a Playlist AI bundle. Prepare the following application contract:

1. Export **both** encoders from the same checkpoint. The audio input is float32
   `input_features` with shape `[1,1,1001,64]`. Text inputs are int64 `input_ids`
   and `attention_mask` with shape `[1,77]`. Alternatively set `textUnpadded`
   for graphs accepting only variable-length `input_ids`; the worker removes
   padding before inference, and that path must pass the same reference tests.
   Each output has shape `[1,512]`;
   the worker normalizes finite nonzero vectors. Record the output names.
2. Supply matching RoBERTa `vocab.json` and `merges.txt`, plus the compiled
   preprocessing configuration: 48 kHz, ten seconds, 1024-point FFT, 480-sample
   hop, 64 Slaney mel bands from 50 to 14,000 Hz, reflect padding, floor 1e-10.
   Match the complete `PreprocessingVersion`, including MP3 decoding, quantization,
   resampling and segment policy. A fused model requires a different adapter.
3. Compare the exported outputs to the original model on independent synthetic
   signals and text inputs, including tokenizer and mel-feature comparisons.
   Keep the reference report and `health.json` with 512-element text/audio
   vectors, 77 token IDs, a text string and a 440 Hz synthetic-tone case. The
   native installer must reproduce these results; file hashes alone are not
   inference validation.
4. Provide the correct native ONNX Runtime 1.26.0 CPU library and complete model
   and dependency license notices for the target platform. Desktop users do not
   need Python; reference export/comparison may use Python on a developer machine.
5. Assemble a version 2 manifest using the application's built-in worker:

   ```sh
   go run ./cmd/audiopack --builtin-worker \
     --export /path/to/export --source /path/to/tokenizer \
     --runtime /path/to/libonnxruntime.so.1.26.0 \
     --licenses /path/to/licenses.txt --platform linux/amd64 \
     --memory-bytes 2147483648 --artifact-base-url https://your-host/version \
     --audio-output audio_embeds --text-output text_embeds \
     --output /path/to/bundle
   ```

   Use your actual graph output names (`embedding` for the repository's original
   export script), library filename, platform and measured memory budget. Add
   `--text-unpadded` for the public Xenova text graph. Inspect
   the manifest's label, source and license attribution before distribution;
   set `--model-source-url` and `--model-license` for your model's provenance.
   Publish its checksummed artifacts under the specified HTTPS URLs, then select
   the local `bundle.json` in the wizard's custom-bundle section. The app resumes
   artifact downloads, verifies sizes and SHA-256 hashes, checks platform and
   embedding identity, and runs both encoders before activating the bundle.

Runtime validation and musical-fit calibration are separate. A v2 bundle with
an empty policy can be installed and inference-tested; it does not enable
automatic musical-fit decisions. To enable those decisions, supply an actual
reviewed development policy with `--policy /path/to/policy.json`. Do not invent
thresholds or claim similarities are probabilities. Legacy v1 bundles still
require their own worker and a calibrated policy.

## Runtime availability and verification limits

Official ONNX Runtime 1.26.0 CPU assets are pinned for Linux amd64/arm64, Windows
amd64/arm64 and macOS arm64. Intel macOS has no matching asset in the inspected
release and receives an explicit custom-bundle message. Checksums of both each
archive and its exact extracted library are pinned. Native validation is run
on the user's machine before activation; platform inclusion does not claim a
clean-machine installation test has already passed on every operating system.

Built-in inference requires a cgo-enabled application, as well as the downloaded
native library. Linux and macOS desktop builds already enable cgo. Following the
v0.8.0 Windows packaging bug, Windows builds also enable it by default, using
checksum-pinned [LLVM-MinGW](https://github.com/mstorsjo/llvm-mingw) compilers for
x64 and ARM64. Run `scripts/setup.ps1`, then `scripts/build.ps1 -Architecture all`.
The compiler is a developer-only dependency; the isolated worker is compiled
into the desktop executable. Models and ONNX Runtime still download in the wizard.
Core recommendation packages remain covered by the pure-Go compilation gate.

Explicit pure-Go builds remain usable without analysis. The wizard checks build
capability before fetching a recommended bundle and offers clear skip/upgrade
guidance instead of displaying a raw missing-worker exception. A legacy custom
bundle can still supply its own native worker. Existing v0.8.0 Windows installers
must be replaced with a new build; re-downloading model weights cannot fix them.

Windows ONNX Runtime requires Microsoft's Visual C++ runtime, as documented by
[ONNX Runtime](https://onnxruntime.ai/docs/install/#requirements). This remains a
host prerequisite; the native build does not remove it. macOS runtime signing
and actual Windows ARM64 inference still require host-specific validation.

### Windows validation — September 9, 2026

Both Windows amd64 and arm64 desktop executables cross-compiled with cgo and
LLVM-MinGW 20260908 UCRT. Inspected imports contain Windows/UCRT libraries, not
compiler DLLs or Python. The amd64 desktop's `--check-audio-worker` probe passed.
The real recommended-bundle installer test executed on Windows amd64 through
WSL interoperability, using existing paired model files and the pinned official
ONNX Runtime 1.26.0 Windows archive. Installation, extraction, activation and
synthetic audio/text parity health passed, including the production desktop's
`--audio-worker` with an empty executable search path. Elapsed test time was
19.82 seconds including fixture staging; this is not playlist latency or musical
quality evidence. No music audio was downloaded for this test.

Offline PowerShell tests cover compiler checksums, no-download checks, cached
reuse, target selection and quoted paths. Browser fixtures cover native-capable
installation/retry and unsupported-build guidance with no download offered and
the ability to continue. The full Linux repository gate passed, including race
tests, pure-Go core compilation, vet, lint, bindings and frontend typecheck/build.
The Windows-hosted compiler and the actual `wails3 task windows:build` command
also built successfully from a Windows-local checkout. Repeating the real-model
installer/health test with that Wails desktop passed in 13.31 seconds.
Native inference has not
been executed on Windows ARM64 or a clean
Windows installation without preinstalled Visual C++ runtimes.

Recommended model provenance: upstream LAION revision
`195c3a3e68faebb3e2088b9a79e79b43ddbda76b`, public ONNX revision
`e9fd5ac1dbf3280936a7fc3ec8a020453ff184db`. See
`internal/audio/resources/parity.json` for the executed numerical comparison.

On Linux amd64/WSL2, all ten reference fixtures passed: three synthetic audio
signals and seven texts, including non-Latin text. Maximum absolute embedding
error was 9.834766387939453e-7 and minimum cosine was 0.9999999403953552.
Tokenizer comparisons were exact; maximum preprocessing error was
7.62939453125e-6. These are export/inference checks, not listening judgments.
The actual installer verified and extracted the official runtime, loaded both
graphs and passed native health checks. The desktop executable also passed in
`--audio-worker` mode with an empty executable search path and no Python setup.

Reproduce with downloaded artifacts outside the repository:

```sh
# Developer-only reference environment: torch 2.9.1+cpu,
# transformers 4.57.1, onnxruntime 1.26.0.
python python/validate_clap_export.py --music-and-speech --reuse-export \
  --source /path/to/upstream-checkpoint --go-fixtures /path/to/fixtures.json \
  --output /path/to/public-export
go build -o /path/to/public-export/audioworker ./cmd/audioworker
go build -o /path/to/playlist-ai .
PLAYLISTAI_TEST_PUBLIC_CLAP=/path/to/public-export \
PLAYLISTAI_TEST_DESKTOP=/path/to/playlist-ai \
  go test ./internal/audio -run TestRecommendedNativeInstallation -v
```

The optional native test expects `audio.onnx`, `text.onnx`, `vocab.json`,
`merges.txt`, `ort.tgz` (Linux amd64 runtime) and `audioworker` in the export
directory. Ordinary tests never download model weights. The initial 2 GiB
memory budget is provisional, not a measured guarantee across platforms.
