# Paipack starting-point and recommendation evaluation

The four priority prompts have an offline workflow fixture at
`internal/evaluation/testdata/paipack-priority-synthetic-v1.json`. It is explicitly
synthetic: placeholder seed judgments refer only to invented `synthetic:` IDs,
not Radiohead, Marilyn Manson, or any recording in the user's pack. Real outputs
therefore remain unjudged. Repetitions populate the existing chronological split
and exercise every prompt in development and test; they are not independent
held-out musical-quality evidence.

## Run the production overlay without external providers

Use an installed Deej catalog for the base source. The supplied paipack is opened
read-only, then imported into an isolated temporary evaluation directory. The
command does not touch the active application library or listening history.

```sh
go run ./cmd/recoeval \
  -dataset internal/evaluation/testdata/paipack-priority-synthetic-v1.json \
  -catalog /absolute/path/to/installed/catalog \
  -paipack "$HOME/datasets/testlibrary.paipack" \
  -library-mode combined \
  -output /tmp/paipack-evaluation.json \
  -markdown /tmp/paipack-evaluation.md \
  -blind-output /tmp/paipack-blind.json \
  -blind-key /tmp/paipack-blind-key.json
```

`combined` gives both sources access to production retrieval; `library_only`
restricts the comparison explicitly. Neither setting imposes a source quota.
This runner installs no live providers, preview downloads, or new model inference.
It cannot evaluate fresh prompt-to-CLAP vectors without an independently supplied
compatible text encoder. The rules parser is a workflow control, not a substitute
for the desktop model parser. Use frozen versioned intents in a private dataset
to isolate recommendation quality from parsing; evaluate raw prompts separately.

`library_evidence_off` and `library_evidence_on` retain their existing names.
They compare the optional library evidence configuration within today's code,
not the historical application and not a complete metadata-versus-CLAP ablation.
Both retain recording identities, typed metadata, and production eligibility.
Shared packs additionally support the existing `-discovery-state` baseline,
metadata, MERT, and combined comparisons. These channel filters run after
retrieval, so their latencies do not measure avoided retrieval computation.

## Label starting points separately from output tracks

Each recommendation case accepts optional `startingPointJudgments`:

```json
{
  "seedRelevance": {"recording-id": 3},
  "openingRelevance": {"different-opening-id": 2},
  "eligibleNeighbors": {"recording-id": 0}
}
```

Relevance grades run from 0 (unsuitable) to 3 (excellent). Missing IDs remain
unknown. Eligible-neighbor counts must come from an independently assessed,
bounded pool under the same request and exclusions; an explicit zero identifies
a dead end, while an absent count says nothing about neighborhood quality.
These counts are supplied judgments, not a claim that the runner exhaustively
searched or listened to a neighborhood.

Case JSON records resolved positive seed IDs from the returned intent and the
actual opening ID separately. Seed order follows the intent's retrieval-reference
enumeration and representative order; it is not a global cross-stage ranking.
Top-1 and top-3 are normalized relevance (grade divided by 3); top-3 averages up
to the first three distinct returned IDs and requires all of them to be judged.
`seedJudgedAt3` and `seedReturnedAt3` expose coverage. Dead-end rate uses only
seeds with neighborhood judgments. No returned anchors means unavailable seed
metrics, even if the engine can still construct a playlist.

The Markdown report shows uncertainty for seed and opening metrics; JSON retains
per-case coverage. Repeated workflow cases must not be used to interpret these
intervals as independent musical-quality evidence.

## Check source neutrality and collect held-out evidence

`sourceQuality` groups returned display IDs into personal pack, shared pack, and
outside sources and reports returned count, judged count, and mean judged grade.
Unjudged tracks never receive zero grades. A recording displayed under an outside
ID may have gained local evidence through a recording join; source groups report
display provenance, not exclusive evidence ownership. Differences in source share
or mean grade alone cannot establish bias or fairness.

For controlled neutrality tests, judge equivalent-fit local/outside candidates,
join authoritative duplicate recording identities, and rerun with an additional
pack alias. Compare the same recording's rank and eligibility, not its namespace.
Do not force a 50/50 output. Labels in private datasets must cover emitted IDs or
be deliberately joined by verified recording identity before evaluation; do not
copy grades across fuzzy title matches.

Before any quality or retuning claim, collect blinded, independent listening
judgments for opening fit, seed relevance, prompt facets, and transitions. Hold
out artists/albums and paraphrases. Include prominent versus occasional piano,
cello versus violin, reversed journey direction, and original 1990s recordings
versus modern stylistic imitations. Preserve unknowns, report coverage, and keep
tag consistency checks separate from human musical judgments. These tools and
synthetic regressions do not establish improved musical quality.
