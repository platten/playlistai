# Enhanced relevance evaluation

The desktop evaluator measures the configured Enhanced operation. It does not
infer musical quality from playlist length, model cosine, tags, or passing tests.
No listening judgments are shipped with this change, and tuned defaults must not
be promoted on the basis of the synthetic regression tests.

## Frozen development and held-out families

`internal/evaluation/testdata/enhanced-relevance-families-v1.json` contains 40
families assigned before tuning: 20 development and 20 held out. Each includes a
primary prompt, a paraphrase, tags, and explicit leakage groups. Related artist,
album and paraphrase variants stay in their family. The default run uses the
primary prompt once per family; paraphrases are robustness cases, not independent
samples. The existing prompt suites remain development regressions.

The frozen v1 fixture SHA-256 is
`d0b19e7cf4585976beac7162b781b9e5f4c0f86f92081c83ff093479ad0a0600`.
Intentional prompt or split changes require a newly versioned fixture.

Both halves cover descriptive requests, track and artist similarity, multiple
references, exclusions, instrumentals, classical, journeys and library-only
requests. The named reference artists do not occur in the existing evaluation
fixtures at introduction. These are authored test requests, not evidence that the
catalog can satisfy them. Audit resolved identities for aliases and unexpected
reference overlap before using a held-out result.

Do not inspect held-out results while tuning. Record the dataset SHA-256, code
revision and working-tree patch, policy version and chosen development settings
before opening the held-out packet. Evaluate that fixed policy once. If listening
is inconclusive, retain the uncertainty and obtain more independent evidence;
do not relabel unknowns or repeatedly select a favorable held-out policy.

## Run the actual desktop pipeline

Build `cmd/musiccheck` using the same native build environment as the application:

```sh
go build -o /tmp/musiccheck ./cmd/musiccheck
/tmp/musiccheck -app-data-dir /tmp/relevance-dev-current \
  -app-config /path/to/evaluation.toml \
  -catalog /path/to/installed/catalog \
  -prompts internal/evaluation/testdata/enhanced-relevance-families-v1.json \
  -eval-split development -variant current -cache-condition cold \
  -output /tmp/current-raw.json
```

Prepare an isolated application directory with the existing verified model and
pack installations. No downloads or imports are initiated by this evaluator.
Writable preferences, history, metadata, candidate catalog and analysis databases
must belong to that directory. The normal application directory and symlinked
stores are rejected. Existing immutable model/pack payloads can be hardlinked
when their runtime opens them read-only; copy activation and manifest files and
never hardlink mutable SQLite stores, preferences, or history. The catalog and
language-model path may refer directly to installed read-only assets. No personal
listening data is needed. To evaluate a particular profile, deliberately prepare
that profile in the isolated store and retain it in the saved search snapshot.

`-app-config` is optional; `-model` and `-runtime` override their config paths.
The app store's own selected model, library/discovery configuration and audio
opt-ins remain authoritative, just as on desktop. `-app-data-dir` invokes
`app.New` and the production bridge, so packed CLAP text queries, available
preview evidence, dynamic discovery, source restrictions and optional DSP/MERT
follow the desktop wiring. Standalone CLI provider flags cannot be mixed into
this mode. Inspect the report's capabilities for the application's effective acquisition
policy rather than assuming a persisted legacy toggle controls it or an installed
model was actually available.

Library-only families declare `requiredLibraryMode: "library_only"`. Run these
against a separately prepared library-only store, selecting their exact prompt
with `-case`. A missing library or mismatched source mode is an explicit failed
observation; the evaluator does not silently change source settings. Other cases
can also be selected with `-case` for bounded diagnostics. Cases with no explicit
split in historical fixtures are treated as development cases.

For one selected case, add `-diagnostics /tmp/private-case-trace.json` to retain
bounded local model/provider diagnostics and progress through ordinary errors
or cancellation. The path must be new, outside the isolated application store,
and separate from the report and its case artifacts. The evaluator creates it
with private permissions and exports the
existing memory store on return: at most 2,000 entries and 16 MiB of retained
entry text, with JSON encoding adding overhead. Progress records contain stage,
counts and elapsed time; other diagnostic events can contain prompts and provider
details. Keep this opt-in file local. Collection is disabled by default, and a
forcibly killed process cannot flush its memory store. Tracing does not change
matching rules or turn an operation failure into a successful result.

