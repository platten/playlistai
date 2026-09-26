# Local parsing context

The enriched parsing context is implemented behind `IntentInput.EnrichParsingContext`.
The desktop default remains the baseline because the initial paired model run
did not pass the acceptance gate.
This flag changes parsing only; it is not a source setting or a recommendation mode.
Starting-track proposals, ranking, and generation time limits are unchanged.

## Prepared evidence

`Container.PrepareIntentInput` runs the existing exact-name and reviewed-alias
recognizer, then prepares context before the parse-cache lookup. The standalone
llama client also prepares context when supplied only text facts. The prepared
snapshot is reused across validation retries and managed-server restarts.
No additional model, embedding index, fuzzy matcher, or network lookup is used.

The treatment facts formatter retains every protected occurrence and its kind,
canonical name when available, match type, polarity, scope, strength, degree,
and alternative group. It distinguishes one candidate, multiple candidates,
truncated lookup results, and user-confirmed identities. Provider IDs and snapshot
hashes stay in saved grounding evidence rather than model prose. Candidate names
and disambiguation descriptions are quoted reference data, not permission to
resolve an ambiguous identity. Source-span validation and reconciliation retain
precedence over model interpretation.

Registry `music-concepts/v6` adds optional reviewed explanations for dark mood,
dark/bright/warm timbre, romantic mood, Romantic classical music, instrumental
music, dynamic range, and compressed dynamics. Aliases and provider mappings are
unchanged. Hints include the matching spelling, canonical value, and facet.
Parent and related links are explicitly labeled as relationships, never synonyms
or additional requirements. CLAP query captions are not definitions; unknown
wording remains in the original request. See the generated
[thesaurus reference](prompt-thesaurus.md).

## Budget and compatibility

Policy `parsing-context/v1` prepares at most eight whole records and 1,024 UTF-8
bytes, including the reference-data header and newlines. Ambiguous identities
come first, concept explanations next, then facet/relationship hints; unambiguous
identity descriptions use any remaining room. Source order breaks ties. One
identity record contains at most three candidate names and descriptions, with an
omission notice for additional or truncated results. A record that does not fit
is omitted whole, preserving names and UTF-8 text.

Each attempt uses the existing template/tokenizer measurement of mandatory
messages, including validation feedback. Its output allowance is computed before
optional hints, using the existing 128-token reserve. Hints use only remaining
space, with one token reserved per UTF-8 byte plus a 32-token boundary allowance.
No extra tokenizer calls or context-window increase are made. Missing or failed
measurement omits optional hints. Protected facts are never dropped to fit hints.
At 4,096 tokens, omission is expected and the readable facts supply the dependable
change. The output allowance can still differ between baseline and treatment
because their mandatory facts differ; optional hints never reduce it.

Saved translations may contain `parsingContext`, with policy version, ordered
records linked to source atoms, a SHA-256 content fingerprint, preparation
omissions, and the record indexes used by the successful attempt. Cloning copies
all nested evidence. Legacy history loads without synthesizing this field.
The policy and fingerprint participate in recognition/cache identity, and the
llama parser version is `llama/v18`. Incomplete recognition remains uncached.
Anchor-proposal serialization explicitly removes parsing-context evidence.
The model output schema and reference resolver interfaces are unchanged.
Wails bindings are generated from the additive saved-evidence types.

Evaluation observes each attempt's measured mandatory token count (absent when
unknown), conservative mandatory/optional byte bounds, output allowance, used and
omitted hints, error, and truncation. Public synthetic test inputs can be included
in explicit evaluation reports; normal operation does not log private prompt or
hint text beyond the existing opt-in diagnostic path.

## Validation and rollout

Offline regressions cover budgets, whole-record selection, cache separation,
immutable retries, successful-attempt evidence, history, and anchor payloads.
The independent parser review found no actionable defects.
The [evaluation protocol](parsing-context-evaluation.md) describes the frozen
32-case development/held-out split and isolated application-data preparation.
Model acceptance requires better targeted held-out parsing with no reduction in
overall labeled-field accuracy or regression in exclusions, required roles,
journey scope, source spans, ambiguity, overflow, truncation, or fallback failures.
Parsing accuracy does not establish musical recommendation quality.


