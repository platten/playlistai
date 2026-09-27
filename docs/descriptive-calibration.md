# Descriptive evidence and calibration

Automatic fit policy `automatic-fit/v4` keeps subjective audio estimates unknown
until the literal criterion has a reviewed calibration for the exact embedding
space. For example, a calibrated `piano` classifier does not establish `soft
piano`. Genre still requires corroboration and vocal admission still uses the
existing strict vocal policy. A below-threshold cosine is unknown, not proof
that a description is absent. High calibrated similarity can oppose a negated
criterion but cannot establish absence.

The source parser retains defining instrument phrases, including soft piano,
pounding drums and distorted guitars, and texture phrases such as spacious
reverberation. Positive defining phrases are essential unless the source explicitly
softens them. OR alternatives, negation and journey scopes retain their roles.
Unknown qualified exclusions retain their full source-owned wording; a partial
instrument label cannot broaden them. Source-atoms v21 also retains full defining
phrases such as detailed textures, deep groove and bright indie-pop energy.
Local journey descriptions retain their stage: sparse at the start, a subtle
pulse in the middle and a calm ending do not become playlist-wide requirements.
These literal descriptions remain unknown without matching evidence. The change
affects new parses only; saved intent and evidence snapshots are not reparsed.

No production calibration is supplied. Automatic remains staged. Unit fixtures
with synthetic labels test control flow only and are not musical-quality evidence.
The exact model fingerprint, calibration version, criterion, validation metrics
and sampled coverage accompany an accepted calibrated signal in saved fit data.
Older assessments without a fingerprint remain unknown for subjective admission.

## Preparing and reviewing a calibration

1. Use the pinned original LAION music CPU bundle described in
   [model preparation](laion-clap-model-preparation.md). Its checkpoint revision
   is `lukewys/laion_clap@4226474:music_audioset_epoch_15_esc_90.14.pt`.
   Preserve the complete `AudioModelIdentity` (including weights, preprocessing,
   runtime and dimension). The live fingerprint is `audio.Fingerprint(model)`.
   Never relabel vectors from another CLAP export as this space. Re-encoding
   actual available audio into a separately versioned pack remains required.
2. Collect independently listened-to development and validation recordings for
   each complete literal criterion. Include realistic negative confounders;
   absent descriptions remain null/unknown. Split by recording identity before
   tuning; keep related versions and copied annotations out of opposite splits.
3. Produce a JSON input with `modelFingerprint`, `kind`, `criterion`, `version`,
   `developmentSet`, `validationSet`, and `examples`. Each example has
   `recordingId`, `split` (`development` or `validation`), finite cosine `score`,
   and `label` (`true`, `false`, or `null`). Dataset identifiers should identify
   immutable reviewed annotation manifests. Keep private audio and annotations
   outside the repository.
4. Run `python3 python/calibrate_audio_similarity.py examples.json > calibration.json`.
   The tool maximizes development recall subject to 90% empirical precision,
   freezes that threshold, then checks the independent validation split once.
   Failure produces no calibration. Unknown labels are excluded, never converted
   to negatives. These empirical metrics do not claim statistical certainty.
5. Review annotation provenance, sample size, rights, confounders, leakage and
   the untouched held-out evaluation before admitting the JSON as a trusted
   `core.AudioSimilarityCalibration` via `AutomaticEngine.WithAudioCalibrations`.
   This is a maintainer code-injection boundary, not a user settings import or a
   self-asserted catalog annotation. There is intentionally no unreviewed
   production artifact loader. Reference similarity uses its exact vector
   `SpaceID` fingerprint and its own criterion; text-audio calibration cannot
   authorize audio-audio similarity in another space.
6. Freeze policy before the 40-prompt development audit and a separate held-out audit. Report discovery
   and descriptive results separately, including every returned song, 90%
   strong-match precision, 80% good/requested slots, existing recall/vocal gates,
   zero known identity/constraint violations and the two-minute latency bound.
   Validation or code-test success alone must not promote Automatic.

