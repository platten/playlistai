# Recommendation Correctness

## Correctness contract

Intent contract v7 separates five concepts that must not be conflated:

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
    P[Prompt] --> I[Validated intent v7]
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

“Electronica” is a reviewed alias for electronic, and “ambient electronica” is
canonicalized to ambient electronic before resolution or semantic lookup. In
explicit entity context, “music by Electronica” remains an artist reference.
If a model fallback parses “ambient electronica” without a compatible semantic
sidecar, generation returns an actionable `unsupported` outcome instead of
attempting to resolve an artist seed named “ambient electronica.”

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
The catalog/sidecar runtime remains pure Go and uses precomputed vectors. Optional
preview analysis adds an isolated native inference worker, described below. No sidecar or
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

Parser identities advance to `rules/v7` and `llama/v7`, invalidating cached
interpretations; the ranking/generation identity advances to `multichannel/v6`.
The intent wire shape is v7 and existing history loaders remain supported.
Legacy seed-only JSON is still accepted by the historical decoder, but is
rejected as a live LLM completion: it cannot bypass current meaning validation.
Generated bindings are regenerated, not hand-edited.

Remaining limits are the absence of a distributed full-catalog semantic
sidecar, subjective/incomplete genre labels, no supported acoustic-energy
feature, and no held-out listening judgments. Unknown or low-confidence rows
are ineligible for essential or strict criteria, which can shorten a playlist.

## Audio-grounded implementation (2026-09-07)

The optional audio pipeline, persistence, native worker, bundle installer and UI
are implemented. A real authorized Deezer preview passed the complete Go/native/
SQLite vertical slice. The wizard now downloads and inference-validates a pinned
public CLAP bundle. **Automatic musical-fit decisions remain disabled for that
bundle:** reviewed calibration, held-out listening comparisons, and clean-machine
platform installations are unfinished gates. Catalog-only
recommendations and existing GGUF installations remain usable.

Listeners independently enable external preview lookups in Settings after installing
a validated bundle. Only artist/title or recording identifiers go to Deezer;
description clauses and taste profiles remain local. The analysis resolver is
separate from the existing playback resolver and has no alternate-provider fallback.

### Intent, identity and eligibility

Intent v7 retains `originalDescription`, genre/mood/texture clauses, references,
exclusions, required tracks and journey stages. Up to three initial specific
recording proposals have independent identity and suitability evidence; one
replacement call is bounded to three more, with all attempts retained. An
artist's catalog does not inherit the representative recording's assessment.
Replay does not ask the LLM to propose replacements again.

Genre strings are open vocabulary. For unfamiliar names the local LLM supplies
musical characteristics and at most three related genres in `genreExpansions`.
Validation requires the original genre to remain an essential criterion. Hints
broaden compatible semantic retrieval and replacement proposals only. The rules
fallback has a smaller recognition vocabulary; accepting arbitrary input does
not establish universal recognition, including for non-Latin descriptions.

`AudioPreviewResolver`, `AudioAnalyzer` and `AnalysisStore` are interfaces in
`internal/ports/audio.go`. Cached, corroborated MusicBrainz ISRCs take precedence
for Deezer lookup. Otherwise full artist/title/version evidence must identify
one unambiguous result among the bounded search results. Mismatches and ambiguity
abstain. Unknown aliases and punctuation variants can therefore reduce coverage.
No unverified first hit is analyzed.

