**CI performance implementation — 12 September 2026**

The implementation preserves full PR validation on GitHub-hosted Windows,
Linux x64/arm64 and macOS. Windows still builds both NSIS installers and measures
full host coverage; its ARM64 installer remains a cross-compilation check.
Windows now collects full atomic coverage in the race-enabled test pass, with
the separate production-tag coverage check retained. Manual comparisons can
still select the previous two-pass execution.

Implemented changes:

- Run CI on PRs and main pushes, avoiding duplicate feature-branch push runs.
- Start platform jobs alongside Linux validation. `CI complete` checks all job
  outcomes and fails on missing, skipped, failed or cancelled required jobs.
- Save raw Go JSON events, package and individual-test timings, exit status,
  runner hardware/image and Go build environment with every instrumented run.
  The wrapper propagates failure and cancellation; six focused Node tests
  verify collection and process handling.
- Populate large catalog and semantic SQLite fixtures transactionally, retaining
  all records, assertions, file access and reopen behavior.
- Virtualize four expensive MusicBrainz policy tests while retaining production
  retry wrappers, exact attempt counts and stronger delay/spacing assertions.
  Real HTTP integration tests remain.
- Build the llama fake server only when lifecycle tests need it. Compile-only
  test execution no longer builds that executable.
- Avoid duplicate TypeScript checking in contributor/CI gates. Standalone
  frontend builds still typecheck. Tighten frontend Task inputs and consistently
  pin pnpm 9.15.9 across development, CI and release setup.

The CI workflow exposes manual Windows-only experiments for package concurrency
(`default`, `1`, `2`, `4`), separate/combined race and coverage, and the existing
versus daily-refreshed Go cache. Normal PR/main runs ignore those experiment
settings. Manual experiments do not cancel PR validation. Refreshed caches use
exact toolchain/dependency/image compatibility, one daily writer per compatible
scope, and normal GitHub cache isolation; their file counts and expanded sizes
are retained for comparison. These inputs allow measurements without changing
the required default checks.

For example, a maintainer can run a bounded Windows concurrency comparison with
`gh workflow run ci.yml --ref BRANCH -f windows_benchmark=true -f windows_package_parallelism=2 -f windows_test_mode=separate -f windows_cache=standard`.
Choose `combined` to measure one race-and-coverage pass, or `refreshed` to test
the alternative cache. Download `go-coverage-windows-latest` from the run; compare
command durations in `*.summary.json` as well as complete job/setup/save times.
The raw `*.jsonl` files retain detailed events and failure output. Coverage
remains in `backend.out` and `production-build.out`.

The aggregate truth table was exercised in Bash for all 32 combinations of
normal/benchmark mode and dependency success/failure/cancellation/skipping.
Partial experiments report `Windows benchmark complete`, never `CI complete`.
Existing repository rules require PRs and linear history but currently do not
require status checks. This change supplies the aggregate check without altering
repository permissions/rules; it can be made required once this workflow is on
main.

Local validation completed with Go 1.27.0 on Windows amd64, Intel Core Ultra 9
285H (16 logical CPUs), and the verified LLVM-MinGW compiler:

| Check | Result |
| --- | --- |
| Full `scripts/test.ps1 -TestReportDirectory ...` | Passed: installer regressions, bindings, typecheck, 154 frontend tests, frontend build, vet, pure-Go compile, full race suite, lint |
| Catalog + semantic race/shuffle, 20 repetitions | Passed; package elapsed 9.567s / 9.740s |
| MusicBrainz race/shuffle, 20 repetitions | Passed; 79.890s |
| MusicBrainz targeted policy tests | 44.686s baseline to 0.530s; full package 3.319s |
| Llama race/shuffle, three repetitions | Passed; 5.228s |
| Compiled llama tests with empty PATH and no selected tests | Passed, proving no helper compilation was needed |
| Timing helper | Six tests passed, including child failures and cancellation |
| actionlint v1.7.12 | Modified CI/release workflows passed; shellcheck disabled in this Windows invocation |

