# Native intent-model comparison

Measured on application commit `7fb92fa6db2f6fe9bd49eb55f86c628ea692b983`:
**Qwen3.5 9B is the more useful upgrade candidate, but neither larger model is a
substantial remedy for the current recommendation failures.** It improves one
of 30 new consumed-intent cases and one of 38 full-count results, at roughly twice
the baseline median parsing latency. Gemma has no overall advantage over Qwen 9B
here, is slower on this 8 GB GPU, and introduces a restrictive journey error.

The higher priority is fixing source extraction/reconciliation and increasing
verified recording evidence. No application parser, ranking policy, or user
setting changed during the comparison. The results below distinguish intent,
count, evidence labels and the limits of musical-quality inference.

## Experiment design

- Native Windows x86-64, Intel Core Ultra 9 285H, approximately 32 GB RAM,
  NVIDIA RTX 5060 Laptop GPU with 8 GB VRAM. Runtime build 10826 (`73a43d1f6`),
  Vulkan backend. Catalog `1:956917:1788613313`, 956,917 tracks.
- Qwen2.5-3B-Instruct Q4_K_M, Qwen3.5-9B Q4_K_M, and Gemma 3 12B IT QAT
  Q4_K_M are the exact artifacts in the existing managed manifest. Downloads
  retain the manifest's SHA-256 and size checks. Application settings are unchanged.
- Same native server, 4096-token context, one request slot, four CPU threads,
  automatic GPU offload and its default memory reserve. The application supplies
  temperature zero, its actual grammar/template, source facts and reconciliation,
  with `enable_thinking=false`. This measures the supported application path,
  not the models' maximum reasoning performance or long-context capacity.
  The single-slot setting limits unused evaluation capacity; this is not a claim
  about performance with the desktop runtime's automatic slot allocation.
- Each model runs the 35 existing development regressions, then 30 new prompts.
  The 30 new prompts are repeated in a second invocation on the same owned server.
  Models run sequentially. Latency comparisons use the second 30-prompt run,
  after downloads completed. This is a shared laptop, not an isolated performance lab.
- The [new corpus](../internal/evaluation/testdata/model-comparison-v1.json) was
  authored and frozen before observing model outputs. Its byte SHA-256 is
  `49299fb5c1b5cbe3e58d07d6523c2418cfa77a33fdabef22a14d739e8663f09b`.
  It covers classical, Aerosmith, Christian Löffler, workouts, NIN/Manson journeys,
  album/track references, exclusions, durations, dates and scope. It is an
  agent-authored challenge set, not an independently human-labeled benchmark.
  No prompt or production code was tuned against these results.

## Scoring boundaries

The existing `musiccheck` assertions inspect selected consumer fields. A second
audit applies the predeclared prose rubrics to requirements those assertions do
not cover: artist-only requests, required recordings, negated durations, stage-only
vocals, comparison polarity, composer roles and spurious entities. Both scores
are retained. The second audit accepts `exclude_style` and `exclude_genre` for
rock/metal when the current consumer uses the same category matcher; the original
stricter assertion remains unchanged and its failure remains recorded.

Passing these checked conditions is not proof of complete understanding. Raw
model responses, extracted source atoms and the consumed intent are saved
separately so a model error is not confused with a compiler/reconciliation error.
The rubric audit was authored after inspecting the 3B output, using the previously
frozen prose requirements, and applied unchanged to all three models. It is a
diagnostic audit, not a blinded human judgment.

## Intent and runtime results

All 285 attempts produced a valid native-parser intent: 95 per model, with no
rules fallback. Each model's per-case audit findings were identical across the
first and repeated 30-prompt runs. The commands exit nonzero because the musical
intent assertions fail, not because a model crashed or failed to return JSON.

| Measurement | Qwen2.5 3B baseline | Qwen3.5 9B | Gemma 3 12B QAT |
| --- | ---: | ---: | ---: |
| Existing development checks | 35/35 | 35/35 | 34/35* |
| New selected-field checks, each run | 21/30 | 22/30 | 21/30 |
| New expanded consumed-intent audit, each run | 11/30 | 12/30 | 12/30 |
| Median parse time, repeat run | 4.01 s | 8.33 s | 19.49 s |
| p95 parse time, repeat run | 10.59 s | 10.51 s | 22.16 s |
| Model load to healthy endpoint | 3.22 s | 8.37 s | 11.65 s |
| GGUF bytes, decimal GB | 1.93 | 5.68 | 7.30 |
| GPU layers offloaded | 37/37 | 33/33 | 37/49 |
| Loaded NVIDIA memory snapshot | 2,287 MiB | 5,290 MiB | 6,223 MiB |
| Peak server process working set | 2.36 GiB | 5.37 GiB | 8.23 GiB |

