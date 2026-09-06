# Semantic Sidecar Pilot

## Scope and grounding

The shipped catalog has 956,917 real Spotify track IDs, artist/title text, remote
30-second preview URLs, and two 100-dimensional Deej-AI vectors. It has no
grounded style, mood, instrumentation, vocal, release-date, or local-audio
features. The Deej-AI vectors are never compared with text embeddings.

Current checked-catalog coverage (2026-09-06):

| Evidence | Tracks | Coverage |
|---|---:|---:|
| Catalog identity + remote preview URL | 956,917 | 100% |
| Committed semantic sidecar | 0 | 0% |
| Grounded style/mood/instrument/vocal/date facets | 0 | 0% |
| Locally analyzed preview segments | 0 | 0% |

The regression suite uses a three-track synthetic sidecar to exercise the
pipeline, not as evidence about production relevance. Generate and retain a
real pilot report from its reviewed input before evaluating relevance.

`semantic.sqlite` is optional. Schema v2 stores canonical artist/recording IDs,
tags, descriptions, supported facets, distinct original-edition and
release-edition dates, confidence, missingness, provenance, preview segment
coverage, and a precomputed query vocabulary. Missing evidence stays `unknown`.
The app rejects a sidecar whose catalog version, schema, declared row counts,
or query encoder is incompatible. It also verifies every retrieved ID against
the loaded catalog. Schema-v1 sidecars remain readable for grounded feature
eligibility, but semantic retrieval stays disabled until they are regenerated.

## Build a bounded pilot

Prepare UTF-8 JSONL with at most 5,000 reviewed records. Every known value must
be an object containing `value`, `confidence`, and non-empty `provenance`;
`track_id` must exist in `catalog.sqlite`. Do not use artist/title text as a
musical description. Then run:

```sh
python -m pip install sentence-transformers
python python/build_semantic_sidecar.py \
  --catalog build/catalog/catalog.sqlite --input pilot.jsonl \
  --output semantic.sqlite --report semantic-coverage.json \
  --model /models/all-MiniLM-L6-v2 \
  --model-name sentence-transformers/all-MiniLM-L6-v2 \
  --model-revision <git-commit> --feature-version pilot-2026-09 --limit 5000
```

Add `--musicbrainz-cache <data-dir>/musicbrainz-cache.sqlite` to reuse only
already-cached matches at or above score 85. This can supply canonical IDs and
dates but never descriptions; supplied evidence remains necessary. The first
returned release date stays a release-edition date. Only an explicit release-
group first-release date enters the separate original-edition field.

The builder is offline-only and emits accepted/rejected rows, per-facet counts,
query-term count, catalog coverage, and actual index bytes. Python and Sentence
Transformers are data-preparation dependencies only; neither is bundled or
started by the desktop app. Configure only the generated file:

```toml
[semantic]
sidecar_path = "/absolute/path/to/semantic.sqlite"
```

Keep the build model and generated sidecar out of Git. Obsolete runtime keys
from older config files are ignored so existing installations continue to
load.

At 384 float32 dimensions, document vectors cost about 7.3 MiB per 5,000
tracks before query vectors and SQLite/feature JSON overhead; a full
956,917-track dense document matrix alone would be about 1.37 GiB. The coverage
report records the complete index size. Exact scan is intentional for the
pilot.

## Runtime behavior

The offline builder uses compatible Sentence Transformers document and query
encoders. It stores query vectors for normalized full descriptions, adjacent
word pairs, and individual words. At runtime, pure Go Unicode tokenization uses
an exact full-phrase vector when present, otherwise composes the known pair and
word vectors and normalizes the result. An out-of-vocabulary request returns an
explicit unavailable result; it is never assigned invented evidence. No
Python interpreter, model file, or embedding subprocess is needed at runtime.

Cosine similarity supplies separate positive and negative,
non-probabilistic ranking evidence. Seedless requests work when positive
semantic intent produces indexed catalog hits. Seeded requests fall back to
existing audio/co-occurrence retrieval with a `semantic_fallback` notice.
Recognized strict style/vocal constraints are enforced only when the sidecar
declares the facet; unknown evidence is ineligible. Other attributes remain
unsupported.

MusicBrainz enrichment must be cached and performed offline from generation.
Its API requires an identifying User-Agent and at most one request per second;
release dates describe particular editions and must not be treated as verified
original recording dates. See the [MusicBrainz API](https://musicbrainz.org/doc/MusicBrainz_API)
and [release-date guidance](https://musicbrainz.org/doc/Release/Date).

The pilot model is a replaceable baseline: its [model card](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2)
documents 384-dimensional sentence embeddings, while [Sentence Transformers
semantic-search guidance](https://www.sbert.net/examples/sentence_transformer/applications/semantic-search/README.html)
requires compatible query/document encoders. An audio-text pilot is deferred:
[LAION-CLAP](https://github.com/LAION-AI/CLAP) supports aligned audio/text, but
the app has no stable local audio corpus. If one is later licensed, store exact
segment start/end and covered duration for every derived feature.