An initial sandboxed gate could not access the installed compiler and caches;
the authorized native rerun resolved those environment failures. The first native
gate found an unchecked rollback in the new fixture cleanup; it was corrected,
and the subsequent complete gate passed. Diagnostic Go sources are excluded
from application package discovery.

The first complete [implementation run](https://github.com/platten/playlistai/actions/runs/34695314573)
passed every platform job in **12m50s**, compared with **17m46s** in the latest
pre-change PR run. Its Windows job took 12m42s: the race command took 282.67s
and the coverage command 186.01s. Both ran all 53 packages.

The [combined race/coverage experiment](https://github.com/platten/playlistai/actions/runs/34695341525)
passed in a **10m37s Windows job**; its combined Go command took 300.80s.
The coverage profile has the same statement scope as the original Windows
report, and total coverage remains **82.0%**. This supports combining the two
instrumented passes, rather than reducing test or coverage scope. The timing
report confirms a four-core AMD EPYC hosted runner with Go 1.27.1.

Repeated hosted measurements and the final-head checks are recorded in the
consolidated PR's validation section. These first samples do not establish a
long-term median or tail latency. No sharding, higher default concurrency,
reduced coverage, paid runner or self-hosted runner is introduced. Cache refresh
remains an explicit experiment until its restore/build/save tradeoff is proven.

The broader audio/recommendation fixture rewrite was assessed and deferred:
those fixtures use concrete validated stores, and bridge tests deliberately
verify full-container wiring. Replacing them with mocks would require new
production seams or reduce integration coverage. Further work should be driven
by the new per-test timings rather than a blanket conversion.

Additional hosted comparisons (Windows job duration; successful runs only):

| Mode | Duration | Run |
| --- | --- | --- |
| Default package concurrency, separate passes | 12m42s / 11m39s | 34695314573 / 34696038553 |
| Two concurrent packages, separate passes | 13m15s | 34695340224 |
| One package, separate passes | 19m54s | 34695339095 |
| Default concurrency, combined pass | 10m37s / 10m34s | 34695341525 / 34696037391 |
| Refreshed cache, cold, separate passes | 13m57s | 34695342670 |
| Refreshed cache, warm, separate passes | 10m56s | 34696055340 |

The runner actually executed up to four packages concurrently. Lowering package
concurrency did not improve total latency, so the default remains unchanged.
The refreshed cache saved a roughly 2.44 GB expanded module/build tree; its cold
save took 21s and its warm restore took 76s. One cold/warm pair is insufficient
to adopt it, particularly without a combined-pass comparison. It remains opt-in.

Two trials exposed intermittent test failures and are excluded from the timing
table. Run 34696024622 exhausted a real one-second audio budget before reaching
inference. That regression now uses virtual time and a synchronous synthetic
preview transport, preserving the actual deadline, decode/resample, MERT and CLAP
paths. It asserts inference entry, DeadlineExceeded, exactly one call per model,
parent/request survival and no cached MERT result. Twenty shuffled repetitions
with race and full atomic coverage passed in 10.950s.

Run 34696039644 encountered an external fake-server startup failure. Its cause
remains unconfirmed: 60 parallel race repetitions did not reproduce it. The four
external-process lifecycle tests now run in isolation and retain child debug
logs on failure. Assertions, lazy helper compilation and cleanup are preserved.
Twenty shuffled combined race/coverage repetitions passed in 44.328s; the full
package race suite and compile-only check also passed.

The timing wrapper now prints failing-test diagnostics immediately, before later
package output can evict them from the bounded console buffer. A regression
emits 250 later events and verifies that the original assertion remains visible;
raw JSON output is also retained. The full local combined contributor gate passed
with 53 Go packages, all 154 frontend tests and zero lint issues. Final-head
hosted results are recorded in the PR validation section.