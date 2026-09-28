# Discogs-EffNet specialist evidence

Automatic can consume uncalibrated specialist class scores from prepared music
packs. The offline preparation command runs the official Discogs-EffNet encoder
once per sampled source, then applies the instrument, vocal/instrumental,
relaxed-mood, and Discogs-style heads to the same embeddings. The desktop also
offers a separate native ONNX download in first-run setup and Settings. It runs
the original encoder and heads in an isolated Go worker on verified Deezer
previews while CLAP analyzes that preview. No Python process or model-host
request runs during generation. Prepared pack evidence remains useful without
the desktop classifier download.

The supported outputs are 40 instrument labels, two vocal/instrumental labels,
two relaxed/non-relaxed labels, and 400 Discogs styles. Complete ordered score
arrays are preserved, alongside hashes of weights and metadata, runtime and
preprocessing identities, original source identity, permissions, and measured
audio intervals. Several heads sharing an encoder count as one evidence family.
Their scores are estimates, not calibrated probabilities or factual labels.
Even a score of 1 cannot satisfy a strict requirement or clear an exclusion.

The exact facet scorer maps `vocal`/`vocals` to the upstream `voice` label and preserves
the instrument vocabulary's concatenated names (`acoustic guitar` to
`acousticguitar`). It accepts the entire Discogs style or parent and the
grammatical forms `relax`, `relaxing`, and `relaxed`. A qualified phrase such as
`soft piano` or `spacious reverberation` remains unknown to these heads; ranking
the complete phrase uses text/audio similarity. Missing labels remain unknown.

## Preparation

Use a separate preparation environment. The verified platform is Python 3.14
on Linux x86_64 (including WSL); do not install these dependencies into the
desktop application environment.

```sh
python3 -m venv /tmp/playlistai-effnet-env
/tmp/playlistai-effnet-env/bin/pip install -r python/requirements-discogs-effnet.txt
python3 python/prepare_discogs_effnet.py setup --models /path/to/music-classifiers
python3 python/prepare_discogs_effnet.py setup --models /path/to/music-classifiers --verify-only
```

Setup uses the existing resumable downloader from
`python/fetch_enhanced_audio.py`. Every graph, ordered-vocabulary metadata file,
and original notice has a pinned byte length and SHA-256 in
`python/discogs-effnet-sources.json`. The eleven source assets total about 24 MB;
they are not checked into the repository. Existing mismatched files are preserved
and rejected. The preparation runtime is optional and separate from the
additional installed desktop data ceiling.

Supply permitted local **mono PCM16 WAV at 16 kHz**, prepared through an
authorized audio path. The command never downloads audio. Each input JSONL row
has the following shape (replace every placeholder with a real source fact):

```json
{"catalogSha256":"<exact catalog archive SHA-256>","trackId":"<exact catalog track ID>","path":"source.wav","audioSha256":"<WAV SHA-256>","source":"<dataset/provider>","sourceId":"<stable recording/source ID>","license":"<source audio terms>","derivedLicense":"<permitted derived evidence terms>","redistributionAllowed":true,"startSeconds":0,"completeRecording":false}
```

The catalog hash and exact track ID bind the supplied join. A title or artist
name is not identity verification. `redistributionAllowed` must explicitly
affirm permission for the derived evidence; it is a maintainer declaration,
not a license inference performed by the application. Keep the source manifest
with the preparation job so original audio terms remain auditable.

```sh
/tmp/playlistai-effnet-env/bin/python python/prepare_discogs_effnet.py prepare \
  --models /path/to/music-classifiers \
  --manifest /path/to/verified-audio.jsonl --audio-root /path/to/permitted-audio \
  --cache /path/to/preparation-cache --out /path/to/classifier-evidence.jsonl \
  --max-seconds 60 --max-records 1000
```

Preparation is serial and bounded to 3–60 seconds per source. It verifies the
audio checksum before using a checkpoint, runs one encoder pass for all heads,
and averages each class across observed patches. Input PCM and intermediate
embeddings are cleared after inference. Audio files remain in the explicitly
supplied source directory and are never copied into the cache or pack.

Atomic per-record checkpoints bind source rows, model lock, preprocessing,
runtime, and sample length. Rerun the same command after interruption; completed
records skip model loading and inference. Replacing a source or model invalidates
the cache. Reused checkpoints are checked against the complete source, model,
runtime, preprocessing, observed coverage, ordered vocabulary and valid score
range; a matching lookup key alone is insufficient. Output JSONL contains
`catalogSha256`, `trackId`, `classifierEvidence`,
and `preparationKey` for exact-identity pack ingestion. A changed catalog hash
must be rejected by the importer. Resume with an expanded `--max-records` limit
to process more source rows.

Coverage intervals are relative to the supplied audio source. Padding repeated
to fill model patches never increases coverage. Whole-recording coverage is
claimed only when `completeRecording` is explicitly true and all source frames
were observed. Otherwise coverage is marked incomplete.

## Licensing and native compatibility

Primary sources:

- [Official model catalog](https://essentia.upf.edu/models.html).
- [Original encoder metadata](https://essentia.upf.edu/models/feature-extractors/discogs-effnet/discogs-effnet-bs64-1.json).
- [Instrument metadata](https://essentia.upf.edu/models/classification-heads/mtg_jamendo_instrument/mtg_jamendo_instrument-discogs-effnet-1.json),
  [vocal metadata](https://essentia.upf.edu/models/classification-heads/voice_instrumental/voice_instrumental-discogs-effnet-1.json),
  [relaxed metadata](https://essentia.upf.edu/models/classification-heads/mood_relaxed/mood_relaxed-discogs-effnet-1.json),
  [style metadata](https://essentia.upf.edu/models/classification-heads/genre_discogs400/genre_discogs400-discogs-effnet-1.json).
- [Official preprocessing](https://essentia.upf.edu/reference/std_TensorflowPredictEffnetDiscogs.html).

The models page states **CC BY-NC-SA 4.0**, while the linked
[model LICENSE](https://essentia.upf.edu/models/LICENSE) names **NC-ND** and links
to **NC-SA** legal terms. The
[general licensing page](https://essentia.upf.edu/licensing_information.html)
also says NC-ND. Retain the original notice and the recorded discrepancy in
`python/licenses/DISCOGS-EFFNET-MODEL-NOTICE.txt`. The supported path uses original
models for noncommercial preparation; this implementation does not settle the
conflicting adaptation terms or grant rights to source audio or derived data.
Resolve terms with UPF before distributing adapted models or commercial use.

The [official encoder directory](https://essentia.upf.edu/models/feature-extractors/discogs-effnet/)
and [classification head directories](https://essentia.upf.edu/models/classification-heads/)
publish original ONNX files. The desktop downloads the dynamic encoder, three
specialist heads, four ordered-class metadata files, and the original model
notice directly from Essentia. Their pinned total is 21.85 MB. They are
verified by length and SHA-256, counted under the additional 10 GB prepared
data ceiling, and stored under `music-classifiers/discogs-effnet-v1`. The
download resumes from verified partial files. Native inference reuses the
installed CLAP ONNX Runtime, so CLAP must be installed first. Removing the
Discogs files leaves existing CLAP/MERT models intact.

The worker accepts 16 kHz mono PCM from the observed preview interval, computes
the EffNet log-mel patches, runs one encoder pass, and averages the 444 ordered
class scores. On startup it checks a fixed synthetic audio fixture against
recorded TensorFlow/Essentia outputs before making the model available.
During generation, completed scores are stored locally with model,
preprocessing, runtime, preview identity, audio hash, source and measured
coverage. An existing CLAP preview cache without Discogs scores is still
unknown; the app does not redownload it solely to backfill classification.
Scores only guide estimated ranking, never strict admission. The original
TensorFlow and ONNX exports count as one evidence family.

The native worker and installer were executed on Linux x86_64 with the installed
ONNX Runtime 1.26.0. Windows and macOS native inference remain unexecuted host
checks; the platform-specific CLAP runtime and worker are required there.
Windows preparation can use the verified WSL Linux path.

Encoder training used the unreleased Discogs-4M collection; the instrument head
uses MTG Jamendo instrument labels, and the two binary heads use MTG collections.
Their training overlap with public evaluation sets is unresolved. These heads
must not establish strong-match calibration or model quality claims from
overlapping labels.

## Verification

Offline regressions require no models or audio downloads:

```sh
go test ./internal/core -run Classifier
go test ./internal/audio -run Discogs
python3 -m unittest discover -s python -p 'test_prepare_discogs_effnet.py'
```

An explicit real-model smoke verifies all output dimensions, finite score
ranges, encoder reuse, and repeated predictions to six decimal places:

```sh
PLAYLISTAI_DISCOGS_MODELS=/path/to/music-classifiers \
  /tmp/playlistai-effnet-env/bin/python -m unittest discover -s python \
  -p 'test_prepare_discogs_effnet.py'
```

On 2026-09-27, this check passed with the pinned original assets and Essentia
2.1b6.dev1438 / NumPy 2.5.3 / Python 3.14.7 on an Intel Core Ultra 9 285H,
16 visible CPUs, Linux x86_64 / WSL2 kernel 6.18.33.2. The working tree was based
on `7b608bfccc5543b7221dce4db9000845a670ec13` with this implementation uncommitted.
CPU was explicitly selected. A generated eight-second 440 Hz PCM fixture
produced all 444 class scores. Fresh-process preparation took **2.35 seconds**
with **777,376 KiB peak RSS**; a cached replay with full checkpoint validation
took **0.11 seconds** with **27,196 KiB peak RSS**. Three consecutive warm
eight-second encoder/head calls took 0.796448, 0.767565 and 0.716501 seconds.
These are one-shot synthetic engineering observations,
not musical-quality evidence, catalog coverage, native ONNX measurements, or
full Automatic generation latency. No held-out calibration is implied.

The native Linux x86_64 worker was also run against the same eight-second
synthetic tone. Its first instrument, vocal and relaxed-mood scores were
0.007834924, 0.983692944 and 0.133184984; the matching Essentia/TensorFlow
outputs were 0.00783490, 0.98369294 and 0.13318640. This checks numerical
compatibility on that fixture, not musical quality or platform-wide parity.