Each raw case calls `GenerateFromPrompt` under the build's production operation
limit (ten minutes in the current policy), including interpretation and resolution.
Use an explicit `-case-timeout 5m` for an equal-budget comparison with older builds;
the evaluator rejects limits above that build's production cap. Startup and artifact hashing are
outside that per-generation measurement. The report retains the actual returned
seed, complete resolved intent, reproducibility identities, search policy,
candidate/evidence snapshot, profile, result, and failure if present. Raw seeds
are generated by the desktop. Freeze a raw report for policy comparisons:

```sh
/tmp/musiccheck -app-data-dir /tmp/relevance-dev-current \
  -app-config /path/to/evaluation.toml \
  -catalog /path/to/installed/catalog \
  -prompts internal/evaluation/testdata/enhanced-relevance-families-v1.json \
  -eval-split development -variant current -cache-condition warm \
  -replay /tmp/current-raw.json -output /tmp/current-frozen.json
```

Here `-replay` freezes the **resolved input and seed**, then performs a fresh
comparison under the current policy. It intentionally does not pass a prior
saved-result replay token. This isolates ranking/retrieval from parsing. Exact
historical playback instead uses the application's saved search snapshot.
Run baseline and treatment frozen cases from the **same** raw report, with
matched profile/source settings. Keep raw and frozen listening packets separate;
the pooling tool rejects mixed input modes.

The frozen input is the saved, complete consumed intent, including its discovered
knowledge and inferred anchors. It therefore holds that context fixed as well as
the language interpretation; it is not an independent fresh reference-discovery
trial. Use raw runs to evaluate interpretation and seed planning end to end.

The full report also records executable SHA-256, build revision when available,
catalog version, pack activation/manifest identity, language-model SHA-256,
CLAP/MERT identities and bundle fingerprints, config, platform and Go version.
Copy-free baseline source reconstruction must include the baseline working-tree
patch, not just the commit. A baseline without candidate snapshots has unknown
candidate coverage. Older reports remain readable without inventing that data.

The parent runs each desktop case in an owned child process, permits a separate
five-minute startup allowance, and records a failed case if its process must be
stopped. Parser readiness means the requested backend is ready; the actual
backend, fallback, interpretation findings, constraint findings and missing
slots remain separate in each report. Ignored standalone CLI options are rejected.
Reports are replaced atomically after every case with permissions `0600`. The additional
`OUTPUT.listening.json` contains only the measured listening interchange data and
avoids loading large full snapshots when pooling. Neither file contains preview
audio. Do not commit local reports or share private prompts/profile snapshots.

## Pool and listen blind

```sh
go run ./cmd/enhancedeval \
  -pool /tmp/baseline-frozen.json.listening.json,/tmp/current-frozen.json.listening.json \
  -blind-output /tmp/listen.json -key /tmp/private-key.json -seed fixed-review-1
```

The listener packet pools assessed candidates and selected recordings across
variants, shuffles their presentation deterministically, and presents playlists
under anonymous labels. Source scores, variant names, performance measurements
and split labels stay in the private key. Give listeners only `listen.json`.
Listen to the prompt/reference music and the actual recordings or available
previews using the same coverage policy for both variants. Preview judgments
apply only to the portion heard. The tool does not fetch bulk audio.

Fill only these fields in the packet:

- `grades[trackId].requestFit` and `referenceSimilarity`: 0 (mismatch), 1 (weak),
  2 (close), or 3 (strong); use `null` when unjudged or unsupported.
- Each playlist's `opening` and `transitions`: the same 0–3 rubric, or `null`.
- `preferred`: an anonymous playlist label, `"tie"`, or `""` for unknown.

