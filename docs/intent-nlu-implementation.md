# Compact intent models: implementation and preparation

The [expanded calibration diagnostic](intent-nlu-expanded-calibration-results.md)
found no additional useful extraction from the current trained DistilBERT pilot;
it remains inactive. More reviewed training data is needed before reconsidering it.

The deterministic compiler and local LLM remain active. MiniLM dictionary
assistance has been removed. Optional learned assistance uses only an imported,
reviewed DistilBERT task extractor; its pretrained base encoder alone is **not**
a trained playlist intent extractor.

The 40-prompt batch has since been approved and the first task pilot trained.
It remains inactive because calibration did not meet the gate. See
[approved changes and measured pilot results](intent-nlu-approved-review.md).

## Desktop behavior

Setup offers explicit preparation of DistilBERT assets. It downloads only the
pinned DistilBERT setup files and the matching native ONNX runtime, reusing
verified local files. No current DistilBERT-only compressed pack is published,
so automatic fallback to the retired combined pack is disabled. Advanced
manifest imports copy only the current pinned DistilBERT assets.

Base setup verifies file sizes, hashes and packaged native dependency availability.
It does not claim trained inference health. After a trained model has been
independently calibrated, Settings can import its directory. Import checks the
model, vocabulary, configuration, head and calibration hashes, copies an immutable
local pack, then requires native inference health. The separate **Use reviewed
DistilBERT suggestions** control remains disabled until an extractor is installed.
Preparation and import never enable it automatically.

The extractor sends bounded source-span proposals to the local LLM. Explicit
source facts remain authoritative, and failures preserve the existing parser.
Inference runs in an isolated compiled application worker; end users need neither
Python nor a compiler. Cancellation reaps the worker before a later restart.

The advisory identity is now `distilbert-advisory/v2` and the asset identity is
`distilbert-assets-v2`. The installed directory remains
`intent-nlu/intent-encoders-v1` to reuse verified legacy DistilBERT/runtime files.
Old MiniLM assets are no longer discovered or executed. They are not deleted from
user data during this migration. Build staging removes retired `minilm-*` payloads.

Legacy shared enablement migrates to `intentExtractorEnabled` only for a valid
previously imported extractor. Dictionary-only opt-in becomes disabled, prior
extractor opt-outs stay disabled, and runtime repair preserves selected extractor
settings. A reviewed extractor requires its runtime, not the unused base encoder.
Saved MiniLM proposal provenance remains readable historical data.

Source extraction is computed once for the app request, shared across LLM
retries and rules fallback, and compiled once. The compiler fixes required
`Title by Artist` recordings, distinct occurrences of the same name, grounded
artist-only output, spelled/negated quantities, soft descriptions and scoped
vocal requests. Wire schema 10 carries optional start, duration and preference
scope/strength fields. Saved core intent version 9 remains loadable. New proposal
provenance is additive; history does not re-run extraction. Parser cache identity
includes source, vocabulary, model and assistance versions and enabled state.

## Retained sources and model roles

The source archive is `C:\Users\pawel\Downloads\playlistai-intent-nlu-v1`.
It contains verified native ONNX graphs, tokenizer files, original training
checkpoints and model cards. Models are never committed to Git.

| Directory | Role | Runtime status |
| --- | --- | --- |
| `distilbert` | DistilBERT base cased, task adaptation source | Base encoder abstains; trained/calibrated task head required |
| `gliner` | GLiNER Small v2.1 entity baseline | Archived research checkpoint; no release-runtime claim |
| `gliner25` | GLiNER2.5 Small structured extraction challenger | Archived research checkpoint; native export remains unverified |

The exact model sources, revisions, hashes and Apache-2.0 declarations are in
[`sources.json`](../internal/intent/nlu/sources.json). The text extractor never supplies vectors to CLAP/MERT or other audio indexes. No additional audio was acquired.

Recreate the source archive with Python 3.11 or newer, using only its standard
library for downloads:

```powershell
python python/fetch_intent_nlu_models.py --output C:/Users/pawel/Downloads/playlistai-intent-nlu-v1
# Optional smaller preparation: only files used by desktop setup
python python/fetch_intent_nlu_models.py --setup-only --output C:/Users/pawel/Downloads/playlistai-intent-nlu-v1
```

For reviewed data preparation, training, ONNX export and independent numerical
parity, follow the complete commands in [the review and training guide](intent-nlu-review.md).
The original 40-record English proposal remains unreviewed for provenance; its
separate approved derivative records the user's decisions and amendments.
Preparation rejects unreviewed records as training labels. Synthetic fixtures verify that
the training/export code runs; they do not establish learned accuracy.

