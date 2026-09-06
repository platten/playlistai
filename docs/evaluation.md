# Recommendation Evaluation

`cmd/recoeval` is the offline, versioned quality harness. It compares the
original `deejai/v4` walk with the multi-channel pipeline without treating
synthetic parity fixtures as evidence of musical quality.

## Run the harness

Use the bundled catalog fixture for a structural smoke run:

```sh
go run ./cmd/recoeval \
  -dataset internal/evaluation/testdata/synthetic.json \
  -catalog internal/catalog/testdata \
  -output /tmp/recoeval.json \
  -markdown /tmp/recoeval.md
```

For a blind listening sheet, add:

```sh
-blind-output /tmp/listening.json -blind-key /tmp/listening-key.json
```

Keep the identity key away from listeners. Record `winner` as `A`, `B`, or
`tie`, confidence from 1–5, and concise notes. Convert completed judgments into
graded relevance labels or versioned interaction records before a later run.
Do not commit prompts, taste histories, or listener identifiers.

## Dataset and split rules

Dataset contract `v1` distinguishes `synthetic`, `unlabeled_workflow`,
`human_judged`, and `observed_interactions` evidence. Recommendation cases need
stable IDs, UTC timestamps, listener pseudonyms, an intent or prompt, optional
0–3 relevance grades, recent exposures, and cohort tags. Use `cold_start`,
`multiple_taste`, `niche`, `non_latin`, and `ambiguous_reference` tags where
applicable.

Observed request outcomes may supply labels when `event.requestId` matches the
case `requestId` (or case ID when omitted). Explicit like/more-like maps to
grade 3, acceptance to 2, and dislike/less-like/removal to 0; the latest track
outcome wins. Exposure is never relevance evidence.

Cases are globally sorted by time into 60% train, 20% development, and 20%
held-out test partitions. Development profiles see train-era events. Test
profiles may see train and development events only; parameters are frozen
before any test run. Duplicate IDs, invalid feedback, catalog version
mismatches, and relevance grades outside 0–3 are rejected; future events are
excluded from profile construction. Supply every
field in each tuning-grid entry; selection first minimizes hard violations,
then maximizes development NDCG@K. Held-out results are never used to tune.

## Metrics and ablations

The report covers labeled intent fields and negation, typed resolver status and
entity accuracy, raw candidate Recall@K, output NDCG@K, runtime hard-constraint
violations, provisional recording duplicates, unique-artist ratio, maximum
artist share, catalog coverage, recent-recording repetition, and mean adjacent
audio/co-occurrence cosine. Parse, exact retrieval, ranking, selection plus
sequencing, and end-to-end build latency are recorded in microseconds. Stage
timings are auxiliary boundary measurements and are not additive to total
latency. Reported 95% intervals are normal approximations over cases and are
unreliable for small samples.

The fixed ablations are audio-only walk, co-occurrence-only walk, existing
blended walk, multi-channel retrieval, personalization, semantic matching when
a compatible sidecar is configured, and the complete diversity/sequencing
pipeline. The semantic row is omitted—not scored as zero—when unavailable.

## Evidence status and defaults

The repository contains no real relevance labels or listening outcomes.
Therefore no musical-quality result or evidence-backed parameter change is
claimed in Milestone 9. The bundled synthetic data checks execution, metric
wiring, cohort coverage, deterministic blinding, and leakage prevention only.
Current `multichannel.DefaultConfig()` values remain the operational defaults
until a sufficiently sized development set can tune them and a once-only
held-out evaluation confirms the choice.

Every successful case records ordered track IDs, normalized-intent fingerprint,
recent-context fingerprint, catalog and algorithm versions, intent version,
profile algorithm/snapshot, and the lossless RNG seed. Reports also record the
semantic feature/model revision when configured. Reproduction requires the
exact dataset and sidecar artifacts matching those identities.

For production-catalog retrieval benchmarks and artifact-aware local parser
evaluation with `cmd/intenteval`, see
[Retrieval and Local Intent Model Evaluation](performance-and-model-evaluation.md).
