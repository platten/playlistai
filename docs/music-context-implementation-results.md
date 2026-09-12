# Native music context and compositional intent extraction

This extends the consolidated Enhanced Hybrid work on
`codex/enhanced-audio-remaining`. It adds native Go source context and strengthens
the pre-parser. Python is not a desktop prerequisite and no new inference model
is required. The [design and model evaluation guide](music-context-and-preparser-plan.md)
describes the source contracts, larger-model options and reproducible commands.

## Behavior

The parser preserves compound durations, indirect negation, `neither/nor`, soft
instrumental preferences, explicit year ranges and names containing conjunctions.
An uncertain whole artist mention remains a candidate rather than a protected
classification. Its source role still determines an exclusion or requested
journey endpoint. A list of possible years cannot become a continuous interval
solely because an LLM emitted matching bounds.

Enhanced Hybrid can resolve public artist and album context through MusicBrainz,
linked Wikidata and a verified English Wikipedia article. The lookup checks
identity before accepting context, records source versions and licenses, and
caches public responses. Genre profiles currently use MusicBrainz identity.
Short musical descriptors pass through the existing reviewed dictionary.

Context can select scoped catalog seeds and broaden candidate retrieval. Artist
biographies never become recording genre tags, essential criteria, hard user
constraints or CLAP verification clauses. Actual required start/end recordings
retain their identity. Explicit genres remain primary for metadata discovery;
context can supply discovery hints when no genre was requested. Other
recommendation modes keep their separate policies.

The context step has an eight-second child deadline and at most eight HTTP
attempts within the existing shared knowledge budget. It proposes at most three
reference profiles and four seeds per profile. An early-artist request proposes
the first two dated studio albums when the returned discography establishes
them; this does not restrict every output recording to that era. Compatible
catalog audio vectors diversify seeds inside the scoped pool. Missing context
leaves the original reference available.

Contracts: `music-context/v1`, `source-atoms/v2`, `rules/v13`, `llama/v15` and
`multichannel/v25`. The intent layout remains version 9; context is an optional
knowledge snapshot field. Source revisions, selected seeds and context policy
participate in saved discovery identity. Old histories do not fetch new context
merely because they are replayed.

## Verification scope

The focused deterministic tests exercise both rules and schema reconciliation,
scoped seed retrieval, context versus recording evidence, exact source identity,
ambiguous identities, source-cache validation, request limits, cancellation,
history, exclusions and endpoint roles. These are correctness tests; synthetic
vectors do not measure musical preference.

Native parser checks use the already installed Qwen2.5-3B-Instruct Q4_K_M and
native runtime with a 4096-token context and temperature zero. The original
twelve prompts, ten Enhanced Hybrid prompts and new composition fixture are
development regressions. They are not an independent held-out set or listening
evaluation. Reports retain the actual parsed intent and consumer-field checks.

The Windows contributor gate includes race-enabled Go tests with atomic
coverage, vet, lint, pure-Go compilation, generated Wails bindings, frontend
typechecking, 158 frontend tests and the production frontend build. Native
execution on every target OS is not established by a local Windows pass.

### Executed checks (2026-09-12)

| Check | Observed result |
| --- | --- |
| Final Windows `scripts/test.ps1 -TestReportDirectory bin/music-context-gate-verified -HostCoverage -PackageParallelism 4` | Passed all checks above; zero lint issues, 158/158 frontend tests. Race/coverage Go invocation completed in 85.46 seconds. |
| Native parser, original 12 + Enhanced Hybrid 10 + composition 13 cases | 35/35 consumer-field checks passed in each of two runs; every result reported `llama/v15`, backend `llama`, and no errors. Summed case durations: 110.293 / 110.036 seconds. |
| Focused provider and retrieval checks | Context identity, cache, seed, scope, exclusion, cancellation and evidence-boundary regressions passed. |
| Independent implementation review | Confirmed parser, context-identity and seed-exclusion findings corrected; no actionable production findings remain. |

The native run used the installed Qwen2.5-3B-Instruct Q4_K_M and catalog
`1:956917:1788613313` (956,917 tracks). These recorded durations are diagnostics,
not a controlled performance comparison. Local artifacts are under
`bin/music-context-gate-verified` and
`bin/enhanced-translation-implementation/native-context-final-all`.
SHA-256 of the two native JSON reports:

