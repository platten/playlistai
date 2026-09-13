# Enhanced audio analysis

Enhanced hybrid adds bounded, optional evidence to the existing recommender.
AcousticBrainz-first, CLAP-first and Deej-AI-only keep their existing behavior.

In Settings, select **Enhanced hybrid**. Under **Recommendation models**, install
MERT and enable **Use MERT to find similar tracks**. DSP preview measurements have
a separate switch and require no model. Model installation itself never changes
either preference or starts preview retrieval. The project owner's existing
Deezer analysis authorization also applies to explicit or generation-bounded
analysis.

**Analyze liked tracks** uses explicit likes/more-like feedback, excluding exposure
and unrelated request feedback. **Analyze candidates** on an enhanced playlist
analyzes its displayed tracks. Each action is limited to 24 distinct catalog
tracks. Cancellation retains completed derived cache entries and cleans transient
audio. Regenerate to use newly cached evidence. Removing MERT retains DSP and
derived caches. **Clear similarity cache** and **Clear DSP cache** delete their
respective derivatives, leaving CLAP, model files, feedback and saved playlists
intact.

## Evidence policy

- CLAP supplies text/audio semantic cosine comparisons, not calibrated probabilities.
- Deej-AI supplies the existing catalog audio/co-occurrence spaces.
- AcousticBrainz supplies archived recording-level measurements and predictions.
- DSP measures preview power, spectral bands, dynamic spread and activity. It is
  not a classifier for genre, instrumental status, soundstage or recording quality.
- MERT supplies audio/audio reference retrieval, content-affinity and continuity
  similarity. **MERT does not understand the text prompt directly.**

Supported DSP preferences are deterministic, soft mappings: deep/more bass,
strong sub-bass, sharp attacks/percussive activity, dynamic swings, and
brightness/darkness proxies. Related measures share an axis instead of receiving
independent bonuses. Unknown values remain unknown. No proxy satisfies an
essential or hard criterion. Preview measurements describe the analyzed preview,
not necessarily the complete recording.

The ranking policy `enhanced-hybrid/v2` uses heuristic additional weights capped
at 0.15 each for DSP and MERT. They share a fixed request-wide denominator; missing
evidence does not remove a candidate's denominator or earn an analyzed-track bonus.
Separate positive references remain separate similarity groups; the best matching
group is used, while several recordings representing one artist are combined by
their existing weights. Negative groups contribute a bounded penalty.
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

Before ordinary candidate retrieval, the orchestrator prepares resolved positive
reference embeddings within the shared budget and searches one compatible local
MERT cache view. Exact cosine results are bounded, recording-deduplicated and
merged as the `mert_audio` retrieval source. The current Deej-AI catalog retriever
continues through unseen candidates when exclusions or missing MERT coverage leave
the eligible result short of the requested count. These additions pass the same
hard constraints, evidence checks, ranking and sequencing rules; they do not need
a MERT embedding.

The first bounded preparation may analyze up to 24 references/candidates with a
two-minute acquisition budget; later candidates without compatible cached evidence
stay unknown. No network or inference runs in ranking/sequencing loops. The local
search projection is additive and partitioned by complete catalog and model
identity. Frozen queries, hits, representations, policy/model identities and
content centroids are serialized into saved requests and fingerprinted in
generation identity. Same-version replay does not rescan a changed MERT cache. A
different catalog requires a new generation. Model/ranking changes never rewrite
old snapshots.

DSP and MERT borrow the existing `audio-analysis.sqlite` connection, with separate
additive tables introduced by M1/M2. MERT identity includes its revision, graph
hash, dimension, preprocessing, pooling and runtime. Updating CLAP does not
invalidate MERT/DSP; changing ranking weights does not require new inference.

## Preparation and limits

See [exact model conversion, checksums and pack commands](mert-model-preparation.md).
All five shipped target packs can be distributed alongside application releases;
each contains a shared FP32 model and the verified target native runtime, with
separate upstream notices. Windows packs additionally carry their required
Microsoft Visual C++ runtime DLLs and terms; the worker verifies app-local
dependency loading. Review the pack's LICENSES.txt before installation.
Python is maintainer-only. Do not commit model packs
or audio to Git. Prepared sources and packs remain in Downloads for future use.

Numerical parity, regression tests and synthetic benchmarks establish correctness
of this implementation, not improved subjective musical taste. MERT layer 12 and
the ranking scales are fixed baselines awaiting bounded musical evaluation. No
supervised heads, retraining, Music-JEPA, bulk catalog audio acquisition or
AcousticBrainz bulk importer are included. The optional importer is deferred
until an eligible corpus and useful exact recording mappings are demonstrated.
