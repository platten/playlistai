# Automated recommendation evaluation

The later [reliable-seeds and descriptive-matching validation](reliable-seeds-validation.md) records Automatic v7, the full 40-prompt run, source evidence, and remaining promotion failures. Earlier measurements below retain their original build scope.

`cmd/automaticeval` separates independently collected labels from real model
features and engine measurements, then evaluates the resulting predictions. It
does not populate human listening grades or modify the existing forty-prompt audit. A missing result is
never a passing result. Synthetic tests establish evaluator correctness only.

## Frozen label corpus

The source is MTG's [Music Classification Annotations](https://github.com/MTG/mtg-jamendo-dataset/tree/master/derived/music-classification-annotations).
Use `music-classification-annotations-raw.tsv`: the upstream cleaned file excludes
`instrumental`, which would bias a no-vocals evaluation. Eligibility requires
exactly three identical `voice_instrumental` annotations, either `voice` or
`instrumental`. Other taxonomies are retained only when all three labels agree;
missing, disputed, or unmatched labels remain unknown.

The seeded builder selects 600 recordings, one per artist, with 150 voice and 150
instrumental recordings in each of development and heldout. It orders recording
IDs by SHA256 of the seed and ID, selects voice before instrumental, and excludes
repeated artist IDs, recording IDs, paths, and published full/low audio hashes.
This establishes separation for those identities and exact encoded duplicates;
it does not establish perceptual deduplication of different encodings or exclude
these recordings from pretrained model training sets.

```sh
go run ./cmd/automaticeval corpus \
  -annotations music-classification-annotations-raw.tsv \
  -audio-hashes raw_30s_audio_sha256_tracks.txt \
  -low-audio-hashes raw_30s_audio-low_sha256_tracks.txt \
  -seed automatic-evaluation-v1 -output corpus.json
```

