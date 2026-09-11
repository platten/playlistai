# Correctness and maintainability review

## Baseline and scope

Reviewed `main` at `b79c031` on 2026-09-11. The implementation is being revised
in correctness-first stages. This document distinguishes reproduced defects,
source-review findings, measured performance, and outstanding validation.

The accepted scope includes all Go packages (desktop startup, commands and
packaging as well as services), all frontend application modules, and a 95%
coverage target. Neither passing existing tests nor synthetic music fixtures
establish that coverage target or musical recommendation quality.

## Findings and implemented corrections

| Area | Finding | Correction |
| --- | --- | --- |
| Deej-AI ordering | Required tracks bypass hard artist spacing | Separate required tracks with eligible candidates or report a conflict |
| Semantic retrieval | Refill repeatedly returns an already-attempted top-K prefix | Exclude attempted IDs before top-K selection |
| Candidate preparation | Failed subsequent retrieval discards a usable first page | Retain the pool and report interrupted retrieval |
| Audio lifecycle | Session-only deadline loses already accepted tracks | Distinguish session budget exhaustion from parent cancellation |
| Catalog vectors | One-space headers are accepted by two-space readers | Validate dimensions, spaces and overflow before exposing mapped data |
| SQLite paths | Reserved URI characters redirect four stores to the wrong file | Share escaped URI construction with explicit connection options |
| Preferences | Interrupted writes can truncate settings; errors are hidden | Atomic replacement and acknowledged settings changes |
| Download lifecycle | Existing-file hashing ignores cancellation | Context-aware verification, including resumed downloads |
| Saved playlist UI | Delayed selection loads can submit the previous playlist | Selection-keyed loading/ready/error state and stale-response rejection |
| Edited history | Edited description can replay the old request | Treat edited text as a new request; preserve the original record |
| Navigation | Rebuilt result and controls are lost on screen unmount | Keep the active playlist workspace above navigation |
| Exposure semantics | Completed but unseen work affects recent-exposure penalties | Idempotent acknowledgment only after the UI displays a result |
| Assembly | Completion checks duplicate final selection and sequencing | One request-local assembly operation with identical resolved inputs |
| Runtime wiring | Catalog services are published incrementally | Publish a complete service snapshot and own shutdown explicitly |
| Model lifecycle | A slow download/startup can undo a newer model selection or clear | Revision-guard replacement from download through activation; persist before publishing |
| Taste clearing | An in-flight profile can restore data after clear | Invalidate display tokens and profile-save epochs under a shared guard |
| History outcome | Count-only legacy status can invent musical verification | One authoritative outcome reconciliation; preserve unsupported requirements |
| Intent normalization | Vocal preference normalization mutates the input; NaN weights pass validation | Copy caller-owned preferences and reject nonfinite resolution values |
| Configuration | A custom `data_dir` leaves implicit catalog/cache paths in the original user directory | Rebase only implicit paths, preserving explicit overrides |

The recommendation, vector-header and SQLite-path findings have bounded offline
reproducers. Frontend and runtime-state findings originated in source review and
now have behavioral regressions. Independent review also found the scarce-
separator case `A → B → B`, count 4: required same-artist separators are reserved
before distributing remaining journey slots.

## Architecture decisions

- Keep intent migrations and legacy JSON loading, but separate normalization,
  validation and capability assessment internally. No new musical model is needed
  for these fixes.
- Preserve all three recommendation policies, essential criteria, hard exclusions,
  recording deduplication, required waypoint order, lossless seeds, and profile
  snapshots. Completion requires a valid assembled playlist, not just enough
  candidates; analysis stops once that playlist reaches the requested count.
- Edited saved descriptions use current settings and create new requests.
  Unedited history can display the stored result without rebuilding it.
- A display acknowledgment records exposure, never a like. Discarded builds,
  failed previews and UI rerenders do not create feedback.
- Preserve existing user data. Do not silently move or overwrite files that may
  have been created at a truncated legacy SQLite URI path.