## Recorded comparison: 2026-09-25

The [machine-readable result summary](data/parsing-context-evaluation-v1.json)
records model, runtime, source, dataset, scorer, and baseline worktree identities.
The baseline preserves the starting worktree, including its uncommitted changes;
only parsing context policy differed between inference runs. Prepared source facts
were verified identical after removing the treatment's additive context evidence.

Both variants used Qwen2.5-3B-Instruct Q4_K_M (SHA-256
`9c9f56a391a3abbd5b89d0245bf6106081bcc3173119d4229235dd9d23253f94`),
llama 0.4.0-dev build 10826 / `73a43d1f6`, context 4,096, eight CPU threads,
99 requested GPU layers, and CUDA0 on an NVIDIA RTX 5060 Laptop GPU (8 GiB),
with an Intel Core Ultra 9 285H under WSL2. Each run reused one model process
for all 32 cases and two passes; the variants ran sequentially. No playlists or
listening tests ran. Both saved outputs were rescored with
`context-operational/v1` after mutation tests exposed evidence-only scoring gaps;
no prompts, labels, or model outputs changed during rescoring.

| Measurement | Baseline | Enriched |
| --- | ---: | ---: |
| Overall labeled fields | 210/250 (84.0%) | 204/250 (81.6%) |
| Held-out labeled fields | 110/130 (84.6%) | 110/130 (84.6%) |
| Exact runs | 40/64 | 40/64 |
| Schema-validation failures | 4/64 | 6/64 |
| Retries | 4 | 12 |
| Context-overflow errors / truncated attempts | 0 / 0 | 0 / 0 |
| Attempts using optional hints | 0/68 | 0/76 |
| Parser startup | 1.503 s | 1.004 s |
| First-pass median / P95 parse time | 3.495 / 5.473 s | 4.166 / 8.811 s |
| Warm-repeat median / P95 parse time | 3.504 / 5.511 s | 4.074 / 8.679 s |
| Median recognition/context preparation | 4.877 ms | 5.048 ms |

The `reference-track` development case introduced an ungrounded `10 tracks`
constraint in both treatment passes. The baseline scored three of its four
labels; the treatment failed schema validation and scored zero. Existing alias,
OR-group, ambiguity, and boundary failures remain visible in the final scores.
The two unavailable-source cases also ran against an empty isolated data
directory: each variant completed four parses without errors and scored 8/16
labels. These are observed timings from one host/run order, not a performance
improvement claim or cold filesystem-cache benchmark.

Every treatment attempt measured its mandatory messages, but none had spare
space after reserving the unchanged mandatory-only output allowance. All 94
optional record opportunities across attempts were omitted. This live comparison
therefore evaluates the readable facts; optional definitions/descriptions have
bounded offline coverage but no live exposure in this run.

The result fails both the required held-out improvement and the overall
non-regression conditions, so enrichment remains opt-in. Desktop fallback was
not measured, and no musical-quality improvement is claimed. Future rollout
requires revised development work and fresh acceptance evidence rather than
using this held-out result as a tuning target.

Final local validation passed on Linux/WSL2: affected Go tests; the full
`PATH="/home/paul/go/bin:$PATH" ./scripts/test.sh` gate (binding generation,
frontend typecheck/tests/build, vet, pure-Go compilation, race tests, and lint);
`python3 -m unittest discover -s python -p test_prepare_intent_dictionary.py`;
`python3 python/prepare_intent_dictionary.py --check`;
`node scripts/render-music-thesaurus.mjs --check`; and `git diff --check`.
The final independent code and artifact reviews found no actionable issues.
Native Windows/macOS execution and desktop rules-fallback frequency were not
measured. No existing listening holdout or unrelated working-tree changes were
modified.