The packet fingerprint rejects changed prompts, track order or candidate sets.
Keep unavailable reference judgments unknown. Judging only selected outputs is
allowed, but full-pool NDCG remains unknown until the entire common pool is
judged. Reviewers must remain blind to the identity key until grading is frozen.

```sh
go run ./cmd/enhancedeval -judgments /tmp/listen.json \
  -key /tmp/private-key.json -baseline baseline -treatment current \
  -policy-frozen > /tmp/listening-report.json
```

`-policy-frozen` is an explicit evaluator attestation that the policy was frozen
before held-out evaluation; it does not establish that fact automatically and
does not tune anything. The existing `enhancedeval -input cohort.json` embedding
comparison remains available and is not a listening evaluation.

## Metrics and promotion

Judged precision uses only known request-fit grades and counts grades 2–3 as
relevant. Mean fit/reference grades, opening and transition grades, and blind
playlist preferences are reported independently. Unknown grades never become
negatives. Relevant-candidate coverage measures retrieved relevant recordings
against the judged common pool; it is unknown if the run has no candidate
snapshot. A useful partial is a nonempty short playlist whose returned tracks
are all judged close/strong. It is unknown while any returned track is unjudged.

Cold/warm/unknown cache conditions, measured operation latency and acquisition
costs remain separate per-run fields. Uninstrumented bytes/new-analysis counters
are `null`, not zero. The evaluator does not claim cache conditions by inference:
label cold/warm only after deliberately preparing independent clean/warmed
stores. Record CPU/GPU, RAM, model build/runtime flags, storage and native host
alongside reports when comparing latency. Cross-compilation is not native timing.

Paired differences are computed between baseline and treatment within a family
and cache condition. Repeats are averaged per family before a two-sided 95%
Student-t interval, so more paraphrases or tracks do not inflate the sample size.
A promotion-eligible report requires at least 20 paired held-out families,
complete output fit judgments, a positive lower confidence bound, an attested
frozen policy, no operation errors, explicit zero constraint violations, and all
compared measurements within each run's recorded budget (at most ten minutes).
Legacy reports without a recorded budget retain their original five-minute cap.
Unequal-budget comparisons measure the combined policy/time change; run the
five-minute treatment control separately to isolate the architecture change.
Constraint/acquisition counters remain
unknown until independently measured; fill the private run records from the
actual deterministic validation evidence, never from playlist length. The gate
therefore stays closed for the unjudged generated reports produced by default.

A statistical eligibility flag is a review aid. Confirm identity/split integrity,
all deterministic regression checks, native timing and the fixed evaluation
protocol before promoting any tuned defaults. No human listening improvement
or held-out success is claimed by this implementation.

## Offline verification

```sh
go test ./internal/evaluation ./cmd/enhancedeval ./cmd/musiccheck
./scripts/test.sh
git diff --check
```

The regressions cover null judgments, deterministic blind pooling, split leakage,
packet tampering, invalid grades, paired intervals, missing constraints, deadline
violations and the balanced family fixture. Real model/provider checks are
explicit opt-ins through `-app-data-dir`; they are not run by the ordinary tests.

The final implementation passed `scripts/test.sh` on 2026-09-25: binding
generation, TypeScript checking, all 249 frontend tests, production frontend
build, Go vet, pure-Go core compilation, the full race-enabled Go suite and
golangci-lint (zero issues), plus the script checks. The gate was run after the
native model workers exited; a concurrent run had failed the existing indexer
test's available-RAM precondition. No assertions were weakened.

`scripts/capture-generate-settings.mjs` passed rendered browser checks at 390px
and 1000px in dark and light themes, including comparison counters, the shared
submission clock, metadata-only stop-and-keep, cancellation and stale-result
protection. These use a mocked bridge and do not establish native Wails-host
behavior. Screenshots are under `/tmp/playlist-ai-relevance/ui-final/`; the full
gate log is `/tmp/playlist-ai-relevance/gate-complete.log`. `git diff --check`
also passed.

## Native development smoke check, 2026-09-25