The p95 uses the nearest-rank 29th observation of 30. Loaded GPU memory is a
whole-device snapshot, not a measurement of peak model VRAM; process working
set is not total system memory. Partial CPU offload is a material contributor
to Gemma's latency on this host. With only 30 observations per repeated run,
small percentile differences should not be interpreted as reliable tail gains.

\* Gemma's one old-suite failure is a redundant-field assertion: Florence and
the Machine is correctly in `Start`, but absent from `References`. The consumer
adds `Start` to retrieval references, so the reported “artist not preserved”
does not demonstrate an actual lost endpoint. The raw 34/35 score is retained
instead of rewriting the checker during the comparison.

Qwen 9B's additional fully passing case is `aerosmith-neighbours`: it retains
Aerosmith as a positive sound reference while excluding the artist from output.
Gemma's additional case is `loeffler-energy`: it marks “more energy for a run”
unsupported rather than silently losing the relative request. That is improved
honesty, not evidence that it actually selected higher-energy music. These are
one-case gains on a small challenge set, not a general accuracy estimate.

## Frozen recommendation experiment

Replay covers the ten earlier Enhanced Hybrid prompts and the thirty new prompts.
It uses seed `42`, Enhanced Hybrid, the same catalog, and the same saved evidence:

- A common pool of 249 actual recording-metadata entries from the earlier public
  acquisition, with original tags and identities preserved. The pool is biased
  toward that earlier classical search and is not a representative music library.
- The existing derived CLAP cache (169 analysis records) and the compatible saved
  DSP/MERT snapshot (eight recordings in each channel). The SQLite archive also
  contains 15 DSP and 15 representation records, but those are not automatically
  all included in the supplied eight-recording Enhanced Hybrid snapshot.
  Each model starts with an isolated identical cache copy. No new preview audio
  is fetched, and no retained audio is needed.
- Saved context for Aerosmith, Christian Löffler and Nine Inch Nails. Context is
  assigned by the original prompt; the early-Aerosmith scoped plan is used only
  for an early-Aerosmith request. Actual model references still must match it.
- The replay preserves each model's parsed controls, references, criteria and
  exclusions. It does not call a second LLM for fresh retrieval anchors or make
  online metadata calls. This isolates the parsed intent under fixed evidence,
  rather than measuring changing live-provider coverage.

Count fulfillment is measured against the original requested count, not a count
invented by the model. Duration prompts are reported separately: the catalog
does not provide the full-track durations necessary to verify playlist seconds.
Independent checks inspect excluded artists, artist-only output, actual endpoints,
required recordings and duplicate recordings. App strong/close labels are retained
as evidence states, not treated as human ratings.

Where an original prompt names an artist reference, a supplementary diagnostic
compares selected tracks against that artist's fixed catalog representatives in
the Deej-AI audio and co-occurrence spaces. It uses the original reference rather
than the model's possibly incorrect one. These cosines detect reference drift;
they are not probabilities, CLAP/MERT scores, or an independent listening metric.

## Playlist length and quality results

| Measurement | 3B baseline | Qwen 9B | Gemma 12B |
| --- | ---: | ---: | ---: |
| Full original count, 38 count-based prompts | 16/38 | 17/38 | 17/38 |
| Tracks returned on those prompts, of 495 requested | 270 | 279 | 265 |
| Empty results across all 40 prompts | 11 | 12 | 13 |
| Full count on 29 new count-based prompts | 13/29 | 14/29 | 14/29 |
| Full new count with passing intent audit and identity checks | 4/29 | 5/29 | 5/29 |
| All returned tracks, including duration-only prompts | 288 | 299 | 285 |
| App strong / close labels | 4 / 284 | 4 / 295 | 2 / 283 |
| Excluded-artist recordings returned | 7 | 7 | 7 |
| Wrong-artist recordings in an artist-only request | 8 | 8 | 8 |
| Required Hurt recording missing | 1 case | 1 case | 1 case |
| Duplicate recording identities | 0 | 0 | 0 |

The intent-audit/identity conjunction is still not a listening-quality or complete
fulfillment score: the audit can accept an honestly unsupported requirement, and
it checks only the specified conditions. All app strong labels in this replay
occur on cases that fail the broader original-intent audit. App fit labels apply
to the consumed intent; they cannot detect requirements lost before ranking.