MusicBrainz keeps recording identities, all returned ISRCs, attributed genre/tag
votes, alternatives and explicit unresolved/ambiguous states. Its cache has a
new identity namespace so old first-hit lookups are not trusted. Clients share
the application limiter; generation reads this cache without bulk live
enrichment. Missing recording-level tags remain missing; an artist tag is not
copied to each recording. The public API's request limit and User-Agent contract
are documented by [MusicBrainz](https://musicbrainz.org/doc/MusicBrainz_API).

All candidate channels first pass catalog and applicable strict metadata
eligibility, then the same preview checks against positive and negative clauses.
Positive scores average segment cosines; negative scores use the highest segment
cosine. Essential and strict clauses require affirmative evidence before ranking,
personalization, diversity and sequencing. Journey membership and the final
check use the original stage-specific criteria. Cosines are not probabilities.
Strict vocal absence remains unknown: a CLAP similarity and a short preview
cannot prove that an entire recording has no vocals. No acoustic energy or
vocal detector is claimed by this implementation.

Starting limits are six anchor proposals, `min(200, max(40, 4 * requestedCount))`
new candidate-analysis attempts, and a cancellable 120-second analysis budget.
These are engineering bounds, not benchmark-optimized settings. Cache hits do
not consume the new-analysis count, but still consume time. Unknown, mismatching
or unchecked tracks never fill a result to the requested count. Outcomes retain
actionable `partial`, `unsupported` and `needs_clarification` reasons.

All Deezer API and preview-CDN requests share a process-wide limiter in
`internal/deezerhttp`: request starts are at least two seconds apart, including
redirects, playback and analysis across separate provider instances. Failed
requests consume an interval; canceled waiters do not reserve future slots.
Cached metadata and analysis avoid their corresponding network requests.
Playback downloads at most 8 MiB into transient memory and returns a data URL,
so browser range requests cannot bypass the limiter. Switching tracks or stopping
playback cancels pending work and clears the previous audio source.
Throttle waiting counts against the existing analysis time budget; uncached
generation may therefore return fewer checked tracks before the deadline.

### Memory, persistence and worker lifecycle

`internal/audio` fetches at most 8 MiB of encoded preview audio and accepts at most
60 decoded seconds. HTTP uses no application disk cache and sends `no-store`;
redirects remain on the authorized HTTPS Deezer CDN. Audio and signed URLs are
absent from logging, history, bundle contents and persistent preview files.
Go's MP3 decoder produces stereo PCM, which is converted to mono and deterministically
resampled/quantized to 48 kHz. Ten-second windows repeat-pad the final segment;
padded duration is excluded from coverage. Segment offsets are relative to the
preview; the offset into the complete recording is explicitly unknown.

Owned encoded buffers, decoded PCM, segments and intermediate tensors are
cleared after analysis and on failures/cancellation. The MP3 library's internal
decoder state becomes unreachable on return. Native inference runs behind
bounded stdin/stdout frames in a Go-managed child; cancellation kills and reaps
that child before borrowed buffers are released. This is application-level
lifetime control, not a guarantee against allocator copies, OS swap or crash
dumps. Python, ffmpeg and system-installed inference tools are not desktop
requirements.

`audio-analysis.sqlite` retains reusable segment embeddings, identity/provenance,
coverage, audio SHA-256 and complete model/preprocessing/runtime versions until
explicitly cleared. The lookup includes catalog version, catalog ID and full
artist/title/version key. Stored content and vector shape are validated again on
read. Request assessments are separate from reusable analyses; unavailable
evidence is retained in the generation/history snapshot. Changing a description
can reuse embeddings without fetching audio. Old models' records remain stored
but cannot be mixed with the new embedding space.

Settings exposes storage usage and separate analysis, history and taste controls.
Removing model artifacts retains analysis records. History stores the full
intent, decimal-string RNG seed, parser/catalog/algorithm/profile identities,
audio model/policy and evidence snapshot. Snapshot identity excludes execution
timings and cache-hit counters; runtime-added intent diagnostics do not change
the musical-assessment key. Stored results can be reopened exactly; regeneration
with changed catalog/model/policy/profile evidence is a new result, not a promise
to recreate an older environment.

### Model and bundle provenance

The first candidate is [LAION music CLAP](https://huggingface.co/laion/larger_clap_music),
revision `a0b4534a14f58e20944452dff00a22a06ce629d1`, source weights SHA-256
`5c289311f4a030d768af7ffbfdecd01b008aa64824211899a4e59f4f9d154fd1`.
This is a separate aligned audio/text space; it is never compared directly with
the recommendation catalog's vectors. Go implements RoBERTa byte BPE and the
non-fusion Slaney log-mel processor (64 bins, FFT 1024, hop 480, 1001 frames).
Inputs have fixed shapes: audio `[1,1,1001,64]`, text IDs/mask `[1,77]`, outputs
512 normalized values. Longer clauses abstain rather than silently truncate.

CPU inference uses `onnxruntime_go` v1.31.0 with ONNX Runtime 1.26.0, two intra-op
threads and one inter-op thread. The [Go wrapper](https://github.com/yalue/onnxruntime_go)
requires cgo and a compatible native shared library; it is not pure-Go inference.
The core application still compiles without cgo. No GPU provider is enabled.

A versioned bundle includes checksummed and sized audio/text ONNX weights,
worker, native runtime, vocabulary, merges, preprocessing config, license notices
and a synthetic inference health fixture. Installation checks platform, reference
revision, preprocessing contract, measured parity and calibration-policy metadata.
Artifacts resume independently; healthy verified files are reused. Activation
updates the active pointer only after actual native health inference, leaving
the prior bundle active on failed download/health. Existing GGUF handling is
unchanged. Legacy v1 bundles require a calibrated policy and their own worker.
Version 2 uses the application's isolated native worker and permits installation
with an empty policy; automatic musical-fit decisions stay disabled until a
reviewed policy is supplied. Both encoders must pass native reference checks.

### Executed measurements and validation

Host: Linux amd64 under WSL2 `6.18.33.2-microsoft-standard-WSL2`, Intel Core Ultra
9 285H. These are small development measurements, not quality benchmarks or
clean-machine installation results.

| Check | Observed result |
| --- | --- |
| Reference/export parity | 3 synthetic tones and 7 text inputs; max embedding error `1.043081283569336e-7`, minimum cosine `1.0` |
| Go/reference preprocessing | 3/3 log-mel fixtures within `1e-4`; maximum error `7.62939453125e-6` |
| Go/reference tokenizer | 7/7 exact, including Japanese, Arabic, accents and whitespace |
| Exported CPU inference | Audio 143–167 ms per synthetic segment; text 54–58 ms, two intra-op threads |
| Live authorized preview | Four Tet — Two Thousand and Seventeen, Deezer `410613892`, ISRC `GBXNG1744401`; corroborated artist/title/version |
| Live slice (before request throttling) | 479,827 fetched bytes, 29.9885625 seconds covered; 2,071 ms analysis after 1,951 ms worker health; not representative of current uncached latency |
| Immediate repeat | Same analysis identity; zero fetched bytes; one SQLite analysis record, no request judgment |
| SQLite size | 40,960 bytes for this three-segment recording |
| Native cache/health process run | `/usr/bin/time -v`: peak RSS 1,127,388 KiB; 2.86 s wall time; this is not combined desktop/LLM memory |
| Repository gate | `scripts/test.sh`: bindings, typecheck/build, vet, cgo-free core compile, all race tests, lint passed |
| Rendered UI smoke | Both themes, 560px window, keyboard focus, reduced-motion computed style, ARIA progress status, stale events, stop/partial, wizard, download/retry; no page errors or horizontal overflow |

The live tool used a deliberately named developer catalog identity; this checks
provider identity and the inference/persistence path, not production catalog
coverage. The parity report is committed as
[`data/clap-parity-linux-amd64.json`](data/clap-parity-linux-amd64.json).
No audio, models, embeddings, feature databases or per-user data are committed.
ONNX export uses fixed-shape tracing; the fixture gate tests those shapes only.
The 2 GB development bundle memory budget is provisional and has not been
validated alongside a running LLM on supported desktop platforms.

Deterministic tests additionally cover wrong first results, ambiguous recording
matches, ISRC mismatches, full version suffixes, unfamiliar genres and expansion
limits, explicit exclusions, required-track conflicts, journeys, all retrieval
channels, replacement bounds, stop/cleanup, native failure/restart, interrupted
downloads, retained old versions, stale generation IDs and history replay.
Rendered screenshots are in `/tmp/playlist-ai-recommendation-ui`; these use
explicit bridge fixtures, not a claim that real music analysis is enabled in the
desktop. ARIA and keyboard checks were automated; a human screen-reader session
was not performed.

### Reproduction

Developer-only export dependencies belong in an isolated environment. Download
the pinned Hugging Face snapshot (config, tokenizer files, preprocessor config
and `pytorch_model.bin`) to `/tmp/playlist-ai-clap-source`, then:

```sh
python3 -m venv /tmp/playlist-ai-clap-validation
/tmp/playlist-ai-clap-validation/bin/pip install torch==2.9.1 --index-url https://download.pytorch.org/whl/cpu
/tmp/playlist-ai-clap-validation/bin/pip install -r python/requirements-clap-validation.txt
/tmp/playlist-ai-clap-validation/bin/python -c 'from huggingface_hub import snapshot_download; snapshot_download("laion/larger_clap_music", revision="a0b4534a14f58e20944452dff00a22a06ce629d1", local_dir="/tmp/playlist-ai-clap-source", allow_patterns=["*.json", "merges.txt", "pytorch_model.bin"])'
go run ./cmd/audioparity -tokenizer /tmp/playlist-ai-clap-source > /tmp/playlist-ai-clap-go-parity.json
/tmp/playlist-ai-clap-validation/bin/python python/validate_clap_export.py \
  --source /tmp/playlist-ai-clap-source --go-fixtures /tmp/playlist-ai-clap-go-parity.json \
  --output /tmp/playlist-ai-clap-export
go build -o /tmp/playlist-ai-clap-export/audioworker ./cmd/audioworker
```

Assemble the worker with the platform's official ONNX Runtime 1.26.0 library and
complete model/runtime/worker dependency license notices. The local helper
checksums every artifact; omit `--policy` until reviewed development calibration:

```sh
go run ./cmd/audiopack --export /tmp/playlist-ai-clap-export \
  --source /tmp/playlist-ai-clap-source --worker /tmp/playlist-ai-clap-export/audioworker \
  --runtime /path/to/libonnxruntime.so.1.26.0 --licenses /path/to/licenses.txt \
  --platform linux/amd64 --memory-bytes 2000000000 --output /tmp/playlist-ai-clap-export
go run ./cmd/audiopreview -bundle /tmp/playlist-ai-clap-export \
  -artist 'Four Tet' -title 'Two Thousand and Seventeen' \
  -track-id developer-identity-check -catalog-version developer-validation \
  -data-dir /tmp/playlist-ai-preview-validation -authorized
bash scripts/test.sh
wails3 build
```

Run the live command a second time to check zero-download cache reuse. The
`-authorized` flag records an actual provider agreement, not a way to establish
permission. Packaging defaults to unpublished placeholder URLs and never
activates or publishes a bundle. Future distribution must supply pinned HTTPS
artifact URLs after platform validation.

With the Vite development server on port 9245 and Playwright/Chromium installed:

```sh
node scripts/capture-recommendation-ui.mjs /path/to/playwright/index.mjs /path/to/chromium
```

### Python-free distribution checks

Python dependency audit: desktop and analysis downloads contain no interpreter,
Python package or Python extension module. `cmd/audiopack` replaces the Python
bundle-assembly helper; the cross-build Dockerfile reuses its existing Node
runtime for manifest parsing instead of explicitly installing Python. Python
remains only in offline dataset tools and the reference PyTorch export/parity
workflow, which is not part of ordinary application builds or user installation.

The real bundle was assembled with `PATH`, `PYTHONHOME` and `PYTHONPATH` pointing
to nonexistent directories. Real native audio/text health then passed with an
empty executable search path, unusable Python environment and empty external
library search path. The Linux worker's loaded mappings contained no `libpython`;
`ldd` also found no Python dependency in the desktop, worker or ONNX Runtime.
Repeat this asset-dependent gate with:

```sh
PLAYLISTAI_TEST_AUDIO_BUNDLE=/path/to/native/bundle \
  go test ./internal/audio -run '^TestNativeInferenceWithoutPython$' -count=1 -v
```

The ordinary suite skips that test when no bundle is supplied, keeping CI free
of model downloads. This Linux runtime verification does not claim that the
cross-build Docker image or Windows/macOS installations were executed here.

### Listening evaluation and remaining gates

`cmd/audioeval` consumes the review format in
`internal/evaluation/audio_review.go` and reports frozen-policy ablations for
`existing`, `seed_verified` and `candidate_verified`. Each case must have all
three variants with the same prompt, split and requested count. It rejects
recording **or** credited-artist overlap across development/held-out splits,
including explicit and proposed reference tracks. Missing judgments remain
unknown; they are not counted as either successes or violations. Reports retain
fit/violation denominators, abstentions, outcome counts, preview coverage,
first-result/total latency, peak memory, bytes fetched and cache reuse.

```sh
go run ./cmd/audioeval -input /path/to/reviewed-runs.json -output /tmp/audio-review-report.json
```

Before collecting labels, group all candidate/reference recordings and credited
artists into disjoint development/held-out components. Freeze assignments and
review criteria. Include ambient electronica, electronic music, genre hybrids,
non-Latin references, relaxing-but-not-sleepy, instrumental/no-vocals and ordered
journeys. Review anonymized preview/description/stage pairs without showing
variant or model scores. Record unavailable previews explicitly; independently
review exclusions and resolve reviewer disagreements. Use development data only
to select cosine thresholds, freeze policy/model/preprocessing versions, then
run held-out comparisons. Synthetic control fixtures cannot calibrate this policy.

No reviewed listening dataset or frozen musical-fit policy was available in
this session. Therefore quality comparisons, threshold selection, held-out
metrics and population-level coverage/latency measurements have not been run.
Clean-machine Windows, macOS and Linux installation/removal/resume checks are
also unexecuted; this WSL2 host run is not a substitute. Platform-specific
runtime artifacts and dependency licenses are now pinned in the recommended
bundle; memory coexistence with the LLM still needs platform measurements.
No release, deployment or model publication is included.

## Open descriptions and metadata retrieval (intent v8)

The v8 contract separates open-vocabulary genres, descriptive styles, moods,
textures, instrumentation, and vocal preferences from explicit artist, album,
and track references. Related genres remain inferred retrieval suggestions.
The grammar bounds lists and allows one local repair attempt. Invalid optional
track proposals and invented positive entities cannot become user instructions.
Explicit required tracks, negative entities and destinations remain validated.
Unrepresented intensified quality clauses (for example, "with lots of ...")
retain their original wording as texture preferences; this does not map them to
fixed recommended recordings. Instrumental instrumentation normalizes to vocal
preference when that field was omitted.
Century arithmetic is deterministic; classical centuries mean composition
periods (the 20th century is 1901–2000). A named final destination pins an actual
catalog recording at the end. Unknown composition dates remain unknown.

New prompt generation uses `best_available`: known mismatches are removed,
supported category matches precede personalization/diversity, and other
suggestions carry per-track uncertainty and a partial outcome. Earlier saved
intents default to `verified_only`. This distinction does not establish strict
no-vocals compliance. The optional audio path still retains only preview-eligible
tracks; its quality gates and six-anchor/120-second analysis limits remain.
Genre suggestions are not claims about an artist's entire catalog.

MusicBrainz lookups occur during explicit generation and send extracted music
terms, never the full description or taste profile. Generation permits at most
20 metadata requests within 30 seconds, sharing the existing client request
limiter. Deezer metadata, preview analysis and playback continue to share a
separate process-wide minimum two-second interval. Successful MusicBrainz
responses are cached for 30 days, empty search results for one day; stale data
remains usable offline. Cached exact genre/alias recognition works without an
LLM and without network requests while typing. Full interpretation requires the
optional local language model.

Genre names come from MusicBrainz's public index. The public genre and alias
pages provide labeled subgenre, influence and fusion relationships; the
[documented JSON genre API](https://musicbrainz.org/doc/MusicBrainz_API) does not
expose those relationships. Only subgenre edges establish broader category
membership. Influence and fusion edges broaden retrieval. Alias lookup uses
previously cached aliases; unfamiliar names still remain valid input. The old
small style hierarchy remains for legacy strict feature-sidecar compatibility,
not as an input whitelist for the new genre path.

Recording search matches must corroborate catalog artist/title/version.
Conflicting recording identities remain ambiguous and cannot establish genre
fit. Original release and composition dates are distinct. Album references use
corroborated release-group identities and matching catalog recordings; album-only
constraints require known membership. Unprovable strict album exclusions return
an unsupported outcome. Artist exclusions examine catalog names, feature-credit
text and available recording credits; metadata coverage is not a guarantee
against incomplete provider credits. Full evidence snapshots and string seeds
are retained in intent/history for replay. New model or metadata evidence is not
silently substituted into a saved snapshot.

`cmd/musiccheck` exercises the twelve requested prompts against a real installed
model and catalog. Its checks cover preserved fields, nonempty output,
exclusions and actual final artist; they are not listening judgments. The same
prompt contract fixture runs through parser/resolver/generation tests using
explicitly synthetic recordings. Neither path hardcodes recommended tracks for
the example prompts. Real-model observations are separate from synthetic tests.

The local evaluation used `qwen3.5-4b-q4km.gguf`, SHA-256
`00fe7986ff5f6b463e62455821146049db6f9313603938a70800d1fb69ef11a4`,
with the installed unified `llama` runtime, SHA-256
`97e29d19ff84e5c2fceceaa2b06863928294896d0ebbd8c7ea258ce51a4ddacb`,
an 8,192-token context and automatic GPU offload on WSL2 Linux amd64. The
catalog identity was `1:956917:1788613313`. These are development checks against
the requested examples, not a held-out evaluation or a default-model change.
The [saved live summary](data/music-prompts-v8-live.json) records ten passing
cases from the combined run and two passing targeted rechecks after omission
fixes, including the initial failures. Eleven latest playlists contain 20 tracks;
the explicit two-artist exclusion returns 14. Partial outcomes and unknown
musical-fit evidence are retained. Reported durations include local parsing,
metadata access and retrieval; they are observations, not calibrated benchmarks.

```sh
go run ./cmd/musiccheck -model /path/to/model.gguf -runtime /path/to/llama \
  -catalog /path/to/catalog -online -output /tmp/music-prompts-report.json
go test ./cmd/musiccheck ./internal/intent/schema ./internal/enrich/musicbrainz ./internal/reco/multichannel
bash scripts/test.sh
bash scripts/build.sh
```

The Generate screen preserves the composer and shows elapsed local-model
processing, a request summary, provisional suggestions and checked-track labels.
Stop-and-keep is available only after a checked track arrives. The rendered
fixture suite covers light/dark themes, narrow windows, keyboard focus, ARIA
announcements, reduced motion, stale events and download/error/partial states.
This is not human screen-reader or listening validation.

The AppImage wrapper removes `/mnt` entries from plugin-discovery PATH before
invoking Wails/linuxdeploy, including direct task invocation. The regression
test covers WSL paths, empty PATH components and preservation of Linux tools.
Distribution remains Go/native with no Python runtime; Python is limited to
developer-side dataset preparation and reference/export validation. Old example
music fits were removed from production few-shots and retained only as legacy
contract test fixtures.

### MusicBrainz artist pools and randomized recording samples

Genre retrieval now requests `/ws/2/artist?query=tag:"<genre>"&limit=100`.
It paginates until at least 100 unique artist MBIDs are collected or the provider
is exhausted; the existing 20-request/30-second metadata budget still applies.
Smaller or unavailable pools are recorded explicitly rather than padded with
invented or unrelated artists. Pools are fetched for the original requested
genres before individual recording lookups consume the budget.

Artist order is shuffled using the saved playlist seed. New unseeded generations
receive a random seed before metadata retrieval; replay preserves both that seed
and the evidence snapshot. Sampling rotates through genre pools, examines up to
six catalog-resolved artists (up to three per genre), and retrieves recordings by
artist MBID with `arid:`. Up to three corroborated catalog recordings per sampled
artist are chosen in shuffled order. Credits must contain the queried MBID;
artist/title/version must still match the local catalog. Artist exclusions apply
before sampling and to available recording credits. Artist tags are stored as
artist evidence and never copied onto recording genre features.

The MusicBrainz throttle now operates at HTTP dispatch, shared across clients
and the public host aliases. Live requests, including redirects, start at least
one second apart; delayed concurrent callers cannot dispatch accumulated slots
together. Waiting is cancellable. Sub-second intervals are accepted only for
loopback test servers. Cache hits make no request. Deezer's separate two-second
limiter is unchanged.
