# Standalone local audio processing and web-source annotations

Build the headless binary independently of the desktop app:

```sh
go build -o bin/playlist-indexer ./cmd/playlist-indexer
bin/playlist-indexer help
```

The same executable contains the existing codec/audio workers and the new
`subset` and `annotate` commands. Audio processing reuses the indexer's verified
codec and model bundles. No Python runtime or desktop host is needed.

## Select a bounded subset

Create an isolated working directory outside the music collection:

```sh
mkdir -p /tmp/playlist-ai-subset
bin/playlist-indexer subset \
  --root /mnt/media/music \
  --out /tmp/playlist-ai-subset/audio \
  --limit 16 --max-bytes 1073741824
```

The command copies the first supported files in lexical order, up to the file
limit. This is a bounded sample, not a representative musical evaluation set.
For a deliberately chosen subset, supply `--file-list selection.json`, where the
file contains a JSON array of exact paths relative to `/mnt/media/music`.
The list is sorted for reproducibility. Absolute paths, traversal, duplicates,
symlinks, and unsupported extensions are rejected for an explicit selection.

Copies preserve source audio and tags. No source files are edited, uploaded, or
re-encoded by `subset`. Unlike hardlinks, later edits to the copies cannot alter
the originals. The new output directory includes `subset-manifest.json`, which
records relative paths, byte counts, SHA-256 hashes, and the selection policy.
The output must not exist and must be outside the source tree. Exceeding the
byte/time budget fails without publishing a partial subset. Directory discovery
also has a 100,000-entry ceiling. Supported inputs match the existing indexer:
FLAC, MP3, AAC, M4A, and MP4. Symlink directories are not followed by selection.

## Build compatible audio and metadata

Use the existing verified runtime and original LAION CPU bundle paths:

```sh
bin/playlist-indexer run \
  --root /tmp/playlist-ai-subset/audio \
  --state /tmp/playlist-ai-subset/index-state \
  --analysis clap --clap-device cpu \
  --clap-bundle /absolute/path/to/verified-original-laion-cpu-bundle \
  --runtime-dir /absolute/path/to/verified-codec-runtime \
  --out /tmp/playlist-ai-subset/subset.paipack \
  --offline --no-progress
```

`--offline` forbids automatic asset acquisition; prepare the documented verified
bundles first. The existing analyzer records model identity, sampling coverage,
and separate vector-space versions. It does not relabel incompatible vectors.
For a lightweight metadata-only pipeline check, replace `--analysis clap` and
its model flags with `--analysis metadata`. That exports a metadata pack and
provides no audio measurements. `run` performs the existing fit/export steps;
there is no second audio pipeline in `annotate`.

## Acquire web-source facts

```sh
bin/playlist-indexer annotate \
  --pack /tmp/playlist-ai-subset/subset.paipack \
  --state /tmp/playlist-ai-subset/annotation-cache \
  --out /tmp/playlist-ai-subset/web-evidence.jsonl \
  --limit 16 --timeout 10m \
  --criterion instrumentation:piano \
  --criterion 'instrumentation:soft piano' \
  --criterion 'texture:spacious reverberation' \
  --criterion vocal:instrumental
```

This explicitly opts into online metadata acquisition. It reuses MusicBrainz
recording searches, exact recording verification, recording-linked Wikidata,
recording-linked Apple metadata, and eligible official recording pages. Existing
provider caches, rate limits, identity checks, bounded responses, and request
deadlines remain in force. It is **not a general web search engine** and does not
upload audio. `annotate --offline` produces unknown source assessments without
network acquisition and is useful for checking the export pipeline.

An embedded recording MBID is checked against recording title, performer credits,
and available ISRCs by the existing verifier. Independently fetched same-recording
ISRC contradictions or a conflicting reliable full-recording duration preserve
an ambiguous identity. Missing lengths and preview durations cannot establish
such a conflict; agreement alone does not establish identity. When a declared
release and release-track ID are available, the exact release-track length takes
precedence over an aggregate recording length. Missing or unavailable edition
evidence leaves that duration check unknown. Without a declared edition, a
large aggregate-duration discrepancy conservatively retains identity ambiguity;
it does not prove that the local master is wrong. When an MBID is missing, a name
search only supplies a proposal: an embedded matching ISRC must corroborate it
before source acquisition proceeds. Ambiguous identities and conflicting versions
remain unknown. Artist biographies and album-wide descriptions cannot establish
recording-specific evidence.

Without a model, official page extraction is deliberately narrow: short sentences must explicitly
name `Track Title by Artist` and directly state the entire requested phrase,
using `features`, `uses`, `contains`, or `is`, or their explicit negative forms.
Generic piano does not establish soft piano. At most 25 quoted words per page
are retained. Existing official-source admission accepts genre, style, vocal,
instrumentation, composer, and release-date facts; other unsupported descriptive
criteria remain unknown. The structured provider also supplies explicit
recording credits and linked facts. Missing descriptions are never negatives.


