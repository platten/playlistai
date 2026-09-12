# Enhanced audio: retained sources and maintainer preparation

This guide accompanies [the milestone PR gates](enhanced-audio-milestones.md).
M2's deterministic DSP runs directly in Go over transient original-rate PCM;
it needs no data-preparation script, trained asset or additional download. Its
synthetic regressions and measurement definitions are in the
[DSP guide](dsp-measurements.md).
M1 acquires and verifies the existing catalog and pinned MERT reference source.
It does **not** export or enable MERT yet. M3 must implement and execute the
reference export/parity harness before any MERT scoring can become available.
Do not feed MERT weights into the existing paired CLAP bundle builder.

## Data selection

No new model training or manually labeled corpus is needed. Keep the existing
Deej-AI catalog and use MERT's pretrained self-supervised representation. This
preserves the recommendation catalog and avoids downloading audio for 956,917
tracks. Source acquisition itself does not contact Deezer or fetch preview audio.

The pinned source lock is [`python/enhanced-audio-sources.json`](../python/enhanced-audio-sources.json).
It retains the existing verified compressed catalog and the upstream MERT
Transformers checkpoint, configuration, reference implementation and model card.
The checkpoint is 377,552,987 bytes. Its SHA-256 is
`a2b8b747f72c06e0595aeae41ae5473f4364938c6b39b2c58be38c48e6bd3fcd`.
The catalog is 219,618,902 bytes. These are source sizes, not measured RAM use or
final ONNX distribution sizes. No lower-precision replacement is selected in M1.

