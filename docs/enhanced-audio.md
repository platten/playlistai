# Enhanced audio analysis

Enhanced hybrid adds bounded, optional evidence to the existing recommender.
AcousticBrainz-first, CLAP-first and Deej-AI-only keep their existing behavior.

In Settings, select **Enhanced hybrid**, then enable **bounded preview analysis**.
DSP is available without a model. MERT is optional: enter the directory of a
verified pack for your OS/architecture and choose **Install MERT pack**. Model
installation itself never starts preview retrieval. The project owner's existing
Deezer analysis authorization also applies to this explicit analysis path.

**Analyze liked tracks** uses explicit likes/more-like feedback, excluding exposure
and unrelated request feedback. **Analyze candidates** on an enhanced playlist
analyzes its displayed tracks. Each action is limited to 24 distinct catalog
tracks. Cancellation retains completed derived cache entries and cleans transient
audio. Regenerate to use newly cached evidence. Removing MERT retains DSP and the
caches; **Clear enhanced cache** deletes only DSP/MERT derivatives, leaving CLAP,
model files, feedback and saved playlists intact.

## Evidence policy

- CLAP supplies text/audio semantic cosine comparisons, not calibrated probabilities.
- Deej-AI supplies the existing catalog audio/co-occurrence spaces.
- AcousticBrainz supplies archived recording-level measurements and predictions.
- DSP measures preview power, spectral bands, dynamic spread and activity. It is
  not a classifier for genre, instrumental status, soundstage or recording quality.
- MERT supplies audio/audio reference, content-affinity and continuity similarity.
  **MERT does not understand the text prompt directly.**

Supported DSP preferences are deterministic, soft mappings: deep/more bass,
strong sub-bass, sharp attacks/percussive activity, dynamic swings, and
brightness/darkness proxies. Related measures share an axis instead of receiving
independent bonuses. Unknown values remain unknown. No proxy satisfies an
essential or hard criterion. Preview measurements describe the analyzed preview,
not necessarily the complete recording.

The ranking policy `enhanced-hybrid/v1` uses heuristic additional weights capped
at 0.15 each for DSP and MERT. They share a fixed request-wide denominator; missing
evidence does not remove a candidate's denominator or earn an analyzed-track bonus.
Explicit negative content feedback stays a penalty. Optional content centroids
use compatible cached vectors and explicit feedback only, with request feedback
ahead of durable history. No history-wide preview acquisition occurs implicitly.

MERT continuity is bounded at 0.15 and gated by transition smoothness. It adds to
the existing sequencing objective, including local improvements, while preserving
required tracks, journey order and artist spacing. Whole-preview content
continuity is not beat matching, intro/outro analysis or DJ mixing.

Configuration keys in `[recommendation]` are `enhanced_mert_weight`,
`enhanced_dsp_weight`, and `enhanced_transition_weight`, each in `[0,0.15]`.
Defaults are 0.15; zero disables that contribution. These are policy defaults,
not learned or musically calibrated weights.

## Execution and replay

Enhanced analysis branches from original decoded PCM. When CLAP is active, the
enhanced service shares its fetched preview and decoder with DSP/MERT. Original
bytes, PCM, intermediate resamples and tensors are cleared; only intended
representations and measurements are persisted. Preview authorization remains
independent of model installation.

The orchestrator freezes a request-local snapshot before ranking. The first
bounded preparation may analyze up to 24 references/candidates with a two-minute
acquisition budget; later candidates without compatible cached evidence stay
unknown. No network or inference runs in ranking/sequencing loops. Snapshots,
policy/model identities and content centroids are serialized into saved requests
for replay and fingerprinted in generation identity. A different catalog requires
a new generation. Model/ranking changes never rewrite old snapshots.

DSP and MERT borrow the existing `audio-analysis.sqlite` connection, with separate
additive tables introduced by M1/M2. MERT identity includes its revision, graph
hash, dimension, preprocessing, pooling and runtime. Updating CLAP does not
invalidate MERT/DSP; changing ranking weights does not require new inference.

## Preparation and limits

See [exact model conversion, checksums and pack commands](mert-model-preparation.md).
All five shipped target packs can be distributed alongside application releases;
each contains a shared FP32 model and the verified target native runtime, with
separate upstream notices. Python is maintainer-only. Do not commit model packs
or audio to Git. Prepared sources and packs remain in Downloads for future use.

Numerical parity, regression tests and synthetic benchmarks establish correctness
of this implementation, not improved subjective musical taste. MERT layer 12 and
the ranking scales are fixed baselines awaiting bounded musical evaluation. No
supervised heads, retraining, Music-JEPA, bulk catalog audio acquisition or
AcousticBrainz bulk importer are included. The optional importer is deferred
until an eligible corpus and useful exact recording mappings are demonstrated.
