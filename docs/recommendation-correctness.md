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
    D --> Q[Joint category, required-order and artist-spacing sequencing]
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

This also applies to “electronic tracks,” “electronic songs,” and “electronic
music like Seed Artist.” Adding a reference does not remove the essential
category. Explicit artist-name spans are excluded from category extraction:
“play Aesop Rock” does not request rock, and “music by Electronic” does not
request electronic music. Narrow exclusions such as “no rock & roll” remain
narrow; they are not expanded into a ban on all rock.

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

Runtime eligibility and evaluation now share `core.CriterionEvidence`,
`core.SemanticConstraintSatisfied`, and `core.JourneySequenceViolations`.
They use the same directional genre hierarchy, confidence threshold (0.6),
provenance requirement, facet completeness, and match/mismatch/unknown states.
An uncertain rock annotation remains unknown even inside a complete facet;
it cannot establish “no rock.” Evaluation judges requested constraints even
when an engine does not claim to enforce them. Journey evaluation checks
ordered stage coverage, not whether every track matches every journey genre.

## Review follow-up: joint journey ordering

`multichannel/v5` removes the post-sequencing category sort and the bypass for
required tracks or explicit waypoints. The sequencer now keeps a deterministic
beam of at most 32 paths per category stage. It uses existing transition scores
while jointly checking category progression, required-track order, recording
deduplication, waypoint order, and hard artist adjacency. Every stage requires
a distinct track; hybrids can occupy any stage they affirmatively match.

The search does not promise a globally optimal ordering. It can return a safe
partial playlist when its selected pool cannot be fully ordered, and reports
`category_journey_exhausted`. Contradictory required order returns
`needs_clarification`. For example, two electronic tracks by artist A followed
by two rock tracks by artist B cannot fulfill four tracks plus hard artist
spacing: the safe result contains one from each stage, not A–A–B–B. A required
rock track can occupy the destination without forcing it before electronic.

The focused suite covers all nine review findings, including parser-to-build
requests, explicit-reference evidence, feature-only exclusions, narrow
negation, required/waypoint category journeys, and runtime/evaluation parity.
The maximum-size synthetic sequencing benchmark is reproducible with:

```sh
go test ./internal/reco/multichannel -run '^$' \
  -bench BenchmarkCategoryJourney100Tracks -benchmem -count=3
```

On Linux/amd64, Intel Core Ultra 9 285H, the 100-track/two-stage fixture measured
50.9–51.3 ms/op and about 35.7 MB allocated per operation (three runs). This
measures sequencing over already-selected synthetic tracks, not parsing,
retrieval, production embedding dimensions, or musical quality.

Validation: `scripts/test.sh` passed shell syntax/lint, generated bindings,
frontend typecheck and production build, `go vet`, pure-Go core compilation,
the full race-enabled Go suite, and `golangci-lint` (zero issues). The initial
sandboxed pnpm cache-access failure was resolved by rerunning the gate with
permission to access its local cache; no dependency or source workaround was
required.

## Compatibility and limitations

V1/v2 history keeps legacy “seed also required” behavior. V3–v5 references
whose evidence explicitly marked them inferred migrate to `inferredAnchors`;
references without evidence stay explicit to avoid changing old direct
requests. Legacy `complete` history status loads as `fulfilled`. Sliders and
history replay preserve essential criteria and inferred anchors.

Parser identities advance to `rules/v5` and `llama/v5`, invalidating cached
interpretations; the ranking/generation identity advances to `multichannel/v5`.
The intent wire shape remains v6 and existing history loaders remain supported.
Legacy seed-only JSON is still accepted by the historical decoder, but is
rejected as a live LLM completion: it cannot bypass current meaning validation.
Generated bindings are regenerated, not hand-edited; no wire-field change is
needed for this follow-up.

Remaining limits are the absence of a distributed full-catalog semantic
sidecar, subjective/incomplete genre labels, no supported acoustic-energy
feature, and no held-out listening judgments. Unknown or low-confidence rows
are ineligible for essential or strict criteria, which can shorten a playlist.