For practical quotation extraction beyond the strict literal fallback, add a
local GGUF and an existing llama runtime:

```sh
bin/playlist-indexer annotate \
  --pack /tmp/playlist-ai-subset/subset.paipack \
  --state /tmp/playlist-ai-subset/annotation-cache \
  --out /tmp/playlist-ai-subset/web-quoted-hints.jsonl \
  --limit 16 --timeout 10m --criterion instrumentation:piano \
  --model /absolute/path/to/local-model.gguf \
  --runtime-dir /absolute/path/to/llama-runtime --model-threads 2
```

This reuses the existing local `llama.Parser.ExtractRecordingClaims` implementation,
with a 4096-token context, a 6000-byte source excerpt, a 20-second extraction
limit, exact-quotation validation, and at most 25 retained quoted words per page.
It starts CPU inference only when `--model` is supplied; there are no model
or runtime downloads. Here `--runtime-dir` identifies the llama executable
directory, whereas `run --runtime-dir` identifies the codec bundle. Runtime
auto-detection is available when the annotation runtime directory is omitted.
Model extractions retain their extractor version and `quoted_statement` method;
they are reviewable source hints, never independently verified musical fit.

Each JSONL row is marked `evidenceClass: "web_source"` and reuses the existing
`EnrichedTrack`, `RecordingClaim`, `ContextSource`, and `CriterionAssessment`
contracts. It includes the source pack ID/hash, recording identity, source URL,
revision, locator or quotation, extraction method, retrieval time, and criterion
assessments. Contradictory supported claims remain visible and yield unknown.
Quoted statements, including model extractions, remain unverified hints: their
criterion assessments stay unknown unless independently supported by structured
recording facts. These rows are **not independently listened human labels or audio-model
predictions** and cannot satisfy held-out listening-calibration requirements.

The sidecar is an explicit audit artifact. It is not automatically imported into
the desktop catalog or treated as training/calibration data; the current portable
pack format has no source-claim ingestion field. Do not flatten these claims into
unsourced raw tags. A future importer must preserve identity, scope, provenance,
conflicts, and evidence class before making them available to recommendation.

The output is created exclusively: existing reports are never overwritten.
Completed rows are synced individually, so cancellation can leave a useful
partial JSONL file. Use a new `--out` path for another run; the metadata cache is
reused. Ctrl-C stops new tracks and drains the current bounded source request;
a second interrupt cancels remaining work.

## Validation

Offline tests cover bounded deterministic copying, explicit selection, source
preservation, byte-budget rollback, exact identity requirements, ambiguous name
searches, qualified descriptions, conflicting claims, scope, cancellation, and
CLI sidecar round-trips. The native Linux executable is built and exercised on
synthetic FLAC input through subset, metadata-only pack export, and offline
annotation. Live subset source coverage and model encoding are separate measured
results; code tests cannot establish musical precision.

## Compare CLAP and DSP with a prompt

`playlist-indexer compare` runs a local LLM over existing sampled measurements.
It writes a diagnostic JSON report; it does not analyze audio, create listening
annotations, or alter playlist admission. Supply one recording's measurements
per invocation, preserving complete phrases and their negation or journey scope:

```json
{
  "prompt": "Soft piano with spacious reverberation",
  "criteria": ["soft piano", "spacious reverberation"],
  "coverage": "30–40 seconds; same excerpt for CLAP and DSP",
  "model_fingerprint": "COPY_FROM_THE_ACTUAL_CLAP_ANALYSIS",
  "clap_cosine": {"soft piano": 0.338, "spacious reverberation": 0.179},
  "dsp": {
    "rms_dbfs": {"value": -24.027, "state": "known"}
  }
}
```

The numbers above illustrate the input format; use the actual recording's
measurements and model fingerprint. Omitted DSP values remain unknown. Do not
combine incompatible model spaces or misrepresent sampled coverage.

```sh
bin/playlist-indexer compare --input evidence.json --out comparison.json \
  --model /path/to/local-model.gguf --runtime /path/to/llama
```

The local runtime uses a fixed JSON grammar. Every output must contain exactly
one result per original criterion in order, with `suggested`, `unclear`, or
`conflicting` overlap and an explanation. Invalid JSON or changed criteria trigger
one correction attempt under the same deadline. A second invalid response produces
a valid report with `status: "unknown"`; it never admits the malformed response.
No evidence also yields unknown without an inference call. Runtime failures and
cancellation return errors. The total deadline, including startup, defaults to two
minutes and cannot exceed it. Existing output files are never overwritten.

Reports retain the input measurements, sampled coverage and CLAP fingerprint,
`version: "audio-overlap/v1"`, `diagnostic_only: true`, and whether correction
succeeded (`repaired`). The LLM's overlap judgments are uncalibrated explanations,
not independent evidence or probabilities. Reports can contain your prompt and
should be treated as private local files.