The initial implementation tests did not re-encode audio, collect human annotations,
audit held-out songs or download production models. Subsequent bounded local-audio
preparation is documented separately in [the subset receipt](reliable-subset.md).
Direct musical no/without/not/nothing/neither/nor exclusions remain required;
reductions and avoid wording retain their preference strength. A missing model
interpretation cannot erase a complete literal hard exclusion.

## Existing real development-feature experiment — 2026-09-27

The [aggregate receipt](data/descriptive-calibration-development-2026-09-27.json)
reuses 300 recordings' existing original-LAION embeddings and independently
collected unanimous three-listener MTG classification annotations. No new audio,
model downloads, inference or listening took place. This is a development
experiment, not production calibration or an untouched held-out evaluation.

Before reading annotations, the experiment froze a label-blind 200/100 split:
sort SHA256 of `descriptive-calibration-development-2026-09-27/v1`, a NUL byte,
and artist ID; use the first 200 for threshold fitting and the remaining 100 for
development validation. Recording, artist, album, path, full-audio hash and
low-bitrate-audio hash checks found zero overlap across those subdivisions.
Every retained record was checked against the existing development feature IDs
and original development membership. These checks do not establish perceptual
deduplication, absence from pretraining, or independence from earlier development
inspection.

Scores reproduce the prepared engine's duration-weighted segment dot products
from stored float32 vectors, without renormalizing caption ensembles. Every
recording covers two ten-second intervals. The existing calibration tool chose
thresholds using fitting labels only, then applied each frozen threshold once to
the validation subdivision. Missing labels stayed unknown; explicit
`not_acoustic`, `not_electronic`, `not_relaxed`, and `not_danceable` annotations
provided negatives. No absent description became a negative label.

| Literal criterion | Fitting positives / negatives / unknown | Validation positives / negatives / unknown | Validation true / false positives | Labeled precision | Labeled recall |
| --- | ---: | ---: | ---: | ---: | ---: |
| acoustic | 42 / 85 / 73 | 23 / 36 / 41 | 11 / 0 | 100% | 47.8% |
| electronic texture | 70 / 59 / 71 | 34 / 35 / 31 | 10 / 0 | 100% | 29.4% |
| relaxed | 46 / 74 / 80 | 21 / 38 / 41 | 9 / 1 | 90% | 42.9% |
| danceable | 35 / 42 / 123 | 19 / 28 / 53 | No fitted threshold | Not evaluated | Not evaluated |

Three criteria met the tool's empirical development-validation precision check;
`danceable` had no fitting threshold reaching 90% precision. The small labeled
samples, substantial unknown coverage and low recall do not clear release gates.
Precision is conditional on labeled examples, not all above-threshold recordings;
the receipt separately records unknown admissions. No resulting threshold was
installed or enabled. These taxonomies supply no labels for soft piano, pounding
drums, spacious reverberation, distorted guitars, warm intimate vocals or
reference similarity. Those criteria remain uncalibrated. Genre corroboration
and strict vocal policy were not altered.

The local reproducibility workspace is
`/tmp/playlist-ai-automatic-f4ob4_yz/benchmark/descriptive-calibration-development-v1`.
It retains the frozen split policy and four derived calibration-tool inputs;
`python3 python/calibrate_audio_similarity.py PATH/mood_relaxed-examples.json`
reproduces one result. The receipt records source, features, tool, runner and
policy hashes, score arithmetic, all confusion counts and coverage. Per-recording
benchmark annotations and feature vectors remain outside the repository.

A read-boundary error occurred during initial inspection: the first 45 lines of
the mixed-split corpus exposed partial labels for one interleaved held-out
recording (`track_0003112`). Those labels were not used in split allocation,
threshold selection, validation or results. Subsequent processing discarded
non-development objects before parsing their labels. Accordingly, this work
does **not** claim the original held-out corpus remained entirely unopened.

Web descriptions, provider metadata, native classifier outputs and generated
text are not independent listening annotations. They cannot replace the human
labels used here or establish an unmentioned musical attribute as absent.
