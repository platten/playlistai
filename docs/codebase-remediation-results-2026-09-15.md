# Remediation implementation and validation — 2026-09-15

This records implementation of the [accepted audit plan](codebase-remediation-plan-2026-09-15.md).
Changes are uncommitted on **codex/codebase-remediation**, based on
9053ee316ffc811d4f069e990d69c3deecaaf7c6. That baseline has the same source tree
as merged main 923318942464a6ade836472d96457a69d1c8f0a3
(tree a240937a06362acf513c005b2c0532dab2ec519b).
No changes have been committed, pushed, released, deployed, or applied to user data.

## Implemented behavior

| Plan | Correction | Regression evidence |
| --- | --- | --- |
| R01 | One catalog manifest validator enforces portable flat names, reserved-name exclusions, case-insensitive uniqueness, valid digests and size budgets before writes. Packaging uses the same validator. | Traversal, absolute paths, Windows device/stream names, case collisions and invalid sizes/digests preserve outside sentinels. |
| R02 | The pinned official text-runtime installer runs inside an operation-owned child profile. A complete validated primary/CPU generation activates through one pointer; prior and independent installations survive failure. | Native PowerShell destination-confinement fixture, cancellation, failed validation/promotion and preserved sentinels. |
| R03 | CSV exports stage and sync complete contents, use no-replace publication for unconfirmed targets, and reconfirm an existing final destination through the native save picker. | Normalization collisions, cancellation, Unicode paths, concurrent creation and failed replacement preserve existing content. Native Windows file publication is exercised. |
| R04 | Mandatory journey separators are reserved before surplus allocation. A bounded artist-group fallback handles feasible allocations missed by greedy ordering. | Surplus-allocation regression, variable gap sizes, endpoints/required order and a 729-pattern independent spacing oracle. |
| R05 | Catalog extraction validates headers before copy and publishes a whole verified generation through a recovery journal. Shared leases protect open catalog handles; abandoned owned stages are cleaned during recovery. | Truncated/oversized/duplicate/link archives, failed promotion, interrupted and retried rollback, orphan cleanup, read-only reuse, multiple readers and failed-open lease cleanup. |
| R06 | Persistent OS locks release on process death. Corrupt MusicBrainz indexes are repaired into a verified sibling and activated without unlinking mapped or previous healthy databases. | Native process-death locking, active-corrupt repair, cancellation, health and promotion failures. |
| R07 | Explicit reference identities survive without Deej-AI vectors and participate in MERT evidence acquisition, retrieval, ranking and replay. | Dynamic positive/negative references and frozen-MERT replay; dense embedding policies remain separate. |
| R08 | Recording exposure penalties apply outside the dense-vector-only scoring branch. | Equivalent dynamic and dense recording-level exposure fixtures. |
| R09 | Dense/content taste projections share effective feedback state. The UI retains acknowledged choice and pending/error state by scope. Persisted sequence makes timestamp ties stable. | Repeated corrections, scope precedence, migration, legacy inserts, VACUUM/reopen stability and UI failure/navigation cases. |
| R10 | Fresh setup requires every supported capability. Completion validates readiness and only closes after successful persistence; failed saves are retryable. | Missing-required-asset, pending validation, save-failure/retry and existing-installation repair cases. |
| R11 | Removed current recommendation modes migrate to Enhanced hybrid at startup. Current choices and historical labels are distinct. | Persist/reopen migration and preserved historical policy fixtures. |
| R12 | An export operation belongs to the retained review draft and captures an immutable payload. Pending work, result and errors survive navigation; acknowledged acceptance is deduplicated. | Deferred completion across navigation, changed selection, duplicate clicks, retries and payload-based progress. |
| R13 | Normal logs omit Soundiiz share URLs and opener errors that might contain them. Privacy copy explains metadata lookup and explicit exports. | Captured-log token checks; no private service calls. |
| R14 | History selection queries summary columns without decoding full saved generation payloads. | Summary/full-record equivalence, bridge tests and measured large-payload listing benchmark. |
| R15 | Audio integrity/worker initialization runs asynchronously with cancellation and stale-operation protection. Loading state blocks premature download/completion. Duplicate initial integrity work is removed. | Native startup/cancellation race tests, pending UI fixtures and actual retained-pack startup measurements. |
| R16 | Export preflight and pip use the same patched exact pins. CLAP uses explicit pooled/projected tensor outputs compatible with Transformers 5.10.1. MERT tokenizer pins are consistent. | Fresh isolated environment, dependency check and actual CLAP/MERT export parity. Cheap consistency checks run in ordinary CI; expensive conversion is explicitly enabled separately. |
| R17 | Risk-driven regressions accompany the changes, and setup/tooling/coverage documentation distinguishes current results from historical measurements. | Full local gate and separate coverage measurements below. The 95% coverage backlog remains open. |

## Compatibility and failure handling