## Packaging and offline setup

Windows releases stage verified app-local MSVC DLLs into the executable. This
uses build-time Python and 7-Zip to extract pinned signed redistributables without
executing their installers. Their license documents accompany the embedded files.

Developer-ID macOS signing retains hardened runtime and adds the
`disable-library-validation` entitlement so the isolated worker can load the
checksum-pinned upstream ONNX dylib outside the signed bundle. This relaxes the
same-team library-signing restriction; native paths and hash verification remain
required. See [Apple's entitlement documentation](https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.security.cs.disable-library-validation).
Signed/notarized execution still requires validation on an available Mac.
The pinned macOS ONNX library requires macOS 14 or later. Older Macs retain the
existing parser; the optional setup step reports the requirement before any
model download. This does not raise the desktop application's minimum OS.

```powershell
python scripts/stage-nlu-runtime.py --arch amd64 --seven-zip "C:/Program Files/7-Zip/7z.exe"
# Use --arch arm64 for a Windows ARM64 binary.
```

All model files and the platform ONNX runtime can also be embedded in a single
release binary. This optional build step adds hundreds of MB; setup then extracts
verified embedded files instead of downloading them:

```powershell
python scripts/stage-nlu-models.py --platform windows/amd64 --cache C:/Users/pawel/Downloads/playlistai-intent-nlu-v1
wails3 build
```

Other targets are `windows/arm64`, `linux/amd64`, `linux/arm64` and `darwin/arm64`.
Stage each platform in its isolated build checkout. Runtime/model staging files
are ignored by Git; the embedded placeholder permits normal offline unit tests
without model downloads. Regular release builds download model weights during
setup; the optional staging step produces a fully self-contained model payload.

The Go-only setup/evaluation utility uses the same asset verification. Its
DistilBERT verification command checks independently generated tokenizer cases;
trained-task numerical parity remains in the extraction export/review workflow:

```powershell
go build -o bin/intentnlu.exe ./cmd/intentnlu
bin/intentnlu.exe setup --root C:/prepared/intent-nlu
bin/intentnlu.exe verify --kind distilbert --model-dir C:/prepared/intent-nlu/intent-encoders-v1/distilbert --runtime C:/prepared/intent-nlu/intent-encoders-v1/runtime/onnxruntime.dll --reference-input bin/intent-nlu-parity/distilbert-reference.json --output bin/intent-nlu-parity/distilbert-native.json
```

## Historical validation before MiniLM removal

The measurements below describe the earlier combined-model implementation.
They are retained as historical evidence and do not validate the removal or
represent current MiniLM availability.

See [measured parser and playlist results](intent-nlu-results.md) for the same-3B
before/after replay, checker changes and unresolved failures.

Windows native MiniLM matched all 17 original PyTorch embedding fixtures: maximum
absolute coordinate error `2.08183111187477e-7`, minimum cosine
`0.999999999999381`. MiniLM and DistilBERT each matched all 17 reference tokenizer
cases exactly, including UTF-8 offsets and composed/decomposed accented names.
Native MiniLM also ran with Python removed from the child environment.

The isolated native setup CLI verified archived assets and passed MiniLM health.
An actual Windows x86-64 executable embedding both encoders and ONNX also
completed fresh setup and MiniLM health with unusable network proxies and Python
removed from its environment. The staged payload was removed after this test so
normal unit-test binaries do not embed hundreds of MB. Linux, Windows ARM64 and
signed macOS runtime execution remain for their native hosts; a cross-compile
does not establish native execution.
The base DistilBERT worker returned `trained_head_unavailable`, as intended.
Sixteen offline Python regressions passed, including actual tiny synthetic training
and export. Browser fixtures exercised automatic download, failure/retry,
keyboard toggling and both themes at 390- and 1000-pixel widths.

Ten additional informal description probes produced **zero MiniLM suggestions**
with the initial conservative filters. Inspecting candidate ranks also found
semantically wrong neighbors (for example, “ominous and threatening” ranked
“comforting” highest). These were rejected; the thresholds were not lowered to
increase proposal count. This pilot establishes native integration and abstention,
not improved interpretation or playlist quality from MiniLM. That experimental model has since been removed from the runtime.

Numerical parity is not intent accuracy or listener preference. Adequate semantic
calibration, broader relation/scope heads and independent
playlist/listening evaluation remain required before replacing the LLM or
enabling learned hard overrides. The existing recommendation evidence gaps remain.