```text
ai-parser-1.json  0d0b91be016e5f9beb4d66a091f22010442739a05f0a817c7e9f21227f4699fb
ai-parser-2.json  190175a94d6884b2fc02f763f74356aacbe8fb1d23197f30d44679760dc5016d
```

Development failures were fixed before the final run: the restricted sandbox
prevented a Windows temporary-file rename, and two ignored standalone probe
programs initially lacked Go build-ignore tags. The persistence regression
passed outside that sandbox and the final full gate passed with the probes
excluded from package discovery. No application assertion was relaxed.

## Public source archive and observed integration gaps

Public metadata downloaded for the live context probe is retained at
`C:\Users\pawel\Downloads\playlistai-music-context-v1`. The source cache and
profile JSON contain public API results, source revisions and licenses. They
contain no preview audio, PCM, model weights or private listening history.
This is a small validation archive, not a comprehensive artist database or a
new bundled runtime dependency. Wikipedia text and supplementary MusicBrainz
data retain their distinct reuse terms.

The first live probe found an Aerosmith identity and its linked Wikidata and
Wikipedia descriptions, but its unfiltered 166-album search was truncated.
It also correctly abstained for Christian Löffler and Nine Inch Nails because
MusicBrainz returned multiple exact-name identities. Those observations drove
studio-album filtering and bounded corroboration against known catalog
recordings. Search rank alone is insufficient to resolve a same-name artist.
The initial report is retained separately from subsequent runs.

The final `context-probe-verified-v1.json` records:

| Public reference | Profile and catalog seeds | Observed source coverage |
| --- | --- | --- |
| Early Aerosmith | One profile; one scoped seed from the proposed 1973–1974 albums | MusicBrainz; the bounded request window did not complete both album recording searches or linked prose. |
| Christian Löffler | One profile; four existing catalog representatives | Correct same-name artist corroborated by recording credits; MusicBrainz genres and identity-checked Wikidata/Wikipedia. |
| Nine Inch Nails | One profile; four existing catalog representatives | Band distinguished from a same-name mashup artist; MusicBrainz genres and identity-checked Wikidata/Wikipedia. |

All three final source probes completed without reported errors. Whole
`PrepareMusic` times were 8.599, 5.187 and 4.699 seconds respectively; the
eight-second context child runs inside this wider operation. The three returned
profiles contained genre hints but no additional dictionary-approved
mood/texture/instrumentation characteristics. Löffler/NIN retained their existing
representatives; this is verified context acquisition, not measured improvement
in recommendation counts or listener preference. Early Aerosmith's single seed
is an explicit coverage limitation.

The archive includes the public response cache, all three probe reports, a
SHA-256 manifest, source/license notes and a compressed ZIP. No Python preparation
is necessary for this native API cache. It is retained for inspection and future
data preparation, not installed automatically or treated as an exhaustive
redistributable dataset. The existing dictionary preparation instructions remain
the optional Python workflow for the bundled concept registry.

The compressed source archive is 209,782 bytes with SHA-256
`506e4602e4c93f554b820887af2b17d74d7babd4745a8c0acabfd7f7000ba10c`.

## Remaining limits

A larger model can be useful for compositional interpretation and independent
test-case proposals. No larger-model comparison was run for this change. The
documented next candidates are Qwen3.5-9B and Gemma 3 12B; the optional 35B-A3B
model still requires its full approximately 22 GB weight artifact despite
activating fewer parameters per token. Disk size is not peak runtime memory.

No parser can guarantee zero mistakes. General multilingual grammar, arbitrary
relative comparisons, stage quantity allocation and exact duration ranges
remain incomplete. Wikipedia lead text can omit relevant musical details.
Missing or ambiguous links can leave context unavailable. Recording-specific
evidence is still required for strict exclusions such as screaming or film
scores; descriptions and a larger LLM cannot supply missing audio evidence.
Held-out listening comparisons, per-facet calibration and fixed-evidence
recommendation ablations remain outstanding.
