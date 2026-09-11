# Test coverage

## Target and current status

The requested 95% frontend and backend coverage **is not yet achieved**.
Passing unit tests, browser smoke checks, typechecking, and production builds
do not establish that target. This change adds repeatable measurement, a strict
opt-in threshold check, and behavioral tests without hiding untested modules.

The normal contributor gate and CI now run frontend unit tests. Coverage is a
separate check while the coverage backlog remains; CI does not currently enforce
95%. No existing musical-quality fixtures or application behavior were changed
to increase coverage.

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

- Backend: Go statement coverage across every package in `./internal/...`,
  including untested packages and test helpers. Root desktop bootstrap, offline
  command-line tools under `cmd/`, and packaging code under `build/` are outside
  this application-service metric. The normal gate still tests those packages.
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

## Executed measurements (2026-09-10)

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

The largest remaining gaps are complete frontend screen/lifecycle interactions,
native inference/runtime loading, updater failure paths, and application startup.
Add deterministic dependency fakes and meaningful assertions for these behaviors;
do not replace assertions with execution-only tests or exclude those modules.
