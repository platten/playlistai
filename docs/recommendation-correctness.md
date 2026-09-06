# Recommendation Correctness

## Correctness contract

Intent contract v6 separates five concepts that must not be conflated:

- **Essential musical criteria** define what the playlist must musically be. A
  simple category request such as “electronic music” makes `style: electronic`
  essential; descriptive nuance remains soft unless the user makes it strict.
- **Soft preferences** influence ranking but do not establish fulfillment.
- **Hard exclusions** are eligibility rules. Unknown evidence cannot satisfy a
  strict rule.
- **Explicit references** are artists or tracks the user actually named.
- **Inferred anchors** are up to three model-proposed retrieval aids with a
  role, reason, independent catalog-resolution evidence, and musical-suitability
  evidence. They are not rewritten as user instructions or required output.

Generation reports `fulfilled`, `partial`, `unsupported`, or
`needs_clarification`. Requested count alone never establishes fulfillment.

## End-to-end flow

```mermaid
flowchart LR
    P[Prompt] --> I[Validated intent v6]
    I --> R[Resolve explicit references and inferred anchors]
    R --> A[Validate anchor suitability and weight representatives]
    A --> U[Union audio, co-occurrence, taste, semantic, exploration]
    U --> S[Batch-score positive and negative semantics]
    S --> E[Essential criteria, exclusions, identity dedup]
    E --> K[Fixed-scale ranking]
    K --> D[MMR selection and journey-stage reservation]
    D --> Q[Transition sequencing]
    Q --> O[Tracks plus structured outcome and evidence]
```

All channels, including exploration and personalization, pass through the same
eligibility stage. Musical suitability uses supplied facets or compatible
semantic vectors—not artist/title keywords. Ranking uses a request-wide
component denominator: missing positive semantic evidence contributes neutral
zero without deleting that component’s weight.

## “Electronic music” behavior

The rules parser classifies the exact prompt as an essential electronic style,
not an artist named “Electronic.” The LLM schema applies the same semantic
validation and rejects output that drops the defining category or claims an
explicit reference absent from the prompt. “Music by Electronic” remains a
legitimate explicit artist request.

In LLM mode, resolved inferred anchors are usable only when representative
catalog tracks have affirmative evidence for the electronic criterion. The
representatives are then reweighted for this request. Candidate tracks must
also have affirmative electronic evidence before ranking, diversity, or
sequencing. If no compatible evidence source is loaded, the result is
`unsupported` with an action to choose a fitting reference or install a
compatible sidecar; it is never an unrelated full playlist. Catalog-only mode
continues to require a named seed.

## Grounded semantic pilot

[`data/semantic-pilot-reviewed.jsonl`](data/semantic-pilot-reviewed.jsonl)
contains seven real IDs from the 956,917-track catalog: clear electronic and
rock-and-roll examples, electronic subgenres, two hybrids, and one deliberately
low-confidence/incomplete row. The annotations retain MusicBrainz entity
provenance. MusicBrainz describes genres as community tags and therefore
subjective, so the pilot records confidence and completeness rather than
claiming a universal taxonomy ([MusicBrainz genres](https://musicbrainz.org/doc/Genre)).

Executed validation:

```text
catalog tracks: 956917
accepted pilot tracks: 7; rejected: 0
style evidence: 7; complete style facets: 6
other complete facets: 0
status: validation_only; generated index bytes: 0
```

The offline builder uses separate compatible document/query encoders, matching
the Sentence Transformers semantic-search contract
([official documentation](https://www.sbert.net/examples/sentence_transformer/applications/semantic-search/README.html)).
The desktop runtime remains pure Go and uses precomputed vectors. No sidecar or
embedding model is committed, so shipped semantic coverage remains 0%; the
pilot is data and tooling, not a production quality claim.

## Regression and evaluation policy

Deterministic regressions cover category parsing, strict exclusions, hybrid and
journey intent, inferred-anchor rejection, feature-only sidecars, incomplete
facet evidence, missing query vocabulary, exploration scoring, fixed-scale
ranking, conflicting required tracks, strong historical rock taste, maximum
discovery/diversity, and insufficient eligible candidates. LLM tests cover
invalid output, timeout, unavailable runtime, completion termination, and one
bounded truncation retry.

The evaluation harness now records essential-criterion violations and outcome
counts alongside hard violations, coverage, diversity, repetition, transition
quality, and stage latency. Synthetic fixtures prove control flow only. No
held-out human listening judgments or full-catalog semantic annotations are
checked in, so this change makes no measured musical-quality claim.

## Compatibility and limitations

V1/v2 history keeps legacy “seed also required” behavior. V3–v5 references
whose evidence explicitly marked them inferred migrate to `inferredAnchors`;
references without evidence stay explicit to avoid changing old direct
requests. Legacy `complete` history status loads as `fulfilled`. Sliders and
history replay preserve essential criteria and inferred anchors.

Remaining limits are the absence of a distributed full-catalog semantic
sidecar, subjective/incomplete genre labels, no supported acoustic-energy
feature, and no held-out listening judgments. Unknown or low-confidence rows
are ineligible for essential or strict criteria, which can shorten a playlist.
