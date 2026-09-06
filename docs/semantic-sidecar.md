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

`semantic.sqlite` is optional. Schema v1 stores canonical artist/recording IDs,
tags, descriptions, supported facets, distinct original-edition and
release-edition dates, confidence, missingness, provenance, and preview segment
coverage. Missing evidence stays `unknown`. The app rejects a sidecar whose
catalog version, schema, model name, revision, or vector dimension does not
match. It also verifies every retrieved ID against the loaded catalog.

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
catalog coverage, and actual index bytes. Configure `[semantic]` with
`sidecar_path`, `python`, `query_script`, `model_path`, `model_name`,
`model_revision`, and `embedding_dim = 384`. Keep the model and generated
sidecar out of Git.

At 384 float32 dimensions, vectors cost about 7.3 MiB per 5,000 tracks before
SQLite/feature JSON overhead; a full 956,917-track dense matrix alone would be
about 1.37 GiB. Exact scan is intentional for the pilot.

## Runtime behavior

Grounded descriptions use Sentence Transformers document embeddings; prompts
use the same revision's query embeddings and cosine similarity. Positive and
negative semantic matches are separate, non-probabilistic ranking evidence.
Seedless requests work when positive semantic intent produces indexed catalog
hits. Seeded requests fall back to existing audio/co-occurrence retrieval with
a `semantic_fallback` notice. Recognized strict style/vocal constraints are
enforced only when the sidecar declares the facet; unknown evidence is
ineligible. Other attributes remain unsupported.

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
