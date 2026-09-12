# Compact NLU implementation: measured results

12 September 2026. This extends the consolidated Enhanced Hybrid PR. See
[implementation and packaging](intent-nlu-implementation.md) and
[annotation review and training](intent-nlu-review.md).

## What was evaluated

The before/after parser comparison uses the same local Qwen2.5 3B Instruct
Q4_K_M checkpoint, llama runtime, 4,096-token context, temperature zero, four
threads and automatic GPU layers on the available Windows x86-64 host. It uses
the existing 65 public development prompts, including the expanded 30-case
intent audit. MiniLM was **disabled** for this comparison, and no trained
DistilBERT head exists yet. These gains are compiler/schema changes, not learned
model gains. The prior baseline is the recorded comparison at `7fb92fa`; the
updated source compiler is `source-atoms/v4`, wire schema 10.

The 40-playlist replay uses RNG seed 42, the same saved 956,917-record catalog,
249 frozen recording metadata entries, saved artist context and cached derived
audio evidence (169 CLAP-covered recordings and eight DSP/MERT-covered previews).
It performs no new provider lookup or audio acquisition. Two requests ask for
duration, leaving 38 count requests totaling 495 tracks. This small evidence
pool is a deliberately bounded regression fixture, not a representative
listening evaluation. Timings overlapped validation work and are not benchmarks.

## Observed outcomes

| Measure | Recorded 3B baseline | Updated compiler + same 3B |
| --- | ---: | ---: |
| Expanded consumed-intent audit | 11/30 | 29/30 |
| Full count on count requests | 16/38 | 18/38 |
| Tracks returned for 495 requested | 270 | 296 |
| Excluded-artist occurrences | 7 | 0 |
| Artist-only violations | 8 | 0 |
| Endpoint violations | 1 | 0 |
| Missing required recording checks | 1 | 0 |
| Duplicate recording checks | 0 | 0 |

All 65 updated automated parse checks pass, but the stricter consumed-intent
audit still fails the Bach/Glenn Gould composer-versus-performer request. This
is why the automated pass count is not the main quality measure. Playlist replay
still exits nonzero because many requests remain partial or unsupported.

Two checker defects were corrected explicitly: journey genre assertions now
count genre/style facets rather than vocal facets, and a canonical genre
exclusion accepts the consumer's existing `exclude_style` representation only
for the same recognized genre. Regressions still reject missing genres, wrong
values and positive constraints. Original reports are retained; these checker
changes are not presented as learned-model improvement.

## Native language models

The complete Windows `scripts/test.ps1 -PackageParallelism 4` gate passed: race
tests, pure-Go compilation, vet, zero lint issues, generated bindings, TypeScript,
164 frontend tests and production frontend build. The actual Windows amd64 NSIS
package built and passed both native worker capability checks. Six rendered UI
states cover failure/retry, keyboard toggling, both themes, narrow/wide viewports
and unsupported-host behavior. These local checks are separate from hosted CI.

- MiniLM and cased DistilBERT match all 17 reference tokenizer cases, including
  token IDs, attention masks and UTF-8 source offsets.
- MiniLM matches all 17 original PyTorch embeddings: maximum absolute coordinate
  error `2.08183111187477e-7`, minimum cosine `0.999999999999381`.
- A Windows executable embedding MiniLM, DistilBERT and ONNX completed fresh
  setup and MiniLM health with unusable HTTP(S) proxies and no Python on PATH.
- DistilBERT base correctly abstains without a reviewed task head. Sixteen
  offline data/training/export/calibration regressions pass. Tiny synthetic
  training checks the tooling; it does not establish task accuracy.
- Ten new informal probes produced zero MiniLM proposals with the conservative
  filter. Some raw nearest neighbors were wrong; thresholds were not relaxed to
  manufacture coverage. MiniLM remains experimental and off by default.

## Remaining work and limits

The 40 annotation proposals remain unreviewed. User review, real adaptation,
independent complete-request evaluation and calibration are prerequisites to
activating a trained DistilBERT extractor. The initial train/export path learns
BIO mention roles; relation, strength and scope heads are not implemented.
GLiNER checkpoints are archived research candidates; their complete native
decoders and quality comparison remain unverified.

Composer/work-versus-performer meaning is unresolved. Relative energy and
disjoint year alternatives are preserved as unsupported where they cannot be
enforced. Recording-level genre, vocal, mood, instrumentation and duration gaps
still limit actual playlist suitability and length. No blind listening study or
learned-model musical improvement is claimed.

Native Windows x86-64 inference and UI were exercised. Linux, Windows ARM64 and
Developer-ID-signed macOS runtime execution require those hosts. The Darwin
ARM64 support check cross-compiles; this does not prove native execution.

## Retained evidence

Original model files, scripts, draft review records, parity reports, the initial
and final native prompt reports and frozen replay reports are retained under
`C:/Users/pawel/Downloads/playlistai-intent-nlu-v1`. The archive includes checksums.
Model files, catalog data and detailed local diagnostics are not committed.
Public failed-response fixtures are committed beside the schema regressions.