One `felt-piano` development family was exercised with actual installed assets on
Linux amd64 under WSL2 (kernel `6.18.33.2-microsoft-standard-WSL2`, Go 1.27.1), an
Intel Core Ultra 9 285H, approximately 16 GiB host RAM and an RTX 5060 Laptop GPU
(8,151 MiB reported, driver 617.14). The installed parser was
`qwen3.5-9b-q4km.gguf`; the CLAP and MERT bundles used ONNX Runtime 1.26.0 CPU.
Catalog size was 956,917 tracks and the shared pack contained 252,516 tracks.
Effective Enhanced capabilities reported DSP and MERT available and enabled.

| Executed operation | Generation elapsed | Returned | Observation |
| --- | ---: | ---: | --- |
| Intermediate current, raw prompt | 285.034 s | 4 | Eligible partial, 60 candidates considered, deadline stop |
| Reconstructed baseline, frozen intent | 300.002 s | 0 | Context deadline exceeded; no playlist returned |
| Later intermediate current, same frozen intent | 285.028 s | 5 | Eligible partial, 76 candidates considered, deadline stop |

These are bounded engineering observations from one family, not musical-quality
validation. The raw result used seed `14759568835232052744`; frozen comparison
runs reuse that exact seed and complete consumed intent. Baseline code was
reconstructed from the initial `7b608bf` checkout plus the preserved initial
working-tree patch and three original untracked audio/startup files. Only the
evaluation harness was added to that source copy; it has no new search snapshot.
The two frozen reports' saved request intents were structurally identical.

The operation timer excludes process/model startup, pack opening and artifact
hashing. Log/report timestamps place that preparation overhead at approximately
211 s for the earlier raw run, 111 s for baseline frozen and 142 s for the later
current frozen run. The earlier raw harness redundantly opened the discovery pack
for identity collection; subsequent
runs reuse the application's status. Both stores began with fresh writable
caches and are labeled `cold`; this does not mean cold OS page caches. Provider
availability and timing remain nondeterministic, and output count establishes no
listening preference. The repository's race-test gate ran concurrently with the
later current frozen operation, adding an uncontrolled load difference. These
timings check deadline/partial behavior only; they are not a speed comparison or
benchmark. Windows and macOS native execution were not tested.

Full local artifacts (not committed) include:

- `/tmp/playlist-ai-relevance-eval.ZFGNlz/current-raw.json` and its listening
  sidecar; executable SHA-256
  `93c0eba4eea29cf3d4c9418b54d75f68a7c1d6e606fe0d4dd323500c33405cc0`.
- `/tmp/playlist-ai-relevance-baseline-data.3jZA6q/baseline-frozen.json` and its
  listening sidecar; executable SHA-256
  `dc1ed36e20742caff98f049e7c468125e289673a9fed5fbc4e700a8c2d2e372e`.
- `/tmp/playlist-ai-relevance-current-final.zjx7ft/current-frozen.json` and its
  listening sidecar; executable SHA-256
  `6da80dd06ef0e8c5620a96f76d936c846e4ea947299cb8b35e0f8ec943e02bfb`.

The baseline patch SHA-256 was
`44fa3a49ccb7b87a7e3808ea40ecb442536983f62d5d242952a85a3a6a04aa74`.
The earlier current binary predates the final fairness, journey-tier and partial
refill corrections; the later current frozen binary includes the fairness work
but predates the final journey-tier and partial-refill fixes. These native runs
must not be presented as a timing or relevance certification of the final source
tree, regardless of the artifact directory's name.

The two frozen outputs were pooled with `enhancedeval` under
`/tmp/playlist-ai-relevance-eval.ZFGNlz/`: `frozen-listening-packet.json` contains
one anonymous comparison and 76 pooled candidate recordings,
`frozen-private-key.json` is the separate variant key, and
`frozen-unjudged-report.json` is the evaluation of the unchanged packet. Every
listening judgment remains unknown (zero judged tracks); the paired relevance
interval is null and `promotionEligible` is false. No held-out case was run.
Both native commands completed and closed their application containers; after
the later run, available host RAM returned to approximately 10.9 GiB, allowing a
repository-gate rerun without competing model loads.
