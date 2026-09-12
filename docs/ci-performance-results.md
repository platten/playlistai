**CI performance implementation — 12 September 2026**

The implementation preserves full PR validation on GitHub-hosted Windows,
Linux x64/arm64 and macOS. Windows still builds both NSIS installers and measures
full host coverage; its ARM64 installer remains a cross-compilation check.

Implemented changes:

- Run CI on PRs and main pushes, avoiding duplicate feature-branch push runs.
- Start platform jobs alongside Linux validation. `CI complete` checks all job
  outcomes and fails on missing, skipped, failed or cancelled required jobs.
- Save raw Go JSON events, package and individual-test timings, exit status,
  runner hardware/image and Go build environment with every instrumented run.
  The wrapper propagates failure and cancellation; five focused Node tests
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
| Timing helper | Five tests passed, including child failures and cancellation |
| actionlint v1.7.12 | Modified CI/release workflows passed; shellcheck disabled in this Windows invocation |

An initial sandboxed gate could not access the installed compiler and caches;
the authorized native rerun resolved those environment failures. The first native
gate found an unchecked rollback in the new fixture cleanup; it was corrected,
and the subsequent complete gate passed. Diagnostic Go sources are excluded
from application package discovery.

Hosted measurements will be recorded below once the implementation run and
controlled experiments complete. Local timing is not a forecast of GitHub
runner performance. No sharding, higher default concurrency, reduced coverage,
paid runner or self-hosted runner has been adopted without hosted evidence.

The broader audio/recommendation fixture rewrite was assessed and deferred:
those fixtures use concrete validated stores, and bridge tests deliberately
verify full-container wiring. Replacing them with mocks would require new
production seams or reduce integration coverage. Further work should be driven
by the new per-test timings rather than a blanket conversion.
