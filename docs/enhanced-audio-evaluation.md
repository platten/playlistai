# Offline enhanced audio comparison

`cmd/enhancedeval` evaluates a frozen cohort of derived vectors. It performs no
downloads, model training, inference, or audio retention. End users do not need
Python. This optional maintainer tool produces reproducible neighbor lists and,
when independently held-out adjacency pairs exist, retrieval metrics.

Build and run on Windows:

```powershell
go build -o .\bin\enhancedeval.exe .\cmd\enhancedeval
.\bin\enhancedeval.exe -input C:\Users\pawel\Downloads\playlistai-enhanced-audio\cohort.json -top 5 > C:\Users\pawel\Downloads\playlistai-enhanced-audio\evaluation.json
```

Build and run on Linux or macOS:

```sh
go build -o bin/enhancedeval ./cmd/enhancedeval
bin/enhancedeval -input /path/to/cohort.json -top 5 > /path/to/evaluation.json
```

## Preparing the cohort

Use the offline preparation and parity procedures in
[enhanced-audio-data-preparation.md](enhanced-audio-data-preparation.md) and the
MERT export instructions. Export only final derived embeddings for audio you
are authorized to analyze. Use accessible source PCM or verified authorized
Deezer previews; preserve the original source inventory in Downloads. The
evaluator does not fetch audio and does not require labels for neighbor lists.

Each track needs a stable catalog ID and a distinct recording identity. Pin each
embedding space in `spaces`: include model revision, preprocessing, pooling,
dimension, model weights hash and runtime identity where applicable. A cohort
must contain only one compatible identity per space. Compare identical observed
audio intervals for CLAP and MERT. Do not mix preview embeddings with full-track
embeddings without describing that limitation in `provenance`.

The schema is illustrated below with **synthetic two-dimensional vectors**, which
test the command but provide no evidence about actual music:

```json
{
  "version": 1,
  "name": "synthetic-command-check",
  "provenance": "Synthetic vectors only; no musical quality conclusion",
  "spaces": {
    "catalogAudio": "synthetic/catalog-a/v1",
    "catalogCooccurrence": "synthetic/catalog-c/v1",
    "clap": "synthetic/clap/v1",
    "mert": "synthetic/mert/v1"
  },
  "tracks": [
    {"id":"a","recordingId":"a","catalogAudio":[1,0],"catalogCooccurrence":[1,0],"clap":[1,0],"mert":[1,0]},
    {"id":"b","recordingId":"b","catalogAudio":[0,1],"catalogCooccurrence":[0,1],"clap":[0.8,0.2],"mert":[0.9,0.1]}
  ],
  "heldOutAdjacency": [{"from":"a","to":"b"}]
}
```

Omit unavailable vector fields. Do not substitute zero vectors. Limits are
2–256 tracks, 2,048 dimensions per space, 1,024 directed adjacency pairs, and a
64 MiB JSON file. Unknown JSON fields, incompatible dimensions, repeated track
or recording IDs, zero vectors, and malformed pair IDs are rejected. Output
contains no timestamps, so identical input bytes and policy produce identical
reports. Preserve `inputSha256`, `policySha256`, the full policy and pinned spaces
alongside the source inventory. Byte-level input changes intentionally change
the input hash.

## What the report means

All methods use the same complete-case candidate pool: tracks with all four
vectors. Missing tracks and adjacency pairs are reported as excluded/skipped,
not assigned favorable or unfavorable scores. Each query excludes its own
recording. Ties resolve by track ID, and `-top` only truncates displayed neighbors;
held-out ranks always use the full comparable pool.

The methods are:

- `catalog`: 0.5 catalog-audio cosine + 0.5 cooccurrence cosine.
- `clap`: audio-to-audio CLAP cosine within the pinned CLAP space.
- `mert`: audio-to-audio MERT cosine within the pinned MERT space.
- `hybrid`: 0.85 catalog score + 0.15 MERT score.

The hybrid is a deliberately frozen **transition comparison proxy**. It is not
the desktop's complete ranking policy, which additionally handles intent, DSP,
taste, exclusions, diversity, required tracks and sequencing constraints. No
CLAP vector is ever compared with a MERT or catalog vector.

Optional adjacency metrics are mean reciprocal rank, mean rank, recall at one,
and recall at five, averaged over evaluated directed pairs. Without held-out
pairs, the tool reports neighbors only. Keep adjacency pairs separate from model
selection, prompt construction and weight tuning. Obtain them from an authorized
independent source; the tool does not create or claim supervised labels.

Complete-case selection can bias coverage, duplicate recordings can leak across
splits despite supplied identities, and small or synthetic cohorts do not
establish musical superiority. Cosines are not probabilities. Report cohort
size, source/preview coverage, missingness, pinned versions and held-out split
provenance with any findings. Compare rank/sequence CPU benchmarks separately
from model inference latency, peak memory and subjective musical quality.

## Validation

```sh
go test ./cmd/enhancedeval
go test ./internal/reco/multichannel -run '^$' -bench BenchmarkEnhancedPolicy -benchmem
```

The command tests use synthetic vectors and validate arithmetic, common-pool
coverage, deterministic output and malformed-input rejection. They do not
constitute held-out musical-quality validation or justify changing production
weights. No automatic winner or superiority assertion is emitted.
