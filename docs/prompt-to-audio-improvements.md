# Prompt-to-audio improvements and retest

Implemented on `codex/enhanced-audio-remaining` for the existing PR #24, following
[prompt-to-audio-investigation.md](prompt-to-audio-investigation.md). The original
investigation is a historical baseline; the changes and validation below are a
separate implementation follow-up dated 2026-09-12.

## Result and boundaries

Sound/mood requests now compare a bounded pool of eligible alternatives before
selecting the requested N tracks. Previously the first N eligible tracks could
end discovery before stronger sound matches were considered. The comparison
pool targets 2N minus required tracks, capped by candidate limits; saved output
counts, required recordings, stop/cancellation and analysis budgets retain their
existing meaning. Genre-only and pure instrumental checks retain their prior
completion behavior.

Compatible cached CLAP analyses can now retrieve recordings outside catalog
seed-neighbor results, including for reference-free descriptions. Retrieval
scans at most 20,000 stored rows and keeps at most 512 candidates, with stable
ordering, catalog/model/hash validation and current recording-identity checks.
It does not fetch previews or write assessments. Every returned candidate still
passes the normal hard exclusions, genre evidence and audio checks. The shortlist
is frozen per request and its fingerprint contributes to evidence identity.
This is a bounded cache search, not exhaustive search of the full catalog;
shortlisting before hard filtering can reduce recall.

Uncalibrated Best Available CLAP comparisons retain the literal phrase and add
at most one short context caption identifying its kind (mood, genre, texture,
instrumentation or vocals). They average the variant similarities without
turning cosine into confidence. Positive genre/style aliases share a facet so
multiple aliases cannot outweigh one mood merely by repetition. Archived-audio
suppression preserves these facet weights. Calibrated policies and strict
checks keep their original query behavior and thresholds. Caption ensembles
remain a ranking hypothesis; this run does not establish listening superiority.

Interpretation now guards invented/conflicting entity exclusions, discarded
positive clauses before contrasting exclusions, emotional words classified as
genres, and explicit vocal descriptions classified as genres. Period repair
handles decades and preserves composition versus release meaning. Requested
quality clauses survive as texture preferences without removing an essential
role. Repeated emotional words in different stages are left to the interpreter;
a lexical repair must not merge their scopes. Negative preferences stay negative.
The DSP phrase parser preserves additive wording such as “not only deep bass.”
The local-model parser uses greedy decoding and its existing bounded corrective
retry, with validation feedback next to the request as well as in system guidance.

## Compatibility and distribution

- Recommendation algorithm: `multichannel/v23`; parser: `llama/v13`.
- New query/aggregation identity: `typed-clause-ensemble/v1`. Assessment cache
  keys include the normalized clauses and policy. Existing compatible audio
  embeddings remain reusable; incompatible model spaces remain separate.
- No new model, database migration, frontend dependency or desktop Python
  requirement. Changes run in compiled Go with the existing packaged native
  workers. Explicit paths to staged `llama-primary`/`llama-cpu` executables now
  retain their unified-runtime `serve` subcommand.
- Existing optional MERT packaging and offline Python preparation remain as
  documented in [mert-model-preparation.md](mert-model-preparation.md). No new
  datasets or previews were needed or downloaded for these changes. Original
  sources, packs and derived evidence remain in the user's Downloads directory.

## Executed checks

The final Windows amd64 `scripts/test.ps1` gate passed: installer/script checks,
Wails binding generation, frontend typecheck, all 154 frontend tests, production
build, Go vet, pure-Go core compile, full race-enabled Go suite, and lint with
zero issues. Targeted audio/schema/parser/recommendation and musiccheck tests
also passed. No golden fixture was weakened. The musiccheck vocal assertion is
stronger: a vocal preference copied into a genre gate now fails the contract.

Focused coverage includes six N=1/N=2 cases across all three analysis policies,
stop preserving checked tracks, stable replay/counts, cache-only expanded recall
with a hard artist exclusion, corrupt/incompatible cache rows, scan limits and
cancellation, typed query identities, facet weights with archived evidence,
period basis, negative/repeated moods and affirmative versus negative clauses.
Separate read-only review found and verified fixes for repeated mood scope,
negative reductions, temporal basis, mixed-evidence facet weights and
negative-only contrast requests and punctuation inside exclusion lists.

One intermediate complete gate failed because the new omission guard did not
accept a correctly grounded genre whose evidence span was the full request.
The guard now accepts the literal positive value within that broader source
span; the existing synthetic contract fixture and assertions were preserved.
The subsequent complete gate passed. Local native execution here was Windows
amd64; the new commit's hosted cross-platform checks are reported in PR #24.

## Local-model and real-cache regression results

The installed Qwen2.5-3B-Instruct Q4_K_M GGUF was tested against the unchanged
12-prompt v8 fixture. Baseline investigation: 8/12 passing interpretation checks.
The final two runs each passed 11/12, with the strengthened vocal assertion.
These are development contract checks, not held-out musical-quality scores.