The checksum metadata comes from the repository's
[download directory](https://github.com/MTG/mtg-jamendo-dataset/tree/master/data/download).
No audio is downloaded by this command. Metadata is licensed CC BY-NC-SA 4.0;
the repository code's Apache license is separate. The upstream
[license section](https://github.com/MTG/mtg-jamendo-dataset#license) specifies
noncommercial research/academic use and separate per-recording audio licenses.
Do not bundle this benchmark metadata or its labels as production ranking data.

Individual files are available on the dataset's official mirror; an archive
download is unnecessary. For an `audioPath` of `36/2636.mp3`, the low-bitrate URL
is `https://cdn.freesound.org/mtg-jamendo/raw_30s/audio-low/36/2636.low.mp3`
and the full-quality URL uses `/raw_30s/audio/36/2636.mp3`. HEAD requests on
2026-09-27 returned 200 for both; no audio was acquired in that check. Validate any
later download against its corresponding frozen manifest checksum. Individual
license/attribution records are in the upstream repository's
[audio_licenses.txt](https://github.com/MTG/mtg-jamendo-dataset/blob/master/audio_licenses.txt).
The local benchmark receipt retains all 600 selected license records.

## Real measurement producer contract

`automaticeval measure` extracts real CLAP audio and text features using the
installed verified model and decoder. It downloads one checksum-verified
low-bitrate recording at a time (32 MiB maximum), samples the indexer's two
distributed ten-second windows, and immediately removes audio and clears PCM.
Per-track failures are retained in the derived-only result and produce a nonzero
exit. This measures feature preparation, not whole-application playlist latency.

```sh
go run ./cmd/automaticeval requests -output frozen-requests.json

go run ./cmd/automaticeval measure \
  -manifest corpus.json -requests frozen-requests.json \
  -bundle /absolute/installed/clap -decoder /absolute/installed/decoder \
  -split development -limit 300 -download \
  -metadata-tsv uploader-tags.tsv \
  -metadata-url https://raw.githubusercontent.com/MTG/mtg-jamendo-dataset/master/data/raw_30s_cleantags_50artists.tsv \
  -output development-features.json
```

Start with development. Heldout extraction requires `-split heldout` plus
`-frozen-policy` with the already-frozen policy's SHA256. A smaller explicit
`-limit` is useful for setup checks but cannot stand in for the complete split.
The requests JSON is an array of `AutomaticFeatureRequest` containing a facet,
value and normalized typed intent. Requests are independent of recording labels;
text vectors use production `audio.EncodeClauseQueries`. Optional metadata is the
original MTG uploader TAGS file, whose hash and URL are retained separately. The
parser rejects crowd ANNOTATIONS files and crowd taxonomy tags. Without this
independent source, metadata remains absent. Download/inference requires the
explicit command above; corpus construction and evaluation never start it.

Run the deterministic production-engine adapter after a complete split has
features. It invokes `multichannel.NewAutomatic` for all three ablations using
the same prepared candidates and seed; it does not substitute a cosine-only
ranking for the engine:

```sh
go run ./cmd/automaticeval engine \
  -manifest corpus.json -features development-features.json \
  -producer-source PRODUCER_SOURCE_SHA256 -policy FROZEN_POLICY_SHA256 \
  -seed 42 -output development-measurements.json
```

For heldout, the feature file must carry the matching frozen policy SHA256 and
the adapter requires `-policy-frozen`. Its supplied immutable pool is a controlled
engine experiment; `retrievalScope: fixed_pool_order` does not measure catalog
retrieval recall. Its `timingScope: prepared_engine` does not measure cold provider
or whole-application latency. Those gates remain insufficient until separately
instrumented complete application runs exist. Neither command reads corpus labels
when preparing evidence or ranking recordings.

The separate producer must use actual installed model/metadata features and the
production engine. Give it a label-free projection of recording IDs, artist IDs,
audio paths and checksums. Keep annotation values solely in the evaluator. Record
the producer source SHA256, frozen policy SHA256, and immutable feature snapshot
SHA256 in `evaluation.AutomaticMeasurements`. Never set `policyFrozen` after
selecting thresholds using heldout results.

Use `evaluation.AutomaticCorpusSHA256(corpus)` for `corpusSHA256`; it hashes Go's
canonical JSON encoding of the parsed corpus, not the formatted file bytes. A
separate file hash should accompany any archived artifact. For each split, use
exactly its 300 manifest IDs as the candidate pool. Compute `candidateSHA256` with
`evaluation.AutomaticCandidateSHA256(ids)`.

Each task has six runs: metadata, audio, and combined, each under `cold_provider`
and `warm_installed` cache conditions. All six must have the same task ID, seed,
candidate hash, facet/value and requested count. Measure end-to-end milliseconds,
including partial/failed runs. A cold provider cache still has installed assets;
asset downloads are not hidden inside or described as ordinary generation.
Set `timingScope` to `whole_application` for those observations. Prepared-engine
timings use `prepared_engine` and cannot pass either whole-application latency
gate; two runs of already prepared features do not establish a cold provider
measurement. Empty scope remains accepted for the original measurement contract.

For each `AutomaticRun`, export ranked `retrieved` recording IDs, the engine's
`strongAdmissions`, and `output` entries with actual recording and artist IDs.
Export measured `factualViolations` and the named `factualChecker` for exclusions,
dates, required tracks and other task facts. Null counts or timings mean unchecked.
The evaluator independently checks output identity and duplicate recordings.
An error retains its requested-slot denominator; missing outputs contribute no
good slots. Each variant reports `runsWithErrors` separately. Usable observations
still contribute to the stated gates, so a passing quality gate does not imply
error-free execution. Labels must never be used to select candidates or alter
ranking.

```sh
go run ./cmd/automaticeval evaluate \
  -manifest corpus.json -measurements measured-runs.json -output report.json
```

Outputs are new files with mode 0600 and cannot overwrite inputs or prior reports.
Evaluation exits nonzero for failed or insufficient gates, after preserving a
valid report. JSON inputs are bounded to 64 MiB and reject unknown fields.

## Acceptance and limits

Heldout combined results determine overall acceptance. Metadata and audio
baselines are reported independently and must be present with matching inputs;
a failing baseline does not prevent a passing combined result.

- Strong-admission precision at least 0.90.
- Recall at 200 at least 0.90 against the complete labelled candidate pool.
- Good returned recordings per requested slot at least 0.80.
- One-sided exact 95% Clopper–Pearson upper vocal-leakage bound at most 0.05.
- Zero known factual, identity or recording-duplicate violations.
- Measured nearest-rank p95 and maximum at most 120,000 ms in both cache conditions.

Precision, recall and good-slot gates are also required separately for
`voice_instrumental`, `genre_dortmund`, `mood_acoustic`, `mood_electronic`,
`mood_relaxed`, and `danceability`. These taxonomies have limited granularity;
passing them would not verify arbitrary subgenres, textures, instrumentation or
whole-recording properties of short previews. An incompletely labelled pool
cannot establish recall; an unlabelled admission/output cannot establish success.
Unsupported/source-unlabelled facets remain unevaluated, not invented negatives.

Vocal leakage counts distinct recordings returned for instrumental tasks per
variant and split, deduplicated across seeds and cache conditions. At least 59
distinct clean returned recordings are needed for a zero-failure bound below 5%;
repeating ten recordings does not increase the sample size. The bound assumes
binomial observations and is conditional on this benchmark's selection.
The prespecified instrumental challenge requests 60 recordings to allow that
sample size in one run; all other task counts remain unchanged. This challenge
tests vocal leakage and is not the ordinary ten-track latency workload.

On 2026-09-27, bounded metadata acquisition found 2,313 unanimous voice and 6,410
unanimous instrumental rows. A 600-recording corpus with 600 distinct artists was
created in the local benchmark workspace. All 600 have vocal labels, while only
23 have unanimous `genre_dortmund` labels; other facets also have missing labels.
This corpus therefore cannot yet clear general-fit recall gates.

## Development measurements — 2026-09-27

All 300 development recordings passed checksum validation, decoding and feature
extraction, with two ten-second windows each (6,000 seconds of sampled audio).
The installed original HTSAT-base model ran on ONNX Runtime 1.26.0 CPU. Mean
per-recording acquisition/extraction time was 841 ms on an Intel Core Ultra 9
285H, 16 logical CPUs, approximately 15.4 GiB RAM, Linux/WSL2. This does not
measure whole-application generation latency or full-song coverage.

The production Automatic engine then ran all 15 frozen tasks against the same
300 candidates and seed 42 for each of the three variants (45 runs). No run
reported an execution error. The final replay used `automatic-fit/v3`, with the
same extracted features, recording IDs, tasks and seeds as the earlier v2 run.
The measured gate counts were unchanged; these taxonomy tasks have no artist
references and do not test the new reference-admission rule.

| Development variant | Returned / requested slots | Independently labelled good outputs | Unlabelled outputs |
| --- | ---: | ---: | ---: |
| Metadata only | 0 / 200 | 0 | 0 |
| Audio only | 0 / 200 | 0 | 0 |
| Combined | 80 / 200 | 4 | 76 |

Combined evidence admitted 243 strong task/recording pairs: six had matching
independent labels and 237 were unlabelled. These counts cannot establish 90%
precision. All returned slots were genre tasks; vocal, acoustic, electronic
texture, relaxed mood and danceability tasks returned none. The benchmark
adapter currently supplies raw cosine evidence, not signed vocal contrasts;
raw vocal similarity correctly cannot establish instrumental admission. No
instrumental output means the vocal-leakage bound is insufficient, not zero.

The development variants fail or lack evidence for the required gates. The
overall release report is `insufficient`: heldout was not run, policy was not
frozen for heldout, the supplied candidate order does not establish retrieval
recall, and prepared-engine timings do not establish either application cache
condition. Keep the heldout split unused while repairing these development
limitations. No musical-quality or whole-application performance acceptance is
claimed.

The [aggregate measurement receipt](data/automatic-development-2026-09-27.json)
records the source, model and artifact hashes alongside the gate counts. Audio,
per-recording annotations and feature vectors remain outside the repository.