Qwen 9B produces exactly the same ordered output as 3B on 35/40 prompts; Gemma
does so on 34/40, including shared empty results. Increasing parameter count
therefore changes little of the current output under this fixed evidence.

Concrete differences and remaining failures:

- **Aerosmith sound without Aerosmith recordings:** all models return 15 tracks,
  but only Qwen 9B preserves the positive artist reference and output exclusion.
  Mean fixed-reference Deej-AI audio affinity is 0.948 / 0.982 / 0.944 for
  3B / 9B / 12B; co-occurrence affinity is 0.521 / 0.748 / 0.522. This supports
  better reference alignment in that example, not a measured listening preference.
- **Manson to NIN, 14 tracks:** output rises from 3 tracks with the wrong first
  artist on 3B to 14 with the requested actual endpoints on both larger models.
  All three nevertheless lose the explicit starting requirement in consumed
  intent. The successful output is not evidence that endpoint enforcement is fixed.
- **Existing NIN to Manson, 15 tracks with no adjacent artist repeats:** 3B and
  9B return 14 tracks; Gemma returns zero because it invents a playlist-wide
  `require_artist: Nine Inch Nails`, conflicting with the Manson endpoint.
  This is a real model-induced regression missed by the older parser assertions,
  separate from the Florence redundant-field false positive above.
- **Classical:** all return 12/12 on the new reading prompt with singing allowed,
  but only 4/12 when singing is forbidden. Increasing the language-model size
  does not supply the missing eligible recording evidence.
- **Workout with no slow ballads:** all return zero under the saved evidence.
  Neither larger model solves strict exclusion coverage.
- **Artist exclusions and artist-only requests:** all return seven forbidden
  Alice in Chains/Stone Temple Pilots recordings in one ten-track result, and
  eight non-Aerosmith recordings in the dated Aerosmith-only request. Full
  counts here are failures, not improvements.
- **Required track:** all return 12 tracks for the Hurt request, but none includes
  Hurt. **Duration:** the spelled 75-minute request produces 18 / 20 / 20 tracks
  without preserving 4,500 seconds. These extra tracks do not establish a better
  duration match; actual playlist seconds cannot be verified from this catalog.

The aggregate replay commands all exit 1 because acceptance conditions fail.
All 120 playlist rows are retained, including empty, partial and incorrect
results. No assertion, production policy or fixture was relaxed to turn these
experiments into passes. No blind listening study was performed.

## Confirmed translation failures

- Qwen 9B preserved a similarity-to-Aerosmith request with an output exclusion
  that 3B lost. Its first and repeated new-prompt runs each pass 22/30 existing
  checks and 12/30 expanded checks; 3B scores 21/30 and 11/30 in each run.
- All three models emit the required Hurt recording in raw JSON, but reconciliation
  removes it. The native diagnostic confirms that the distinct required-track
  mention is considered owned by another extracted fact. Required-track lexical
  extraction recognizes the `Artist - Title` form, while the challenge uses
  `Hurt by Nine Inch Nails`.
- Qwen 9B correctly emits negative aggression for “warm ... over an aggressive
  one” and negative party mood for “not cheerful party music.” The consumed
  intent restores those as positive preferences. This is a translation loss,
  not a lack of musical knowledge in the larger model.
- The wire grammar has no top-level start-reference or duration field. Those
  depend on source extraction. The tested “begin with” construction loses its
  actual start; spelled-out duration loses its target; a negated numeric duration
  becomes positive. Increasing model size cannot by itself change that contract.

For model choice now, retain 3B as the fast baseline and prefer Qwen 9B when the
additional local latency and memory are acceptable. Do not promote Gemma on the
assumption that 12B must outperform 9B. Keep both larger models as comparison
oracles for development; neither should generate its own acceptance labels.
After the translation fixes, repeat the same-evidence comparison and add fresh
independently reviewed prompts. These results do not justify promising larger
or more musically accurate playlists from a model switch alone.

## Remediation priorities exposed by the comparison

1. Fix source-fact ownership before changing the default model. Reconciliation
   must match the typed role and actual mention span: a similarity-artist fact
   must not erase a separate required recording, and a negative occurrence must
   not overwrite a positive similarity reference to the same artist.
2. Extend and verify composition semantics: “begin with,” `Title by Artist`,
   artist-only suffixes, negated quantities, spelled compound durations,
   contrastive mood wording and stage-only vocal requests. Preserve unresolved
   alternatives explicitly rather than silently broadening or dropping them.