- New recommendation generations use **multichannel/v30**; new taste projections
  use **taste-profile/v3**. The profile contract remains v2. Existing saved
  results and snapshots remain readable; regeneration across algorithm versions
  is not promised to be identical.
- Feedback remains event version 1 with additive sequence. Migration saves old
  insertion order transactionally and supports writes from older app versions.
  Exposure remains separate from positive preference.
- Current removed modes migrate once. Historical AcousticBrainz-first and
  CLAP-first policies remain available for replay. MERT, CLAP and Deej-AI spaces,
  graph hashes and preprocessing identities remain separate.
- Previously completed setups preserve explicit rules-only and preview-off
  choices during repair. New setup cannot bypass required supported steps.
- Catalog activation requires exclusive access; readers hold shared leases until
  their database and vector handles close. An in-use catalog returns a busy
  error instead of exposing mixed generations. Truly immutable legacy catalogs
  without a lock file can still open when write access is denied or the filesystem
  is read-only; mutation by a different privileged identity is outside that assumption.
- Catalog limits are 64 files, 128 GiB aggregate payload and 1 MiB manifest.
  Recovery addresses interrupted application operations. It is not a proof
  against arbitrary disk corruption or every power-loss/filesystem scenario.
- Journey search is capped at 20,000 states. Exhaustion produces an honest
  partial outcome, distinct from a proven required-order conflict.
- New export graphs are isolated validation outputs. They have new measured
  artifact hashes; no deployed pack, embedding cache or calibration was replaced.

## Validation

Host: Windows amd64, Go 1.27.0, Intel Core Ultra 9 285H, 16 logical CPUs,
approximately 32 GiB RAM. The race gate selects LLVM-MinGW
20260908-ucrt-x86_64 with CGO_ENABLED=1; the ordinary shell defaults to CGO_ENABLED=0.
Results refer to this uncommitted remediation worktree, not a hosted CI run.

The complete repository gate passed:

    .\scripts\test.ps1 -TestReportDirectory bin/remediation-validation -HostCoverage

It includes PowerShell parsing, pnpm failure fixtures, compiler installer checks,
native Windows installer replacement fixtures, CI timing helper, Wails binding
generation, frozen-lockfile install, frontend typecheck, **216 frontend tests**,
production frontend build, Go vet, pure-Go compilation, all-package race tests
and golangci-lint (**0 issues**). The initial run found an import-formatting issue;
it was corrected before the passing rerun.

After the final CSV review correction, the all-package native race suite and
whole-repository lint passed again. Their logs are bin/remediation-go-final.log
and bin/remediation-lint-final.log. The coverage profile and provenance were
refreshed with an explicit PowerShell argument array to prevent dotted coverage
flags being split by the shell.

Local logs are under ignored bin/remediation-gate-final.log and
bin/remediation-validation/. The latter retains the exact test arguments,
compiler provenance, package results, timings and coverage profile.

Separate threshold checks correctly fail:

| Metric | Measured | Required |
| --- | ---: | ---: |
| Backend statements, all packages | 20,862 / 25,320 = 82.3934% | 95% |
| Frontend statements | 2,037 / 2,148 = 94.83% | 95% |
| Frontend branches | 1,969 / 2,207 = 89.21% | 95% |
| Frontend functions | 636 / 681 = 93.39% | 95% |
| Frontend lines | 1,672 / 1,719 = 97.26% | 95% |

Commands: go run ./cmd/coveragecheck -profile bin/remediation-validation/backend.out;
pnpm test:coverage from frontend/. Backend flags are -count=1 -race
-coverpkg=./... -covermode=atomic. The inclusive denominator retains the
pre-existing ignored bin/prompt-layer-investigation Go helper discovered by ./....
New diagnostics use underscore-prefixed Go directories. No application package
was excluded and no threshold or musical-quality fixture was weakened.

The final Go JSON report records 1,980 passing test/subtest events and 23 skips.
Skips cover platform-specific behavior, unavailable symlink privileges, opt-in
live/model tests and previously excluded legacy journey fixtures. They are not
counted as passes; exact names and reasons remain in race-tests.jsonl. The separate
retained-model parity/startup measurements below do not imply every opt-in
native model/package test was enabled.

Offline Python validation ran 19 tests across export-environment consistency,
CLAP preparation, the manual workflow driver, and MERT preprocessing: 18 passed
and one symlink test was skipped because this Windows identity cannot create
symlinks. YAML, Python AST, workflow Bash syntax and Actionlint v1.7.12 checks
passed locally.

Browser fixtures passed for capture-session-ui.mjs, capture-export-ui.mjs and
capture-clap-wizard.mjs. Captures cover dark/light themes, narrow 390-pixel
layouts, retained pending/completed export state, completion retry and supported/
unsupported analysis states. Screenshots are in ignored bin/remediation-ui/.
The coordinator inspected narrow pending export and light completion retry.
These are browser fixtures, not native-dialog or live-provider tests.

