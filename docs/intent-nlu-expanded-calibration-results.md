# Expanded DistilBERT calibration diagnostic

The current trained pilot is not worth enabling in the recommendation pipeline.
Increasing the sample from 8 to 128 prompts did not uncover useful additional
extraction: on 120 new prompts it accepted only two count spans, both already
covered by the Go pre-parser. Keep the tooling and archived originals for future
experiments; leave this head inactive. This conclusion concerns the 26-prompt
trained pilot, not DistilBERT's general capabilities.

## Sample and experiment

The original eight user-approved calibration prompts remain unchanged. The
supplement adds 120 distinct English prompts across 12 families, with 529 role
spans and 89 distinct named identities absent from training/original calibration.
All new labels remain **agent-authored and unreviewed**: the diagnostic sample is
128, while formally approved calibration remains eight. No approval was inferred
from the request to investigate.

Model/tokenizer/head/configuration hashes match the previous pilot. No retraining,
activation or app-settings changes occurred. The original calibration scan
reproduced exactly. New labels were semantically inspected and frozen before
inference, not changed to match predictions. Corpus SHA256:
`ed160297232a25a222314f05e9d26058f7d423ce6a03f77ae10cbaf69344b244`.

The fresh author read only allowed training/calibration sources. Before authoring
was reassigned, a coordinating agent's broad search exposed snippets of the full
reviewed corpus; these were not passed to the fresh author. The six reserved
evaluation requests were not executed or used to train/select thresholds in this
experiment. Nevertheless, the overall workflow is not a blinded study. Authored
families and correlated spans are not a representative random sample.

## Results

The comparison threshold was fixed at 0.5 before inference. A separate scan used
the existing 13 thresholds from 0.5 through 0.999, without relaxing the decoder
range or the 99% observed precision / 20 accepted-span gate.

| Sample | Prompts | Gold spans | Correct accepted | Missed | Span recall | Complete role-span requests |
|---|---:|---:|---:|---:|---:|---:|
| Original approved calibration | 8 | 39 | 8 | 31 | 20.51% | 0 |
| New unreviewed diagnostic | 120 | 529 | 2 | 527 | 0.38% | 0 |
| Combined diagnostic | 128 | 568 | 10 | 558 | 1.76% | 0 |

All accepted spans were `quantity:count`. The new successes were “eighteen” in
an acoustic-folk-to-shoegaze journey and “nine” in an Air-inspired request.
No artist, composer, performer, genre, mood, duration, exclusion or endpoint
span was accepted. At every tested threshold of 0.6 or higher, all 120 new
requests produced no proposals. No threshold met the minimum accepted-span gate,
even on the combined diagnostic.

Observed precision was 100% because the model almost entirely abstained.
Two accepted spans are not evidence of reliable extraction: the reported 95%
Wilson lower endpoint is 34.24%, and that calculation itself treats spans as
independent. The combined ten-span lower endpoint is 72.25%. Coverage and
uncertainty make the perfect observed percentage a poor deployment argument.

## Added value over the pre-parser

The actual Go `lexicon.Extract` ran on the same 120 frozen prompts. The comparison
matches quantity roles one-to-one, allowing source evidence to include the noun.
An atom spanning multiple alternative quantities earns no match. Both sides use
the same role scope. This does **not** score numeric values, polarity, strength,
complete intent or musical suitability.

| Role | Gold spans | Go matched spans | DistilBERT matched spans | Correct DistilBERT spans beyond Go |
|---|---:|---:|---:|---:|
| Track count | 25 | 14 | 2 | 0 |
| Duration | 10 | 9 | 0 | 0 |

Go emitted 15 count and 11 duration atoms, so it also has gaps and unmatched
quantities; this does not certify it on every new prompt. The pilot did not fill
its measured quantity gaps or extract the requested musical traits.

On Windows x86-64, Intel Core Ultra 9 285H (16 reported cores/logical processors),
Python 3.12 and ONNX Runtime 1.26.0 CPU with two intra-op threads, the pilot's
tokenization-plus-inference median was 14.99 ms, p95 19.06 ms, with 339.17 ms
session creation. Its ONNX file is 261,279,835 bytes. Go source extraction median
was 0.528 ms. These single-run offline timings have different runtime/measurement
boundaries and are not native desktop end-to-end latency figures.

## Recommendation

The pilot trained on 26 prompts containing 137 spans across 41 of 63 roles.
Twenty-two roles have no training spans, and 30 observed roles have only one or
two. Count has 25 spans; mood preference has six. Calibration measures confidence
and coverage; it cannot teach absent roles.

Reconsider a learned extractor after expanding and reviewing **training** data,
especially artist roles, negation, mood/genre, composers and journeys. Preserve
identity/paraphrase groups, freeze a later independent evaluation set, and measure
additional correct facts and consumed intent against the pre-parser plus LLM.
Relations and preference strength need separate assessment. Then test playlist
length and listening quality. This diagnostic establishes no playlist-quality
gain or general failure of the DistilBERT architecture.

## Reproduction and validation

Inputs, complete predictions, threshold scans, source snapshots and logs remain
under `C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/calibration-expansion-v1`.
Original models/training outputs remain in the parent archive. Use fresh outputs:

```powershell
$root = 'C:/Users/pawel/Downloads/playlistai-intent-nlu-v1'
$intentPython = 'C:/Users/pawel/Downloads/playlistai-enhanced-audio/tools/mert312-venv/Scripts/python.exe'
go run ./cmd/intentnlu source-probe --input "$root/calibration-expansion-v1/prompts-unreviewed.json" --output "$root/calibration-expansion-v1/source-next.json"
& $intentPython python/probe_intent_nlu_calibration.py --data "$root/data-reviewed-v2" --training "$root/training-reviewed-v1" --export "$root/export-reviewed-v1" --input "$root/calibration-expansion-v1/prompts-unreviewed.json" --source-report "$root/calibration-expansion-v1/source-next.json" --output "$root/calibration-expansion-v1/measurement-next" --threads 2
```

The probe refuses reused IDs/prompts/groups/identities, incompatible label spans,
changed model provenance, truncation and existing output directories. It writes
diagnostics only, never an importable calibration candidate. Approved calibration
tooling retains its review requirement. Python remains offline maintainer tooling.

Validation passed: full Windows `scripts/test.ps1 -PackageParallelism 4` gate
(race-enabled Go suite, vet, lint, bindings, typecheck, 181 frontend tests,
production build), 23 Python tests, and independent structural/token-alignment
checks on all 120 prompts. Read-only review verified fixes for comparison
counting/scope defects. There are no app UI or recommendation-policy changes.

- [New prompt review sheet](intent-nlu-calibration-expansion-review.md)
- [Compact measured results](../internal/evaluation/testdata/intent-nlu-calibration-expansion-results-v1.json)
- [Original approved pilot](intent-nlu-approved-review.md)
- [Training and calibration setup](intent-nlu-review.md)