MERT revision:
[`12af15fef9d0ac838c3f475bfbbf26d2060dd4f5`](https://huggingface.co/m-a-p/MERT-v1-95M/tree/12af15fef9d0ac838c3f475bfbbf26d2060dd4f5).
The pinned config specifies 24,000 Hz, 12 transformer layers and hidden size 768.
The upstream card reports 95M parameters and a five-second pretraining context;
the parameter count has not yet been measured by loading the checkpoint.
The preprocessing config specifies normalization, right padding with zero and
an attention mask. M3 must reproduce its actual numerical behavior, including
short-input handling, instead of merely copying these settings.

MERT weights are **CC-BY-NC-4.0**, separately licensed from the GPL-3.0 app.
The retained model card records attribution; reference source files are preserved
verbatim, including their upstream implementation references. Their code-license
provenance must be resolved before redistribution; the model card's weight license
is not a substitute for that check. Acquisition is not permission to relicense assets.
M3's distributable pack must include complete applicable notices and license text.

## Download and verify on Windows

Run from the repository root. Python 3.11+ is needed only on the maintainer's
machine; this downloader uses the standard library and needs no pip packages.

```powershell
$assetRoot = 'C:\Users\pawel\Downloads\playlistai-enhanced-audio'
python python/fetch_enhanced_audio.py --out $assetRoot
python python/fetch_enhanced_audio.py --out $assetRoot --verify-only
python -m unittest discover -s python -p 'test_fetch_enhanced_audio.py'
```

The first command performs the downloads for this work. Run it again after an
interruption; `.part` files resume only with a matching HTTP range and all final
files require the pinned size and SHA-256. If a server ignores range requests,
the partial download restarts. A mismatching existing final file is preserved
and reported as an error; move it aside after investigation rather than silently
overwriting it. A full-length corrupt partial also requires investigation and
removal before retrying. Do not run two downloaders against the same directory.

The second command performs no HTTP requests. A successful run writes
`source-inventory.json`, including source URLs, revisions, licenses, byte counts,
checksums, verification time and the checksum of the checked-in source lock.
It explicitly records that no audio was downloaded and MERT export is unvalidated.
Do not mistake a previous inventory for a fresh successful verification.

Retained layout:

```text
C:\Users\pawel\Downloads\playlistai-enhanced-audio\
  source-inventory.json
  sources\catalog\catalog.tar.zst
  sources\MERT-v1-95M\
    pytorch_model.bin
    config.json
    preprocessor_config.json
    configuration_MERT.py
    modeling_MERT.py
    README.md
```

On macOS/Linux the same commands work with `python3` and a chosen absolute
`--out` directory. No model code is imported and no pickle is deserialized by
this downloader. The larger duplicate Fairseq training checkpoint is unnecessary
for the planned Transformers export and is not downloaded.

## Prepare the existing catalog

The retained `catalog.tar.zst` is already the optimized runtime dataset. To use
it without another download, set `catalog.bundle_path` to its absolute path in
a development config. The Go setup path verifies and unpacks the archive; use
a separate temporary app data directory for testing, never reset real user data.

Rebuilding from upstream pickles is optional, not needed to use the retained
runtime catalog. If a deliberate catalog rebuild is required, the existing Python
scripts provide this path (only unpickle sources whose provenance you trust):

```powershell
$assetRoot = 'C:\Users\pawel\Downloads\playlistai-enhanced-audio'
python -m venv "$assetRoot\tools\catalog-venv"
& "$assetRoot\tools\catalog-venv\Scripts\python.exe" -m pip install -r python/requirements.txt
& "$assetRoot\tools\catalog-venv\Scripts\python.exe" python/fetch_pickles.py --out "$assetRoot\sources\deej-ai-pickles"
& "$assetRoot\tools\catalog-venv\Scripts\python.exe" python/convert_pickles.py --pickles "$assetRoot\sources\deej-ai-pickles" --out "$assetRoot\derived\catalog" --manifest "$assetRoot\derived\catalog\catalog-manifest.json"
go run ./cmd/catalogpack -in "$assetRoot\derived\catalog" -out "$assetRoot\derived\catalog.tar.zst"
```

`fetch_pickles.py` detects Google Drive HTML/quota failures. Its basic sanity
checks are not equivalent to the pinned checksums in the new source downloader.
`convert_pickles.py --limit N` creates a smaller development catalog, which must
not replace the production catalog implicitly. Preserve full sources and write
derived/quantized/compressed outputs separately. See [catalog construction](CATALOG.md)
for the format, licenses and hosting rules. No catalog retraining is performed.

## Audio eligibility and evaluation preparation

Use deterministic synthetic signals for DSP and numerical parity fixtures. For
music-level evaluation, choose a bounded cohort only when source audio is
available or an exact verified recording can obtain a Deezer preview of up to
30 seconds under the existing preview policy. Skip unavailable, ambiguous or
unauthorized tracks; report their counts and reasons. An artist/title similarity
alone is not an accepted recording match. Do not persist signed preview URLs as
dataset identities, or retain downloaded preview bytes, decoded PCM, spectra or
intermediate neural tensors. Persist only permitted derived measurements and
representations with coverage/provenance. Model-download consent does not replace
preview authorization.

AcousticBrainz's [official archives](https://acousticbrainz.org/download) contain
features keyed by recording MBID, not source PCM. They cannot produce MERT vectors
or establish preview availability. Bulk acquisition is deferred to M8 until exact
usable catalog/preview mappings and an importer exist; current online/archive
evidence remains available through the existing enrichment path. No unrelated
feature-only corpus, supervised classifier pack or unavailable training corpus
is downloaded for this task.

## Export and release gate (M3 onward)

The planned maintainer-only MERT script must load the retained exact revision,
review its custom implementation before execution, export a fixed five-second
24 kHz graph and record names/shapes/dtypes/hash/size. It must generate synthetic
reference cases for silence, tones, impulses, seeded noise, dynamic envelopes,
short input and multiple segments, then compare Go preprocessing and ONNX output
with PyTorch at measured tolerances. It must not train parameters or invent parity
results. Exact runnable export/install commands belong in the M3 PR once they
exist and have been executed; there is no MERT install command in M1.

Application/runtime packs contain compiled Go, native ONNX Runtime and separately
licensed graphs/notices as applicable. They never include `.venv`, pip, PyTorch,
Transformers, Essentia, or Python scripts. Each supported OS/architecture needs
native execution and package inspection before it is described as validated.
Compression/optimization is welcome after parity and quality checks; source
weights remain preserved. The current release allowlist excludes catalog/model
archives, so distribution layout changes require their own tested milestone.