The remaining prompt is “Dubstep but with no skrillex or bassnectar.” The model
first omits the affirmative genre; correction sometimes misspells the explicit
artist as `sksrillex`, which grounding rejects. The final first run failed on
that invalid reference; the second failed on a local llama HTTP connection
closing during correction. In these CLI runs neither failure is counted as a pass or silently
replaced with a broader request. The desktop retains its existing reported rules fallback, whose limited interpretation remains a risk. Earlier intermediate reports (including a
10/12 greedy-decoding run and transport errors) are retained rather than
selecting only favorable runs. Larger-model/copy-fidelity evaluation and the
local-runtime connection failure remain unresolved.

A complete replay cannot accept a report with a failed raw interpretation. That
initial attempt failed explicitly. The eleven successfully interpreted prompts
were then replayed unchanged in each mode with requested count 5 and the
existing minimum-one-track CLI assertion. The missing twelfth interpretation
remains a separate failure; the original fixture was not edited.

| Mode | Replay contract/count passes | Failures | Interpretation failures excluded from replay |
| --- | ---: | ---: | ---: |
| Deej-AI-only | 2/11 | 9 | 1 |
| AcousticBrainz-first | 7/11 | 4 | 1 |
| CLAP-first | 7/11 | 4 | 1 |
| Enhanced hybrid | 7/11 | 4 | 1 |

Each analysis mode made 58 cache hits across the eleven requests and fetched
zero bytes. There were only eight unique compatible preview recordings. Across
five requests, 21 selected slots had cached CLAP evidence; the other successful
requests were two named-artist baselines. Instrumental output had only one of
five requested tracks and remained partial despite passing the CLI minimum.
Unverified mood/era suggestions also remained partial. Four requests had no
output: three lacked affirmative genre evidence and the classical journey's
required destination lacked compatible preview evidence. These results are
**not a passing end-to-end quality regression**. No blind listening or held-out
quality improvement was established, and the earlier rules-only replay is not
an apples-to-apples comparison with this LLM-plus-cache configuration.

The cache uses `laion/larger_clap_music_and_speech` revision
`195c3a3e68faebb3e2088b9a79e79b43ddbda76b`, weights fingerprint
`433ed0b2f651ca8869ed41276bbb929b406900ebac239569748923371a2b23a3`, ONNX Runtime
1.26.0 CPU, 512 dimensions. Catalog: `1:956917:1788613313`, 956,917 tracks.
The frozen Enhanced snapshot fingerprint is
`099d4e76e66cf86948c3d41350517ea17b3afb39b57178fbc72bc793bbd7ab75`.
This does not validate the separately recommended music-only CLAP bundle or
resolve its previously documented query-discrimination concern.

## Reproduction and retained artifacts

After the documented platform setup, run:

```powershell
.\scripts\test.ps1
go test ./cmd/musiccheck ./internal/audio ./internal/intent/schema ./internal/intent/llama ./internal/reco/multichannel -count=1
go build -o bin/musiccheck.exe ./cmd/musiccheck
```

For a native parser check, supply an installed GGUF, runtime and catalog. Repeat
the command with a distinct output path to expose model/runtime variability:

```powershell
.\bin\musiccheck.exe -parse-only `
  -model "$env:APPDATA/playlist-ai/models/qwen2.5-3b-instruct-q4km.gguf" `
  -runtime "$env:APPDATA/playlist-ai/llama/llama-primary.exe" `
  -catalog "$env:APPDATA/playlist-ai/catalog" `
  -prompts internal/evaluation/testdata/music-prompts-v8.json `
  -output bin/prompt-check.json
```

The recorded runs instead used an explicitly owned loopback server with context
4096 and `-server-url`, stopping it in a PowerShell `finally` block. Exact scripts
and all reports are retained locally under the ignored
`bin/prompt-layer-investigation/` directory:

- `retest-parser.ps1`, `retest-intent-verified/`: final repeated native checks.
- `retest-cached-audio-final.ps1`, `replayable-prompts.json`,
  `replayable-intents-final.json`, `cached-audio-retest-final/`: eleven-case replay,
  reports and independent working cache copies for each mode.
- `full-gate.log`, `full-gate-final.log`, `full-gate-reviewed.log` and `full-gate-delivery.log`: initial passing,
  intermediate failing, reviewed passing and final delivery passing complete gates respectively.
- Other `retest-intent-*` and `cached-audio-retest-*` directories retain intermediate
  attempts. Their numeric results should not be substituted for final results.

For replay, use `-replay <report> -replay-parsed -count 5`, separate `-cache` and
`-analysis-dir` output paths, and `-bundle <verified bundle> -cached-audio-only`
for analysis modes. Enhanced additionally uses `-enhanced-evidence <snapshot>`.
Copy the source derived cache before evaluating; never reset real application
data. Omit `-online`. The original eight-record cache and frozen snapshot remain
under `C:/Users/pawel/Downloads/playlistai-enhanced-audio/evaluation/real-preview-20260911`.
Source PCM is not required for this retest and no audio was retained.