Independent review checked recommendation/taste/history changes, dataset/export
tooling, and UI/bridge/runtime integration. It found and resolved Unix read-only
error classification, unowned stage directories blocking additional readers,
and a CSV file-creation race at the picker boundary. The CSV regression also
required capturing Windows file identity through an open handle before prompting.
Existing CSV destinations now receive a second save prompt; Wails does not expose
whether its first picker actually confirmed overwrite. Native picker UX remains
a platform verification task.

## Measured improvements

All samples are synthetic or use retained local model assets; none measure
musical quality.

| Operation | Before | After | Method and limit |
| --- | ---: | ---: | --- |
| History listing, 50 records with 256 KiB evidence each | 45.7–54.4 ms; ~27.8 MB/op | 98–189 microseconds; ~26.7 KB/op | Three short five-iteration samples of BenchmarkHistoryListingEvidence; allocation reduction is clearer than precise latency. |
| Dynamic reference lookup, 1,000 rows | 260,275 ns/op; 152,512 B/op; 4,006 allocations | 7,614 ns/op; 2,144 B/op; 45 allocations | BenchmarkDynamicCatalogResolve, fixed 100 iterations, includes one cache build amortized over the run. |
| Dynamic reference lookup, 10,000 rows | 3,377,982 ns/op; 1,524,027 B/op; 40,006 allocations | 85,520 ns/op; 20,201 B/op; 405 allocations | Same fixture; immutable sorted lookup cache invalidates after successful registration. No identity pruning. |
| Host startup return with installed CLAP and MERT | 5,935–6,063 ms | 4.52–5.94 ms | Three same-host launches, warm filesystem cache and cold worker processes. Actual model readiness afterward: 2,894–2,927 ms. This is host construction time, not first GUI paint. |

Dynamic lookup keeps persistence and memory publication serialized to prevent
concurrent upserts from leaving stale metadata in memory. Its optimization does
not bound catalog growth or change dense-vector membership.

CLAP conversion in a fresh CPython 3.12 environment with torch 2.13.0+cpu,
Transformers 5.10.1, ONNX 1.22.0, ONNX Runtime 1.26.0 and NumPy 2.3.4 passed
ten fixtures: maximum absolute error **2.125278115272522e-6**, minimum cosine
**0.9999999403953552**. Seven tokenizer fixtures matched exactly; three
preprocessing fixtures had maximum error **7.62939453125e-6**.

The same core environment plus tokenizers 0.22.2 exported the pinned custom MERT
implementation. Eight fixtures passed: maximum absolute error
**1.0561197996139526e-6**, minimum cosine **0.9999999999826559**.
pip check passed. Reports remain in ignored local validation output directories.
No source checkpoint was downloaded for these local tests; isolated dependency
installation did download wheels. The first long Windows venv path hit a wheel
path-length error; a short isolated path resolved it.

The Transformers update addresses the offline exporter advisories identified in
R16; Python is not a desktop runtime dependency. This does not establish that the
desktop app exposed any particular exploit. Existing dated older-environment
reports remain historical evidence.

## Remaining verification and measured candidates

- **Coverage:** retain the 95% target and continue failure-path tests by risk;
  current ordinary-gate success does not mean that target is reached.
- **Release:** before the next Windows release, perform an isolated prior-release
  Start Menu upgrade and two relaunches, including elevation cancellation and
  recovery. Native installer fixtures passed, but this end-to-end installed-app
  exercise was not performed here. No release was requested in this turn.
- **Native UI/platforms:** save picker behavior needs actual interaction on each
  supported desktop platform. Native Windows publication is tested; Unix
  no-replace fallback and read-only mounts were not executed on native Unix.
  On Unix filesystems without hard links, the exclusive-create CSV fallback
  preserves other files but may expose its new destination before copying ends.
  Shared locks were cross-compiled for Linux/macOS, which is not native testing.
- **Memory:** combined worker RSS/VRAM, low-memory behavior and GPU throughput
  remain a hardware profiling task. No OOM was reproduced and no speculative
  admission-control or idle-unloading policy was added.
- **Playback:** a development React Profiler fixture confirmed every row rerenders
  on playback time updates. For 40 synthetic updates, 20 tracks produced 800 row
  invocations (median commit 2.5 ms, p95 3.7 ms); 100 tracks produced 4,000
  invocations (median 4.2 ms, p95 6.6 ms). One panel was expanded, updates were
  100 ms apart, and the sample used Edge 153.0.4234.32 / React 18.3.1 on this
  busy host. No production rendering-latency claim or refactor follows from this
  short sample. Reproduce with bin/remediation-ui/profile-playback.mjs.
- **Providers/model quality:** live metadata providers, held-out musical quality,
  native inference on other hosts and cold-cache GUI paint remain unmeasured.
- **Delivery:** hosted CI and the manual export workflow have not run for these
  unpushed changes. Model artifacts were neither uploaded nor activated.
