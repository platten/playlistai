# Automatic matching: frozen comparisons and challenger status

CLAP remains the installed scorer. An eight-recording native CLAP baseline and
four production Automatic cold/warm runs were measured; MuQ-MuLan was not installed or compared. No musical-quality improvement,
strong-match calibration, or model replacement is established by this work. The
[availability and baseline receipt](data/automatic-matching-availability-2026-09-27.json)
records actual model checksums, metadata sources, measurements and limitations.

`python/automatic_matching_eval.py` adds an offline comparison path alongside
[`cmd/automaticeval`](automatic-evaluation.md). The existing Go tool continues to
own production-engine ablations and strict quality gates. The new helper prepares
caption/facet/reference benchmarks and evaluates two independently produced score
receipts on identical inputs. It uses only the Python standard library and never
downloads a model, reads a user library, or changes the enabled scorer.

## Human evidence and source terms

| Source | Evaluation use | Limits and license |
| --- | --- | --- |
| [Song Describer](https://github.com/mulab-mir/song-describer-dataset) | Preserve complete validated human captions; rank their associated recordings. | Dataset CC BY-SA 4.0; individual audio licenses remain separate. Source association is a retrieval target, not a judgment that every other recording is unsuitable. |
| [MTG Music Classification Annotations](https://github.com/MTG/mtg-jamendo-dataset/tree/master/derived/music-classification-annotations) | Explicit unanimous positive/negative facet labels through the existing `automaticeval corpus` builder. | Use raw annotations: the cleaned release removes instrumental labels. Missing/disagreed labels remain unknown. [MTG terms](https://github.com/MTG/mtg-jamendo-dataset#license) distinguish noncommercial metadata from per-recording audio rights. |
| [CCMSim](https://github.com/pxaris/CCMSim) | Rank available reference pairs by published overall similarity ratings. | Annotations CC BY 4.0; code MIT. Audio is not redistributed by CCMSim and requires the individual source collection's access and terms. Preserve mapped 20-second intervals. |

The local metadata audit downloaded only the named public metadata files. The
frozen Song Describer CSV contains 1,106 captions for 706 recordings; 746 validated
captions refer to **547 recordings and 169 artists**. The upstream README's
summary says 546 validated recordings; use the receipt's file hash and observed
count for reproduction. All 547 validated recording IDs also occur in the MTG
classification file. These are overlapping benchmark sources, not independent
corroboration. The [Song Describer datasheet](https://github.com/mulab-mir/song-describer-dataset/blob/main/docs/datasheet.md)
places its source recordings in MTG split-0 test and discourages training use.

The frozen CCMSim files contain 1,130 pairs, a mapping of 486 clips, and 463 clip
IDs actually used by those pairs. The README reports 468 clips; the file-level
counts are retained separately. Every mapped interval is 20 seconds. Accessible
subsets must be selected before seeing model scores, retain their source balance,
and report missing pairs. Missing source access cannot silently shrink a previously
frozen comparison. Shared artists, clips and related versions require grouping.

## Preparing a benchmark

First create an audio manifest from the actual permitted audio used by both
producers. Each item has the following shape; `audioSHA256` hashes the encoded
source file, and coverage uses offsets in that file:

```json
[
  {
    "id": "EXACT_DATASET_RECORDING_ID",
    "artistId": "EXACT_DATASET_ARTIST_ID",
    "audioSHA256": "LOWERCASE_64_HEX_DIGEST",
    "license": "EXACT_RECORDING_LICENSE_AND_ATTRIBUTION_REFERENCE",
    "coverage": [{"startMs": 0, "endMs": 20000}]
  }
]
```

Use exact identity joins. Do not join by similar artist/title text. Duplicated
encoded audio is rejected. Deduplicate alternate encodings and related versions
in the independently reviewed selection manifest as well. CCMSim additionally
requires `sourcePath` matching its official mapping; the importer verifies the
rated interval. MuQ and CLAP may resample differently, but must receive the same
source bytes and source intervals. Keep duration, decoder and preprocessing
versions in each model's producer receipt.

```sh
python3 python/automatic_matching_eval.py prepare --kind song_describer \
  --source song_describer.csv --audio-manifest sdd-audio.json \
  --source-url https://zenodo.org/records/10072001/files/song_describer.csv \
  --source-license 'CC BY-SA 4.0; audio licenses separate' --output captions.json

python3 python/automatic_matching_eval.py prepare --kind mtg \
  --source mtg-corpus.json --audio-manifest mtg-audio.json --split heldout \
  --source-url https://github.com/MTG/mtg-jamendo-dataset/tree/master/derived/music-classification-annotations \
  --source-license 'CC BY-NC-SA 4.0; audio licenses separate' --output facets.json

python3 python/automatic_matching_eval.py prepare --kind ccmsim \
  --source human_similarity.csv --audio-manifest ccmsim-audio.json \
  --mapping audio_annotations_mapping.csv \
  --source-url https://github.com/pxaris/CCMSim/tree/main/data/annotations \
  --source-license 'CC BY 4.0; audio licenses separate' --output references.json
```

`mtg-corpus.json` is the existing `automatic-mtg-corpus/v1` output. Its published
full/low audio hashes must match the supplied recordings. The importer currently
covers instrumental, vocal, acoustic, electronic, relaxed and danceable labels.
It does not infer “soft piano” or “spacious reverberation” from those taxonomies.
Corpus files record unavailable source items; they contain human labels and stay
outside production ranking data.

## Freeze before measurement

A specification contains the fields below. Digest placeholders must be replaced
with actual hashes. Both roles need their own complete model object; a policy
comparison may share weights while pinning different scorer policy hashes.

```json
{
  "split": "heldout",
  "corpora": {"captions": "captions.json", "facets": "facets.json", "references": "references.json"},
  "primaryCorpus": "captions",
  "primaryMetric": "mrr",
  "k": 10,
  "bootstrapSamples": 10000,
  "bootstrapSeed": "automatic-matching-heldout-v1",
  "minimumClusters": 30,
  "runSeed": "18446744073709551615",
  "cacheCondition": "warm_installed",
  "startingCacheSHA256": "SHA256_OF_IDENTICAL_STARTING_CACHE_SNAPSHOT",
  "assetsSHA256": "SHA256_OF_SHARED_AUDIO_AND_ASSET_MANIFEST",
  "producerSourceSHA256": "SHA256_OF_FROZEN_PRODUCER_SOURCE",
  "selectionPolicySHA256": "SHA256_OF_LABEL_BLIND_SELECTION_POLICY",
  "models": {
    "baseline": {"id": "INSTALLED_CLAP_ID", "fingerprint": "SHA256", "weightsSHA256": "SHA256", "policySHA256": "SHA256", "preprocessingVersion": "PINNED_VERSION", "license": "MODEL_TERMS"},
    "challenger": {"id": "OpenMuQ/MuQ-MuLan-large@PINNED_REVISION", "fingerprint": "SHA256", "weightsSHA256": "SHA256", "policySHA256": "SHA256", "preprocessingVersion": "PINNED_VERSION", "license": "CC BY-NC 4.0"}
  },
  "trainingOverlapAudits": {
    "captions": {"status": "unknown", "artifactSHA256": "SHA256", "notes": "Record checked model-training inventories and unresolved sources."},
    "facets": {"status": "unknown", "artifactSHA256": "SHA256", "notes": "Record dataset/model-head training overlap."},
    "references": {"status": "unknown", "artifactSHA256": "SHA256", "notes": "Audit each available source collection."}
  },
  "requiredPlatforms": ["linux-amd64", "windows-amd64", "darwin-arm64"],
  "nativeLimits": {"additionalInstalledBytes": 10000000000, "peakRSSBytes": 4000000000, "p95InferenceMs": 5000, "maxParityError": 0.001}
}
```

The resource numbers above are example **acceptance limits**, not measurements.
Set them before evaluation for the intended supported platforms. Freeze separate
cold-provider and warm-installed protocols. Neither is a cold model download.

```sh
python3 python/automatic_matching_eval.py freeze specification.json --output frozen.json
python3 python/automatic_matching_eval.py project frozen.json --output producer-inputs.json
```

`freeze` embeds and hashes the parsed corpora, including complete query wording,
audio intervals, labels, model identities, seeds and constraints. Archive that
receipt before producing either run. `project` emits only the audio identities
and query text/reference IDs needed for inference. Give producers this projection;
keep the label-bearing frozen protocol in the evaluator. Do not tune against the
heldout results or later rename a development experiment as heldout.

Each producer writes a receipt with `version: automatic-matching-evaluation/v1`,
`protocolSHA256`, its exact frozen `model` object, and matching `runSeed`,
`cacheCondition`, `startingCacheSHA256`, `assetsSHA256`, `producerSourceSHA256`.
`scores` is a map `corpus name -> task ID -> candidate recording ID -> finite
numeric score`. Every frozen task and candidate must appear; exclude the reference
itself for reference queries. Failures or missing candidates reject the comparison,
so they cannot improve a metric by vanishing. Raw scores are ranked within each
model; incompatible spaces are never numerically averaged. Equal scores use
recording ID as a deterministic tie break.

The challenger may also provide `nativeMeasurements`, keyed by the declared
platforms. Each entry requires `receiptSHA256`, matching `modelFingerprint`,
`nativeExecution: true`, `downloadVerified: true`, `licenseNoticesPresent: true`
and measured `additionalInstalledBytes`, `peakRSSBytes`, `p95InferenceMs`,
`maxParityError`. The referenced receipt must describe hardware, versions, actual
native execution, sampling, repeated runs and ONNX/original parity. An unchecked
boolean or cross-compilation is not evidence. Missing native evidence retains the
current scorer even if a ranking comparison is positive.

```sh
python3 python/automatic_matching_eval.py compare frozen.json \
  clap-observations.json muq-observations.json --output comparison.json
```

All outputs are new files with mode 0600; existing files are never overwritten.
Inputs are bounded to 64 MiB. Exit 1 means invalid inputs; exit 2 means a retained
valid report with failed/insufficient replacement gates. Exit 0 means the report
is eligible for human review, never automatic installation.

## Metrics and inference limits

The recommended primary outcome is caption retrieval MRR: the reciprocal rank
of each caption's associated recording, averaged within artist and then across
artists. A paired percentile bootstrap resamples the same artist clusters for
both models and reports the 2.5th/97.5th percentiles. This avoids treating multiple
captions of the same artist as independent observations. At least 30 independent
clusters and a strictly positive lower bound are required for replacement review.
This threshold is a floor, not a claim of adequate power for every effect size.

MTG and CCMSim report graded nDCG at the frozen `k`, restricted to explicitly
judged candidates. Unknowns are excluded rather than assigned a zero grade;
unknown counts and unavailable source counts remain visible. Queries with no
known positive grades are unevaluable. MTG tasks share the same recordings and
are conservatively one cluster. CCMSim shared-clip/artist connected components
stay in one cluster; a fully connected corpus cannot supply a spurious narrow
interval by resampling its dependent pairs. These secondary checks do not add
independent weight to the caption primary. User-visible estimated coverage and
strong-match precision remain separate from all of these retrieval metrics.

Training-overlap audits support `unknown`, `no_detected_overlap`, or
`known_overlap_excluded`, with an evidence artifact and explanation. “No detected”
does not prove absence from undisclosed pretraining. Current training inventories
are insufficient; the audit remains unknown and blocks replacement. Reuse of MTG
labels for both model-head training and evaluation must be called out explicitly.

## MuQ-MuLan adoption boundary

The [official implementation](https://github.com/tencent-ailab/MuQ) documents a
roughly 700M-parameter bilingual music/text model, strict 24 kHz input, MIT code
and CC BY-NC 4.0 weights. Its [official model card](https://huggingface.co/OpenMuQ/MuQ-MuLan-large)
provides PyTorch inference. No official native export was identified in this
review. Do not infer desktop compatibility or memory consumption from parameter
count. An existing MERT export is not a MuQ export.

Before desktop adoption, provide a pinned verified download, validated native
audio and text exports, tokenizer/preprocessing parity, notices, measured target
platform memory and latency, and total additional installed data at or below
10 GB. Use identical source audio, intervals and complete requests for CLAP and
MuQ. A Python research run can provide ranking evidence; it cannot satisfy native
runtime/packaging checks. CLAP and optional MERT stay available.

## Executed baseline, 2026-09-27

The producer was built from the coordinator's pre-change source archive
(`82578d75883d78dd67418b3cdf4f10963f0170b30540a41f2536220785b99076`).
The isolated workspace is `/tmp/playlist-ai-matching-evaluation`; it retains
metadata, derived features and the hashed summary/audit scripts. The source
archive and derived feature hashes are in the receipt. Audio was removed after
feature extraction; no user caches, credentials or playlists were accessed.

```sh
go build -o "$EVAL_ROOT/automaticeval" ./cmd/automaticeval
"$EVAL_ROOT/automaticeval" corpus \
  -annotations "$EVAL_ROOT/music-classification-annotations-raw.tsv" \
  -audio-hashes "$EVAL_ROOT/raw_30s_audio_sha256_tracks.txt" \
  -low-audio-hashes "$EVAL_ROOT/raw_30s_audio-low_sha256_tracks.txt" \
  -seed automatic-matching-baseline-2026-09-27 -output "$EVAL_ROOT/mtg-corpus.json"
"$EVAL_ROOT/automaticeval" requests -output "$EVAL_ROOT/frozen-requests.json"
"$EVAL_ROOT/automaticeval" measure \
  -manifest "$EVAL_ROOT/mtg-corpus.json" -requests "$EVAL_ROOT/frozen-requests.json" \
  -bundle "$INSTALLED_VERIFIED_CLAP" -decoder "$VERIFIED_V2_DECODER" \
  -split development -limit 8 -download -output "$EVAL_ROOT/development-clap-features.json"
```

Eight checksum-verified development recordings yielded 160 sampled seconds with
zero extraction errors; 26,004,969 bytes of audio were fetched. CLAP worker health
took 2,103 ms. Per-track acquisition/decode/inference times had a 783.5 ms median;
these include network time and do not measure whole generation or pure inference.
The initial older decoder was correctly rejected; the successful run used the
already available verified FFmpeg/Chromaprint v2 bundle.

Known-label-only nDCG@10 was 0.852 instrumental, 0.774 voice, 1.000 acoustic,
1.000 electronic, 0.693 relaxed, and 1.000 danceable. These are **eight-recording
development observations**, with only one positive and one negative danceability
label and six unknowns. They are unsuitable for promotion, calibration, broad
quality claims or comparisons to the earlier 300-recording experiment. The score
policy used the frozen positive production clause vector and duration-weighted
segment dot products; it did not run the production recommendation engine.

The model bundle artifact hashes were verified locally: CLAP totals 807,374,279
bytes and MERT 401,467,084 bytes, excluding unrelated assets. No MuQ weights,
validated export or verified installer entry was available. Song Describer and
CCMSim audio were not acquired. Consequently there is no matched MuQ comparison,
paired quality interval, native challenger timing or memory measurement, or
Windows/macOS execution result. The current scorer is retained.

Software checks: `python3 -m unittest discover -s python -p test_automatic_matching_eval.py`
covers immutable inputs, missing scores, unknown labels, dependency clusters,
caption wording, CCMSim intervals, label-free projections, native blockers and
deterministic paired intervals. Synthetic fixtures verify the evaluator only.

## Production Automatic cold/warm observations

The [runtime receipt](data/automatic-matching-runtime-2026-09-27.json) records four
actual desktop-pipeline CLI runs of `automatic/v8+automatic-fit/v5`, using one
binary and the existing installed public catalog, discovery pack, original LAION
CPU CLAP and optional MERT. Each case had its own temporary store, initially
containing no mutable provider/analysis caches, listening history or credentials.
No new graph or classifier pack was activated. Large immutable files were reused
through hard links; real user stores were not modified.

| Request and condition | Diagnostic startup | Generation | Returned | Stop reason |
| --- | ---: | ---: | ---: | --- |
| Soft piano / spacious reverberation / reflective atmosphere, cold | 88.862 s | 15.744 s | 0/10 | `preparation_stopped` |
| Same description, warm replay | 67.232 s | 115.005 s | 9/10 | `preparation_stopped` |
| Similar to Radiohead including other artists, cold | 74.640 s | 12.711 s | 10/10 | `prepared_obligations_met` |
| Same reference, warm replay | 66.129 s | 1.054 s | 10/10 | `prepared_obligations_met` |

Generation stayed within the 120-second limit. Startup is separately measured
from process launch to the CLI's initial receipt, with 100 ms polling resolution;
it includes model health checks and diagnostic asset/status hashing, so it is
**not ordinary GUI time-to-ready**. The four process durations totaled 441.819
seconds. All processes exited successfully, without the external watchdog firing.
The harness reported no parser, interpretation or constraint findings.

The fresh rules interpretation preserved all three complete descriptions as
`essential` but non-strict musical clauses. The warm description result explicitly
reported “Best estimates — some qualities are unconfirmed,” alongside incomplete
preparation. Its 42 candidates came from metadata retrieval, with nine newly
analyzed CLAP previews and four selected artists. The first retrieval returned
zero candidates at its eight-second limit; the second retained 42 completed
candidates at that limit, then spent 99.632 seconds in online evidence preparation.
The installed pack has 252,491 CLAP vectors, but they belong to the older CUDA
`laion/larger_clap_music` model. They cannot serve text queries from the installed
original LAION CPU model. These observations expose a remaining compatible-data
and retrieval-latency limitation; they do not establish that the nine estimates
sound like the request.

Radiohead resolved to its exact artist identity with `preferred` reference
strength. Both runs returned ten distinct other artists and retained the
`reference_fit_estimated` uncertainty outcome. Cold/warm retrieval took
497/454 ms, batch preparation 347/339 ms, and final assembly 91/87 ms. Both stopped
before provider acquisition was necessary. The cold run additionally spent
10.824 seconds in intent preparation; warm replay supplied the already resolved
intent. The total-time difference therefore cannot be attributed solely to
provider caches, or used as a model-inference speed claim.

For each pair, the final resolved intent hash, lossless seed and profile snapshot
were identical; the warm starting mutable files exactly matched the cold ending
files. The description seed was `8649349911664434966`, and the reference seed was
`1782523484729933899`. Cold used `GenerateFromPrompt` with the current rules parser;
warm used `BuildPlaylist` with the frozen resolved input and seed, recomputing the
playlist. The original v7 run's resolved input artifact was unavailable, so this
is not a matched v7/v8 production comparison.

The receipt preserves complete commands, resolved inputs, binary and source
inventory hashes, installed asset identities, mutable cache hashes, phase timings,
provider response aggregates, and the raw diagnostic/report hashes. Reproduction
uses the existing CLI, after preparing equivalent isolated immutable assets and
fresh neutral preferences:

```sh
GOMAXPROCS=2 go build -p 2 -o "$RUNTIME_ROOT/musiccheck-v8" ./cmd/musiccheck
"$RUNTIME_ROOT/musiccheck-v8" -mode automatic \
  -app-data-dir "$CASE_ROOT/state" -catalog "$CASE_ROOT/state/catalog" \
  -prompts "$RUNTIME_ROOT/prompts.json" -case "$EXACT_PROMPT" \
  -case-timeout 120s -cleanup-timeout 10s -eval-split development \
  -variant automatic-v8-runtime-description -cache-condition cold \
  -output "$CASE_ROOT/cold.json" -diagnostics "$CASE_ROOT/cold-diagnostics.json" \
  -supervised-child
```

Repeat the same command with `-cache-condition warm`, separate output/diagnostic
paths, and `-replay "$CASE_ROOT/cold.json"`. The exact two public prompts and
neutral preferences are recorded in the receipt/workspace. The external runner
bounded each process to 300 seconds and the complete sequence to 720 seconds.

These are single observations on a shared Linux/WSL host, with an unflushed OS
page cache and a new application process each time. The first cold description
run overlapped the end of the repository gate and another bounded public metadata
preparation job; that provider job ended before warm generation began, although
warm startup overlapped its final seconds. Warm generation and both reference
generations had no such provider workload. Cold means empty mutable application caches, not a
cold disk, unloaded model download or controlled hardware experiment. No musical
quality, statistical speed improvement, Windows/macOS performance, strong-match
calibration or MuQ replacement claim follows from these measurements.
