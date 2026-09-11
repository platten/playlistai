# Test coverage

## Target and current status

The requested 95% frontend and backend coverage **is not yet achieved**.
Passing unit tests, browser smoke checks, typechecking, and production builds
do not establish that target. This change adds repeatable measurement, a strict
opt-in threshold check, and behavioral tests without hiding untested modules.

The normal contributor gate and CI run frontend unit tests. Coverage is a
separate check while the coverage backlog remains; CI does not currently enforce
95%. The refactor adds behavioral fixes and coverage together; baseline musical-
quality fixtures were not weakened to increase the reported percentage.

## Reproduce

From the repository root, after the documented setup and frontend dependency
installation:

```sh
bash scripts/coverage.sh
# Or combine the ordinary checks with the coverage target:
bash scripts/test.sh --coverage
```

Windows PowerShell equivalents:

```powershell
.\scripts\coverage.ps1
.\scripts\test.ps1 -Coverage
```

Both coverage scripts run both suites, retain reports on failure, and return a
failure if tests fail or either coverage threshold is missed. Backend comparison
uses the unrounded percentage: 94.995% cannot pass as 95%.

Backend reports: `coverage/backend.out` and `coverage/backend.html`.
Frontend reports: `frontend/coverage/index.html`, `lcov.info`, and
`coverage-summary.json`. Reports are ignored by Git.

## Scope and interpretation

- Backend: Go statement coverage across **every package in `./...`**, including
  root desktop bootstrap, `cmd/`, `build/`, untested packages and test helpers.
  `-coverpkg=./...` counts calls exercised by other packages' tests. The threshold
  checker unions repeated source blocks rather than double-counting them.
  Build-tagged Windows/macOS/native variants need measurement on their own hosts;
  a Linux result is not cross-platform coverage proof.
- Frontend: Vitest V8 lines, statements, functions, and branches, each with a 95%
  threshold. Includes all `frontend/src/**/*.ts` and `*.tsx`, even modules no test
  imports. Only declarations and test files are excluded. Generated Wails bindings
  and dependencies are outside `src/`. Existing Playwright browser checks remain
  useful independent integration evidence; their execution is not included in
  these Vitest percentages.
- Test dependencies are development-only; no test modules are imported by the
  production entry point. Framework configuration follows the
  [Vitest coverage documentation](https://vitest.dev/config/coverage).

CI's host build matrix additionally uploads backend profiles, production-tag
packaging coverage and `GOOS`/`GOARCH`/`CGO_ENABLED`/`CC` provenance. A Windows arm64
cross-build is not an arm64 test run, and worker preflight tests do not execute
valid-model inference. These new hosted reports have not run for the unpushed
refactor. `scripts/coverage.sh` and `.ps1` still enforce the full 95% target when
invoked; they do not hide this backlog behind a passing threshold.

## Earlier measurements (2026-09-10; narrower backend scope)

Measured on Linux, current dirty `main` worktree, using Go 1.27 and Vitest 4.1.11.
These are deterministic test-coverage measurements, not musical-quality scores.

| Metric | Before these tests | After these tests |
| --- | ---: | ---: |
| Backend statements (`internal/...`) | 76.8934% | 78.5187% (12,223/15,567) |
| Core statements | 62.7% | 98.3% |
| Logging statements | 92.6% | 97.9% |
| Resolution statements | 0% | 100% |
| Frontend lines | Not instrumented | 6.81% (88/1,291) |
| Frontend statements | Not instrumented | 6.43% (100/1,554) |
| Frontend branches | Not instrumented | 4.96% (79/1,592) |
| Frontend functions | Not instrumented | 5.03% (27/536) |

`bash scripts/test.sh`: passed all checks, including 35 frontend tests, frontend
typecheck/build, bindings generation, Go vet, pure-Go compilation, race-enabled
Go tests, and lint (zero issues). `bash scripts/coverage.sh`: tests passed but
exit status **1**, correctly rejecting both below-target aggregates. PowerShell
scripts were added for parity but were not executed on Windows. Existing rendered
browser regressions were not rerun for these test-only changes.

## Added behavioral coverage

Frontend tests exercise log severity interpretation, playlist-count versus
musical-fulfillment messages, button disabling, count bounds, progress
accessibility, and theme persistence/cycling/storage failures. Backend additions
cover intent validation and normalization, semantic evidence, feedback contracts,
lossless seed errors, resolver caching and ambiguity, and diagnostic privacy.

## Refactor measurements (2026-09-11)

The new denominator deliberately includes command-line and startup code. Before
the refactor it measured 76.2% across all Go packages with cross-package
instrumentation; it is not directly comparable with the earlier internal-only
78.5187% figure. The final complete inclusive Go run measured **83.8749%**
(14,351/17,110 statements). All tests passed; the 95% threshold correctly failed.

Frontend coverage now exceeds 95% for lines, statements and functions; branches
remain below 95%. The final frontend suite has 150 passing tests:

| Metric | Covered / total | Coverage |
| --- | ---: | ---: |
| Lines | 1,329 / 1,340 | 99.17% |
| Statements | 1,584 / 1,628 | 97.29% |
| Functions | 543 / 558 | 97.31% |
| Branches | 1,519 / 1,669 | 91.01% |

No application source files, native wrappers or error paths are excluded to
improve these numbers.

Focused package measurements (not the whole-application aggregate) include
Deej-AI 96.4%, semantic 96.7%, brute similarity 95.6%, multichannel 91.1%, audio
89.7%, app 75.5%, bridge 82.3%, and llama 70.0%. Production-tag `build` tests and
the non-CGO analysis fallback each measured 100% in separate runs. Their scope
is small and does not establish 95% overall or native inference coverage.

New behavioral coverage includes saved-selection races, unchanged versus edited
history, retained controls/export state, display-only exposure and retry
idempotence, atomic preferences, model revision races, shutdown leases, profile
clear epochs, required spacing, candidate refill, analysis budget exhaustion,
input immutability, URI migration detection, CLI failures and startup teardown.
Seven rendered Chromium workflows are additional integration evidence, not V8
coverage. See [the review report](codebase-review.md).

The remaining work is substantial: backend installer/native-host/device and
worker paths, service failures, metadata/anchor edge cases, and frontend screen
conditional branches. Native inference, GUI launch and Windows/macOS execution
need their actual hosts/assets; other gaps can still be closed with bounded
behavioral tests. Do not present the 95% requirement as completed.