- Keep native GUI/inference boundaries explicit and Python confined to offline
  tools. Do not change the default LLM, publish, or deploy this refactor.

## Executed baseline measurements

Linux amd64, Intel Core Ultra 9 285H; repository fixtures only:

- `go test ./... -count=1`: passed.
- Targeted race tests for app, bridge, catalog, recommendation, audio and semantic
  packages: passed.
- `go vet ./...`, `golangci-lint run ./...`, frontend typecheck: passed.
- Frontend: 35 tests passed. Coverage: 6.82% lines, 6.44% statements,
  4.96% branches, 5.05% functions; the 95% check correctly failed.
- All-Go statement coverage: 74.7101% with package-local instrumentation;
  76.2% with `-coverpkg=./...` (includes execution by other packages' tests).
- `BenchmarkCategoryJourney100Tracks`, `-benchtime=5x -count=3`: approximately
  72–77 ms/op, 58.8 MB/op, 82,000 allocations/op. A separate 20-iteration profile
  attributed approximately 99% of sampled allocation volume to category-journey
  sequencing. This is a synthetic sequencing benchmark, not generation latency.
- Real-catalog artist lookup benchmarking was skipped: `PLAYLISTAI_BENCH_CATALOG`
  was not configured. Native Windows/macOS and real model/provider execution were
  not established by this Linux audit.

## Implementation and verification record

### Structure and compatibility

- `core/intent.go` contains the contract; migration/legacy adapters,
  normalization, validation and capability rules now have separate files.
  Intent schema version remains 8; full intent and lossless seeds still replay.
- `multichannel/assembly.go` owns ranking, selection and sequencing for both
  iterative completion and final output. Its cache is request-local, stores only
  completed assemblies, and fingerprints mutable selection/evidence inputs.
  `multichannel/v21` and `deejai/v5` identify changed correctness behavior; old
  recorded algorithm IDs remain untouched, and existing baseline goldens remain.
- `app/catalog.go` publishes a complete runtime snapshot; `app/lifecycle.go`
  owns cancellation leases and shutdown. `main.go` separates headless commands,
  container initialization and native desktop launch, enabling real temporary-
  store startup/cleanup tests without launching a desktop or model.
- `bridge/presentation.go` issues bounded, ephemeral display tokens. Only the
  accepted displayed result is acknowledged; retries/rerenders do not duplicate
  exposure. Navigation retains that token; reopening history creates a new
  presentation without rewriting generation identity. No feedback schema change
  is required. Clear taste data invalidates pending tokens and profile writes.
- History loading now derives status from the domain outcome. A full-count
  legacy result lacking a musical verdict cannot establish essential or strict
  musical fulfillment, including preserved unsupported requirements. Invalid
  JSON results are not written as incomplete history rows.
  Unchanged legacy history without generation IDs uses its backend-issued saved
  presentation token for display reuse, not an invented reproducibility ID.
  Editing descriptions or changing controls still creates fresh generation work.
- Settings persist atomically before acknowledgment. Corrupt preferences remain
  intact and produce an actionable error on mutation. A changed TOML `data_dir`
  now rebases implicit catalog/cache paths; explicit paths (including empty
  overrides) keep their meaning. Set explicit paths to retain assets in the
  previous default directory; no files are automatically relocated.
- All SQLite paths share escaped URI construction. When a missing intended file
  has a recognizable database at the old interpreted URI path, startup warns
  instead of creating an empty replacement. Close the app and back up the
  database plus any `-wal`/`-shm` before reviewing its schema and recovering it;
  detection does not establish ownership and never moves data automatically.
- Command helpers use isolated flag sets and deterministic test seams. Catalog
  packaging now closes compressors/files on failure. Search retains bounded
  match tiers and resolution caches have explicit entry/key limits.

### Executed performance comparison

Same Linux/amd64 Intel Core Ultra 9 285H host, Go 1.27, `-16` benchmark worker
setting, synthetic fixtures, three samples; no musical-quality inference:

| Measurement | Before | After |
| --- | ---: | ---: |
| 100-track category journey | 72.697–73.754 ms/op | 23.039–23.748 ms/op |
| Journey allocation volume | 58.784 MB/op | 1.274 MB/op |
| Journey allocations | 82,036–82,039/op | 2,783/op |
| Common-token catalog lookup | 467–479 µs/op | 230–231 µs/op |
| Lookup allocation volume | 172,105 B/op | 57,472 B/op |
| Lookup allocations | 5,497/op | 2,628/op |

Journey samples retain their exact command below. Lookup ranges were recorded
during the independent audit using the 256-track fixture (100 dimensions in each
of two spaces), repeated `Resolve("a", 5)`, with catalog opening excluded from
timing. Its original iteration/count flags were not retained; the lookup command
below is a reproduction template, not a claim about those missing run settings.

The indexed-state/predecessor-path journey implementation matches a frozen
test-only copy of the previous implementation across 240 deterministic cases
(ties, duplicates, stages, hard/soft spacing, trajectory, count up to 100).
Completed-assembly reuse avoids a second selector pass in both AcousticBrainz-
first and CLAP-first fixture generations. The 100-candidate assembly fingerprint
costs 127.2–128.0 µs/op and approximately 95 KB/op; that fixture does not include
large knowledge snapshots or audio assessments. No speculative ANN/model change
was made; these results do not replace a real-catalog end-to-end benchmark.

```sh
go test ./internal/reco/multichannel -run '^$' -bench '^BenchmarkCategoryJourney100Tracks$' -benchmem -benchtime=5x -count=3
go test ./internal/reco/multichannel -run '^$' -bench '^BenchmarkAssemblyKey100Tracks$' -benchmem -count=3
go test ./internal/catalog -run '^$' -bench '^BenchmarkResolveCommonToken$' -benchmem
```

### Verification and remaining work

`scripts/test.sh` passed on Linux: shell checks, generated Wails bindings,
frontend tests/typecheck/build, Go vet, pure-Go core compilation, full Go race
suite and lint (zero issues). Repeated race regressions cover slow model
replacement/clear, shutdown readers and taste clearing. Production-tag packaging
tests and the non-CGO runtime fallback each measured 100% in their focused runs;
these do not establish native inference or whole-application coverage.

Seven Chromium capture workflows passed: generation, recommendation, wizard,
logs, export, updates and saved-session navigation. Session checks exercise
loading/disabled/edited history, late responses, accepted slider/seed retention,
export drafts and exposure acknowledgments. Dark/light screenshots include
1000×800 and a 390px-width stress case (below the native minimum window width).
Capture scripts use generated enum contracts rather than manually copied values.

The new navigation check can be reproduced with Vite serving port 9245 and a
locally installed Playwright/Chromium pair (no private account data):

```sh
cd frontend && pnpm dev --host 127.0.0.1 --port 9245
# In another terminal, from the repository root:
node scripts/capture-session-ui.mjs /absolute/path/to/playwright/index.mjs /absolute/path/to/chromium /tmp/playlist-ai-session-check
```

The strict **95% target is not complete**. Inclusive results and exact remaining
gaps are tracked in [test coverage](test-coverage.md); below-target scripts fail
without excluding files or lowering thresholds. Host CI now records Go build
settings and uploads separate Linux/macOS/Windows coverage reports, but those
hosted jobs have not run for this unpushed work. Native GUI launch, valid-model
inference, Windows/macOS execution and real-provider musical-quality evaluation
remain unverified here. No release, publication or deployment was performed.

Final inclusive measurements: backend **83.8749%** (14,351/17,110 statements);
frontend **150 passing tests**, 99.17% lines, 97.29% statements, 97.31% functions
and 91.01% branches. The coverage command exits 1 for backend and frontend branch
shortfalls, while the ordinary repository gate passes. These unfinished coverage
items are part of the remaining plan, not an environmental-only limitation.
