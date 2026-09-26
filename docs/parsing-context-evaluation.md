# Parsing-context evaluation protocol

`cmd/intenteval` evaluates parsing without generating playlists, resolving
starting-track proposals, or invoking a second LLM. A run constructs one parser
and reuses it across all selected cases and repeats. `-context-policy baseline`
is the default; `enriched` enables readable protected facts and bounded context.
The default application behavior remains baseline until the acceptance evidence
described in [parsing-context.md](parsing-context.md) is sufficient.

## Frozen cases and evidence

`internal/evaluation/testdata/parsing-context-v1.json` contains 32 synthetic,
explicitly labeled requests: 16 development and 16 held-out. Sixteen families
keep related names and paraphrases in the same split. The `family:` and `split:`
tags were assigned before model evaluation. The dataset SHA-256 is
`f1ac5d9c3460af53588d91804d41684449a69125ded4c3ea51160eb592bfe5f4`.
Tests pin this fingerprint; future label changes require a new dataset version.
This dataset does not replace or modify the musical listening holdout and does
not establish musical quality or a human listening judgment.

The cases cover artist/common-word collisions, reviewed aliases, homonyms,
compound and non-Latin names, exclusions, required tracks, OR alternatives,
journey scopes, mood/timbre distinctions, romantic mood/historical period,
vocal strength, unknown wording, crowded matches, and long requests. Cases
tagged `unavailable_sources` must additionally run with an empty isolated data
directory: unknown names in an installed snapshot alone do not test unavailable
sources. Do not use held-out outcomes to tune either prompts or labels.

Labels score both the retained source interpretation and the operational intent.
Musical atoms must have a corresponding preference with the correct facet,
concept, scope, polarity, strength, and degree; essential/required positive
musical atoms must also have an essential criterion. Source-occurrence labels
allow surrounding modifiers in evidence, such as `mostly instrumental` around
`instrumental`. Journey labels check the actual start/destination references.
Source-span checks include explicit operational references, preferences,
criteria, constraints, and other source evidence. Ambiguity checks require
multiple or truncated candidates in the source, intact operational grounding,
and no selected identity. Artist OR groups must retain their shared source group
and operational references; the reference contract has no separate OR field.

The scorer is `context-operational/v1`. It was strengthened using deterministic
mutation regressions before interpreting paired model results. Saved reports
can be rescored without rerunning inference; this changes scores only, preserving
the outputs, source snapshots, model identity, observations, and timing.

## Isolated local recognition

`-app-data` invokes `Container.PrepareIntentInput`, the same offline recognition
path used by the desktop, before the selected parser. New empty directories get
an `.intenteval-isolated` marker. Existing directories must already have that
marker; the normal application directory and directories containing symlinks
are rejected. Never mark live user data as evaluation data. Use a dedicated
directory and stage any real source snapshots there through the existing setup
paths before benchmarking.

The preparation container uses the rules backend and refuses model-enabled
preferences or installed audio workers. It creates isolated stores but does not
download anything, generate playlists, or launch a second inference runtime.
Without `-app-data`, evaluation prepares the same text-only extraction snapshot
as the direct llama client and makes no claim about installed source availability.
Prepared facts are retained even when parsing fails.

`-recognition-fixture` builds a small local MusicBrainz-format index using the
repository's existing builder. It uses explicitly synthetic IDs and descriptions
from `cmd/intenteval/testdata/context-recognition-v1/`. Two Phoenix identities
exercise ambiguity; twelve John Williams identities exercise truncation to eight
grounded candidates; reviewed alias examples and non-Latin names exercise exact
recognition. These are test records, not artist biographical claims. No external
data or model is fetched. The fixture must never replace a different snapshot.

Current recognition can include a final period in an artist lookup token. The
fixture's short exact-reference cases omit terminal periods to exercise grounded
identity behavior. That pre-existing punctuation limitation is outside this
change. Existing failures, including artist OR grouping and long required-track
boundaries, remain scored failures rather than changing labels to match output.

## Reproducible runs

Create a fresh isolated directory for each source condition. Substitute existing
local model/runtime paths and use the same flags, binary, model, resources, and
frozen dataset for both variants. Model hashing and device probes are outside
the separately measured parser startup interval.

```sh
go run ./cmd/intenteval \
  -dataset internal/evaluation/testdata/parsing-context-v1.json \
  -backend rules -app-data /tmp/intent-context-fixture -recognition-fixture \
  -output /tmp/intent-context-smoke.json -markdown /tmp/intent-context-smoke.md

go run ./cmd/intenteval \
  -dataset internal/evaluation/testdata/parsing-context-v1.json \
  -model /path/to/model.gguf -runtime /path/to/llama \
  -app-data /tmp/intent-context-fixture -context-policy baseline \
  -n-ctx 4096 -gpu-layers -1 -repeat 2 \
  -output /tmp/intent-context-baseline.json -markdown /tmp/intent-context-baseline.md

go run ./cmd/intenteval \
  -dataset internal/evaluation/testdata/parsing-context-v1.json \
  -model /path/to/model.gguf -runtime /path/to/llama \
  -app-data /tmp/intent-context-fixture -context-policy enriched \
  -n-ctx 4096 -gpu-layers -1 -repeat 2 \
  -output /tmp/intent-context-enriched.json -markdown /tmp/intent-context-enriched.md
```

For GPU runs, use the same explicit `-device` and nonnegative `-gpu-layers` for
both variants. `-split development` and `-split heldout` select the frozen split;
`-case` selects a single case. Repeats share the parser process but each request
prepares its own snapshot and bypasses the desktop parse cache. Reports separate
first-pass and warm-repeat parsing latency, preparation latency, parser startup,
and recognition setup. Startup includes loading and health checks; it is not a
token-generation timing. Latencies do not establish cold OS filesystem caches.

Run the two unavailable-source cases with a different fresh `-app-data` directory
and omit `-recognition-fixture`. Verify that the saved recognition status is
`unavailable`. Do not delete or reset normal application data to prepare a run.

```sh
go run ./cmd/intenteval \
  -dataset internal/evaluation/testdata/parsing-context-v1.json \
  -rescore /tmp/intent-context-baseline.json \
  -output /tmp/intent-context-baseline-rescored.json \
  -markdown /tmp/intent-context-baseline-rescored.md
```

Version 3 reports add prepared source facts, resource/recognition identity,
context policy and ordered hint text/fingerprint, successful used-hint subsets,
preparation and parsing timing, phase aggregates, retry counts, and per-attempt
budget observations. Mandatory token counts are measured when the runtime
provides reliable tokenization; optional bounds are conservative UTF-8 byte
estimates. Missing measurements stay absent, including all LLM attempt
measurements for the rules backend. Old reports remain readable without
synthesizing new evidence.

This harness calls the configured parser directly. A parser error is recorded
and all labels count as incorrect; application rules fallback is not invoked or
measured. Therefore direct-parser results cannot establish that desktop fallback
frequency is unchanged. Compare schema errors, truncation, retries, overall
labeled-field accuracy, held-out accuracy, and protected-field failures, and
retain baseline defaults whenever model results are unavailable, inconclusive,
or fail an acceptance condition. Keep detailed reports local: they contain the
evaluated prompts and any configured source descriptions.

## Offline checks

```sh
go test ./cmd/intenteval ./internal/evaluation
./scripts/test.sh
git diff --check
```

Regression tests cover isolated/unavailable sources, canonical/alias/homonym
fixture recognition, frozen family splits, operational scoring mutations,
immutable saved observations, unknown token measurements, legacy reports,
first-pass/warm-repeat separation, and no-model rescoring.