3. Keep preferred facets soft. Incorrectly promoting electronic sound to an
   essential criterion can reduce output without satisfying the original intent
   better. Preserve strict exclusions; improve their recording-level evidence
   instead of disabling them to obtain a full count.
4. Add duration and scoped-role support consistently to the typed model contract,
   compiler, UI/history and consumer. Full-duration fulfillment also requires
   reliable full-recording durations; more language-model parameters cannot
   supply that missing catalog evidence.
5. Expand verified recording evidence and test on a separate representative
   library. Artist biographies and genres can seed retrieval, but cannot certify
   that each recording has the requested mood or vocal characteristics.
6. Keep these cases as development regressions after fixing them. Use another
   independently labeled prompt set and blinded listening comparison before
   claiming a general musical-quality improvement or changing model defaults.

## Model provenance and portability

The comparison uses native GGUF inference and Go evaluation tools. It adds no
Python prerequisite, release dependency, setting migration or model-selection
change. The three model files and evaluation evidence are retained separately
under `C:/Users/pawel/Downloads/playlistai-model-comparison`; no model, catalog,
audio, or raw runtime log belongs in the PR.

Primary model cards: [Qwen2.5 3B Instruct](https://huggingface.co/Qwen/Qwen2.5-3B-Instruct),
[Qwen3.5 9B](https://huggingface.co/Qwen/Qwen3.5-9B), and
[Gemma 3 12B IT](https://huggingface.co/google/gemma-3-12b-it).
Exact quantizations, artifact hashes and license URLs come from the repository's
[managed manifest](../internal/intent/modelmgr/models-manifest.json). These are
different model families/generations as well as sizes; observed differences
cannot be attributed to parameter count alone. The desktop manifest lists these
options for native distribution, but this comparison executes only Windows
x86-64. It does not establish native inference performance on other operating
systems or architectures.

## Reproduction and validation

Compact results are available as [aggregate JSON](data/intent-model-comparison-20260912.json)
and [paired per-prompt CSV](data/intent-model-comparison-20260912.csv). CSV triples
are ordered 3B / 9B / 12B; `NA` means the affinity is unavailable, not zero.
The two endpoint-missing entries for Gemma count the absent beginning and end of
its empty journey result; they do not mean two wrong artists were returned.

The local archive `evaluation-20260912.zip` contains the exact helper sources,
frozen prompts, raw responses, source atoms, consumed intents, all 120 playlist
rows, per-case audits, source evidence, runtime logs and SHA-256 inventory. Model
files remain in a separate `models` directory, with verified sizes, hashes,
source URLs and license URLs in `verified-models.json`. Public metadata retains
its source attribution. No preview audio or PCM was fetched or retained for this
comparison. The existing catalog and native runtime/bundle are prerequisites
already managed by the application's setup flow; the archive is not an installer.

To reproduce on this host, use the tested application commit, restore the helper
files to `bin/model-comparison`, and build the native evaluation command with the
documented Go/native compiler setup. Run models sequentially, with the desktop's
LLM stopped to avoid competing inference. The helpers record this host's paths;
adjust catalog, runtime, bundle and evidence paths on another machine.

```powershell
go build -o bin/model-comparison/musiccheck.exe ./cmd/musiccheck
$modelIDs = @('qwen2.5-3b-instruct-q4km', 'qwen3.5-9b-q4km', 'gemma-3-12b-it-qat-q4km')
foreach ($modelID in $modelIDs) {
  & bin/model-comparison/run-model.ps1 -ModelID $modelID
}
go run bin/model-comparison/audit-intents.go @modelIDs
go run bin/model-comparison/prepare-replay.go @modelIDs
foreach ($modelID in $modelIDs) {
  & bin/model-comparison/run-replay.ps1 -ModelID $modelID
}
go run bin/model-comparison/playlist-metrics.go @modelIDs
node bin/model-comparison/paired-analysis.mjs
& bin/model-comparison/summarize.ps1
```

The first repeated baseline and both larger-model repeat runs follow completed
downloads. Source-evidence identities were checked equal across all 40 replay
inputs, and each replay records the initial derived-cache and enhanced-snapshot
hash. All parser outputs remained valid; no failed prompt was discarded.

Validation after the experiment: `scripts/test.ps1 -PackageParallelism 4` passed
with race-enabled Go tests, pure-Go compile, vet, lint (zero issues), regenerated
Wails bindings, frontend typecheck, all 158 frontend tests and production build.
No gate step was skipped. `git diff --check` passed. These application checks are
distinct from the deliberately failing musical-acceptance experiments above;
the new challenge fixture is retained without weakening its assertions.
