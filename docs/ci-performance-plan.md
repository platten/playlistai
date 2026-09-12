**GitHub Actions performance investigation and implementation plan**

Investigated 12 September 2026 against `8179aa6e184e08afddbae347a28e2e78bc52a641`, on branch `codex/enhanced-audio-remaining`. This is a proposal: workflows, application code, and existing tests were not changed. Experiments used disposable files under ignored `bin/ci-investigation/`. Keep implementation in the existing consolidated PR when implementation is requested. Use GitHub-hosted runners throughout.

**Recommendation**

First remove duplicate workflow execution and the unnecessary dependency delaying Windows, then fix expensive fixture construction and real-time waits. Measure package concurrency after those changes. Improve cache refresh and consider two Windows test shards only when end-to-end measurements justify their setup cost. Preserve current assertions, race checking, coverage scope, and platform packaging during this first pass.

**Measured baseline**

Three completed PR runs establish a small baseline, not a reliable percentile distribution:

| PR run | Commit | Total elapsed | Windows job |
| --- | --- | ---: | ---: |
| [34669812008](https://github.com/platten/playlistai/actions/runs/34669812008) | `92d2ff7` | 20m45s | 17m05s |
| [34670851682](https://github.com/platten/playlistai/actions/runs/34670851682) | `91fd028` | 17m08s | 13m28s |
| [34693088389](https://github.com/platten/playlistai/actions/runs/34693088389) | `8179aa6` | 17m46s | 14m01s |

The latest run's Linux lint/test job takes 3m37s before platform jobs can start. Windows determines completion. Its important steps are:

| Windows step | Elapsed | Interpretation |
| --- | ---: | --- |
| Go setup and cache restore | 84s | Approximately 17s SDK setup, 7s cache transfer, 58s extraction |
| Contributor test gate | 378s | Includes 301s race suite, 31s frontend tests, and setup/checks |
| amd64 installer | 32s | Includes production binding/frontend work |
| arm64 installer | 16s | Existing Task freshness already skips bindings/frontend |
| Full host coverage | 254s | Repeats broad Go test execution with coverage instrumentation |
| Native compiler installation | 27s | Smaller opportunity than tests or cache extraction |
| Wails CLI installation | 5s | Low priority |

The contributor gate plus coverage takes 632s, approximately 75% of Windows duration. Installer construction totals only 48s. Source: [latest Windows job](https://github.com/platten/playlistai/actions/runs/34693088389/job/103552204610).

The same head also ran [push workflow 34693086755](https://github.com/platten/playlistai/actions/runs/34693086755). The PR run used 28m39s of summed job execution; the push run used 28m52s. Together that is **57m31s**. Removing one saves approximately half the work for this update, not necessarily half its elapsed time. A PR checkout can test a merge result whereas a push checkout tests the branch head; preserve the authoritative PR merge validation and retain main-branch validation.

Slow Windows package execution appears in both instrumented passes:

| Package under `internal/` | Race | Coverage |
| --- | ---: | ---: |
| audio | 109.65s | 108.13s |
| catalog | 100.71s | 107.88s |
| bridge | 100.52s | 105.80s |
| enrich/musicbrainz | 93.43s | 82.23s |
| reco/multichannel | 93.40s | 91.74s |
| semantic | 38.49s | 38.17s |

These are overlapping per-package execution durations, not additive workflow costs. Plain Go output is buffered: the initial gap before a package result does not precisely measure compilation. Similar race and coverage timings motivate investigation of fixture I/O, waits, and contention; they do not prove that antivirus, disk flush latency, or CPU is the dominant cause.

**1. Establish useful timing evidence**

Owner: CI/scripts implementer. Intended files: [CI workflow](C:/Users/pawel/Documents/GitHub/playlistai/.github/workflows/ci.yml), [Windows gate](C:/Users/pawel/Documents/GitHub/playlistai/scripts/test.ps1), and a small timing-summary helper under `scripts/`.

Capture `go test -json` from race and coverage runs, and always upload timing artifacts, including on failures. Summarize slow individual tests, package execution, package overlap, and total command duration. Record commit and checkout SHA, event, runner image/version, CPU count, RAM, Go patch version, compiler identity, `CGO_ENABLED`, concurrency flags, and cache key/hit/size. Measure restore, extraction, compilation, test execution, and save separately where instrumentation allows; mark unattributed overhead honestly.

An example Windows race invocation after the existing compiler/frontend setup is:

```powershell
New-Item -ItemType Directory -Force coverage | Out-Null
$ciTimer = [System.Diagnostics.Stopwatch]::StartNew()
go test -json -race -count=1 ./... > coverage/race-tests.jsonl
$ciTestExit = $LASTEXITCODE
$ciTimer.Stop()
[ordered]@{ seconds = $ciTimer.Elapsed.TotalSeconds; exitCode = $ciTestExit } |
  ConvertTo-Json | Set-Content coverage/race-timing.json
exit $ciTestExit
```

Do not let an output parser turn a failed test command into success. Use at least three matched repetitions per finalist on the same hosted image and exact toolchain. Record both warm-cache and cold-cache behavior; alternate candidate order. Compare workflow elapsed time and summed runner time. Expand to ten observations after rollout before assessing tail latency.

Acceptance: existing failures still propagate, all packages remain represented, and telemetry adds little overhead. Initially collect timing data without enforcing a fragile per-test speed threshold.

**2. Eliminate duplicated work and start Windows immediately**

Owner: CI implementer; independent of fixture changes. Intended file: the CI workflow above.

Recommended event policy: PRs targeting main, pushes to main, and manual dispatch. Remove all-branch push execution. A feature branch without a PR can use dispatch; opening a draft PR provides continuous validation. Preserve PR merge-result testing. If merge queues are enabled later, add their required event explicitly.

Remove `wails-build`'s dependency on the complete Linux lint/test job. The platform jobs generate their own inputs already. Run Linux validation and platform validation concurrently, then require an aggregate `ci-complete` job that waits for every required result and fails for failed or unexpectedly skipped dependencies. Validate cancellation and failed-job cases; update required-check settings with the workflow migration when authorized so renamed checks do not strand PRs.

The latest run's dependency accounts for approximately 3m37s of avoidable delay. With everything else unchanged and no extra queueing, moving Windows earlier gives a counterfactual of roughly **14m09s instead of 17m46s**. That is an estimate from the observed dependency, not a measured improved run. Earlier platform startup spends more runner time on commits that fail Linux validation; event deduplication reduces that cost.

Keep packaging output checks in their native jobs. Any release or publication consumer must wait for the full successful aggregate gate. This reordering does not remove checks or authorize a release.

Acceptance: one PR workflow per update, main still validated, all current OS jobs remain required, and no successful aggregate result after a failed/cancelled required job. Rollback: restore the dependency or event policy independently.

**3. Make fixture construction cheaper while keeping the same test cases**

Owner: test implementer, with file ownership allocated by package. Start here before adding parallel tests.

The catalog's [600-row test](C:/Users/pawel/Documents/GitHub/playlistai/internal/catalog/artist_index_test.go:60) calls [an on-disk insert helper](C:/Users/pawel/Documents/GitHub/playlistai/internal/catalog/resolver_test.go:154) 600 times without an explicit transaction. Batch fixture insertion in one transaction and commit before calling the code being tested. Keep the full row count and cross-batch ordering checks. SQLite documents why grouping inserts in transactions reduces commit overhead. [SQLite FAQ](https://www.sqlite.org/faq.html)

A local isolated experiment reproduced the same six-column table and 600 inserts with modernc SQLite 1.58.0. It alternated transaction/autocommit order over three repetitions on Windows amd64, Go 1.27.0, 16 logical CPUs. Each database was closed, reopened, and checked for all 600 ordered IDs:

| Fixture population | Three results | Median |
| --- | --- | ---: |
| Individual commits | 1015.744, 1008.423, 1029.019 ms | 1015.744 ms |
| One transaction | 3.528, 3.503, 3.217 ms | 3.503 ms |

This is approximately 290 times faster **for population in this local microbenchmark**. It is not a 290-fold test-suite improvement or a hosted-runner measurement. Schema creation and reopening were outside the population timer; persistence validation passed for both variants. The probe is [available locally](C:/Users/pawel/Documents/GitHub/playlistai/bin/ci-investigation/sqlite-fixture/main.go), with [raw measurements](C:/Users/pawel/Documents/GitHub/playlistai/bin/ci-investigation/sqlite-fixture-results.jsonl).

Extend this approach selectively:

| Target | Proposed change | Required behavior to retain |
| --- | --- | --- |
| [Semantic sidecar fixtures](C:/Users/pawel/Documents/GitHub/playlistai/internal/semantic/store_test.go:188) | Transactionally construct schema/metadata/vector fixtures, then reopen normally | Corrupt schema, version/identity mismatch, read-only access and persistence |
| [Recommendation pool fixtures](C:/Users/pawel/Documents/GitHub/playlistai/internal/reco/multichannel/recommendation_pool_test.go:42) | Batch setup or use an owned cache fake for ranking-only cases | Actual persistent-cache integration and replay tests |
| Audio policy tests | Use existing service ports with isolated in-memory fixtures when storage is outside the assertion | Store admission, invalidation, versioning, cleanup and cancellation integration |
| [Bridge test containers](C:/Users/pawel/Documents/GitHub/playlistai/internal/bridge/bridge_test.go:21) | Narrow controller/DTO fixtures where full `app.New` is unnecessary | Representative full-container lifecycle, history and persistence tests |

Do not add a production bulk-write API solely to accelerate tests. A test helper can accept a small `Exec` interface implemented by both a database and transaction. Use isolated in-memory SQLite only for cases that do not assert file behavior; keep its connection lifetime explicit. If reusing a populated fixture template later, give each test its own copy and never share mutable databases or containers.

Acceptance: unchanged case cardinalities and assertions, equivalent persisted contents, all affected package regressions pass under race and shuffled order, and hosted JSON timings confirm the improvement. Do not disable production durability, drop coverage, or make the 600-row test too small to cross its batching boundary.

**4. Replace real waits with deterministic time**

Owner: provider-test implementer; independent of catalog/semantic edits. Relevant files under [MusicBrainz tests](C:/Users/pawel/Documents/GitHub/playlistai/internal/enrich/musicbrainz): `instrumental_test.go`, `albums_test.go`, `discogs_cache_test.go`, and `transport_test.go`.

A local uncached JSON run took 48.44s for this package. Five tests accounted for 47.20s: instrumental fallback 23.77s, two album cases 8.46s and 8.16s, persistent Discogs cache 4.81s, and production spacing 2.00s. Several paths use real exponential retry delays despite local fake responses; the persistent-cache test makes three requests with real throttling.

Use `testing/synctest` with synchronous in-process `http.RoundTripper` fakes for policy tests. Assert attempt counts, retry timing, `Retry-After` handling, fallback selection, cancellation, and spacing against virtual time. Keep representative real HTTP transport integration tests. Network sockets do not become virtual merely by wrapping `httptest.Server` in a synctest bubble; construct timers and limiter state inside each isolated test. [Go synctest documentation](https://pkg.go.dev/testing/synctest)

The local evidence suggests roughly 40–45s of removable waiting per package invocation. Confirm the hosted saving after implementation; package overlap means it cannot simply be subtracted from every workflow phase. Existing already-virtual retry tests need no rewrite. Never change production delays, reduce retry counts to one, or shorten timeouts until tests become flaky.

Also inspect audio deadline tests for equivalent virtual-time opportunities. Native processes and real I/O need explicit synchronization or an injected clock; do not move them into synctest without a suitable boundary.

Acceptance: repeated race/shuffle execution preserves all retry and cancellation assertions with no real multi-second waits in the converted policy cases.

**5. Tune parallelism on the actual hosted Windows runner**

The repository is public. GitHub documents four CPUs and 16 GB RAM for standard public-repository Windows x64 runners. The inspected image was `windows-2025-vs2026`; local measurements used 16 logical CPUs and cannot predict hosted scaling. [GitHub runner specifications](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)

Go already runs packages concurrently. `-p` controls package/build process concurrency; `-parallel` affects tests explicitly using `t.Parallel()`. Their defaults follow `GOMAXPROCS`. Raising only `-parallel` does not parallelize ordinary sequential tests. `-cpu=1,2,4` repeats tests rather than distributing them. Preserve `-count=1` for test execution; it does not disable compilation caching. [Go command documentation](https://pkg.go.dev/cmd/go)

Use a staged experiment, not a large Cartesian matrix:

1. Compare existing defaults with `-p 1` and `-p 2`, then explicit `-p 4`, using unchanged tests and the same cache baseline. Repeat after fixture improvements.
2. At the best package limit, trial `-parallel 1`, `2`, and `4` for packages with parallel tests. Sample CPU, memory and disk activity. A smaller limit can win if many SQLite fixtures compete for storage.
3. Add `t.Parallel()` only to independent expensive tests. Owned temporary directories, servers and mutable fixtures are prerequisites. Tests changing process environment, global registries, working directories or helper-process lifecycle require isolation first. `t.Setenv` cannot be used with parallel tests or parallel ancestors. [Go testing documentation](https://pkg.go.dev/testing#T.Setenv)
4. If a single job still limits elapsed time, trial **two Windows package shards** on separate hosted machines. Partition the `go list ./...` output deterministically using recent durations, balancing long packages with short ones. Each package must occur exactly once; fail if newly added packages are missing. Keep whole packages together initially to avoid repeated TestMain and coverage complexity.

Four CPU cores do not justify four workers at every layer: each package is another process, Go runtime threads and native threads add to load, and race instrumentation increases memory demand. Two shards provide separate machines but repeat setup, cache extraction and dependency compilation. Accept sharding only when its complete workflow timing wins, and report the extra runner minutes. Keep package-specific logs and preserve coverage statement identities and counts when merging shard profiles; never concatenate percentage summaries.

Do not parallelize Windows amd64 and arm64 packaging in one checkout: build tasks share `.syso` and executable output paths. Separate machines would duplicate setup to save at most the current 16s shorter packaging stage before overhead, so this is low priority.

**6. Repair cache refresh before adding more caches**

Owner: CI implementer after baseline instrumentation. The latest Windows Go cache archive contained 792,891,717 bytes. Transfer took approximately 7.4s, extraction about 58s. Its exact primary-key hit meant updated build outputs were not saved. setup-go v6 restores module and build caches; its default dependency input is `go.mod`, and an exact cache hit skips saving. This is confirmed in the action source and current log. [Restore implementation](https://raw.githubusercontent.com/actions/setup-go/v6/src/cache-restore.ts), [default inputs](https://raw.githubusercontent.com/actions/setup-go/v6/src/package-managers.ts), [save implementation](https://raw.githubusercontent.com/actions/setup-go/v6/src/cache-save.ts)

Replace the existing automatic cache with an explicitly managed, refreshed cache experiment rather than layering another large cache over it. Begin with one Windows cache and one designated successful writer. Include exact Go patch version, host architecture, compiler identity, job role, dependency checksum and a cache-format version in a compatibility prefix. Use a bounded refresh/source suffix with compatible restore prefixes so source-change build outputs can be published. Include image/toolchain changes in compatibility decisions; do not assume Go detects every change to native libraries.

Measure file counts and extracted sizes separately for module and build storage. Only then trial splitting stable module storage from refreshed build outputs, or caching module archives rather than expanded trees. The latter can move extraction into `go mod` operations, so count that cost too. Avoid separate huge caches for every race/coverage/production variant until measured reuse justifies them. Bound save frequency and retained generations. Preserve ordinary PR cache isolation. [GitHub cache behavior](https://docs.github.com/en/actions/reference/workflows-and-actions/dependency-caching)

Acceptance is improved **restore + build/test + save** time, including misses, rather than a higher hit rate. Keep the previous configuration available as a rollback. Compiler caching must beat the observed 27s installation after accounting for extraction; Wails caching competes against only 5s. Faster network access alone will not solve the observed extraction cost.

**7. Decide whether repeated instrumentation should be combined or rescheduled**

Owner: coordinator with testing review. Make this a measured decision after fixture/wait changes; first preserve all current check scope.

| Option | Benefit to test | Cost or semantic tradeoff |
| --- | --- | --- |
| Keep separate race and coverage passes, optimize fixtures | Smallest behavior change | Executes tests twice on Windows |
| Combine `-race -coverpkg=./... -covermode=atomic` in one full pass | Potentially removes repeated setup/execution while retaining race and coverage | Instrumentation can compound overhead; compare wall time, memory and measured coverage before adopting |
| Independent coverage and race jobs | Overlaps two long phases with their own cores | Repeats expensive setup/cache restore and consumes more runner time; aggregate must require both |
| Canonical Linux full coverage on PRs, full per-host coverage on main/nightly | Reduces PR work | Changes when Windows-specific coverage is measured; requires an explicit coverage-policy decision |

Keep native Windows race/tests and both installer checks in every option. Retain the separate production-tag build tests. Current workflow coverage reports are measurements, not enforcement of the separate 95% coverage target. Do not claim that target is already met, or narrow `-coverpkg` to make timing or percentages look better.

A local LogMel test took about 0.10s normally and 0.62s with broad atomic coverage, showing that instrumentation itself can matter in DSP loops. That single case does not explain approximately 100s remote packages. Preserve audio tensor dimensions and numerical assertions. Factor repeated expensive setup from policy-only cases only where DSP behavior is already covered directly.

**8. Smaller improvements and later experiments**

- Frontend: `typecheck` runs `tsc --noEmit`, while `build` runs `tsc && vite build` with `noEmit` already enabled. Arrange one explicit typecheck per equivalent configuration, keeping the contributor command complete. The current duplicate costs about 4.6s on Windows. Profile the 31s Vitest phase and its workers before tuning them.
- Build freshness: [Task configuration](C:/Users/pawel/Documents/GitHub/playlistai/build/Taskfile.yml) uses broad frontend source globs excluding only node_modules. Restrict to real inputs so generated output does not invalidate freshness. Production binding generation uses different flags; validate equivalence before reusing developer bindings. The arm64 packaging stage already reuses frontend output.
- Pin pnpm consistently: package metadata specifies 9.15.0, while the workflow's broad version resolved to 9.15.9. Align the exact version before introducing frontend artifacts or caches.
- Lazy helper compilation: [llama TestMain](C:/Users/pawel/Documents/GitHub/playlistai/internal/intent/llama/server_test.go:20) builds a fake server even for `go test -run '^$'`. Build it once on demand, or reuse the existing test executable through a guarded helper-process pattern. Keep compile-only checks: replacing them with `go build` alone would omit test-source compilation. Expected saving is modest, around the observed 1–2s helper/package cost per relevant invocation.
- Dependency-aware work selection: consider only after the full gate is fast and observable. A classifier plus always-running aggregate can skip genuinely irrelevant work while recognizing Go, bridge, embed, build, lockfile and workflow dependencies. Keep full validation on main and relevant changes. Plain workflow path filters can leave required checks pending; test docs-only, frontend-only, backend, build, fork PR and cancellation cases.
- Windows storage experiments: compare fixture/cache locations on available volumes using the same benchmark and measure file I/O. Antivirus contribution is unproven. Do not adopt broad protection exclusions as a routine CI optimization.
- Larger hosted runners: benchmark an 8-core Windows runner only if optimized tests remain CPU-bound and the account is eligible. GitHub lists 8-core/32-GB and 16-core/64-GB options; larger runners require suitable organization plans and incur charges even for public repositories. Eligibility was not established. No paid-runner switch is proposed for the first pass. [Larger runner specifications](https://docs.github.com/en/actions/reference/runners/larger-runners), [runner pricing](https://docs.github.com/en/billing/reference/actions-runner-pricing)

**Delivery sequence, verification, and success criteria**

Implement these as focused commits within the existing consolidated PR, with one owner per changed file. Sequence: telemetry and event/DAG correction; independent transactional-fixture and virtual-time changes; integrated concurrency measurements; bounded cache experiment; instrumentation/sharding decisions only if still needed. Have a separate reviewer assess the combined diff and timing evidence before merge.

Run affected packages after each change. For fixtures and concurrency changes, repeat relevant tests under `-race -shuffle=on -count=20` once the long waits are removed; retain failing seeds. Run the complete repository gate and existing native packaging checks before delivery. Compare executed package/test inventories, coverage statement scope, required check outcomes, cancellation behavior, generated binding changes and output architectures. Windows arm64 installers are cross-compiled on x64; this is not native arm64 test execution. Keep Linux x64/arm64 and macOS jobs, compiled native workers, and the existing Python-free desktop runtime requirements.

The first measurable goals are one workflow per PR update, approximately 3m37s less prerequisite delay under the observed schedule, materially cheaper fixture/wait execution, and no lost checks. A **12-minute median PR completion time** is a useful initial engineering target against the current three-run median of 17m46s; it is not a forecast. Set a more aggressive target only after hosted A/B data. Accept complex cache/sharding changes only for a meaningful end-to-end improvement beyond run-to-run noise, and publish their runner-minute cost alongside latency. Do not add individual savings together when stages overlap.

Evidence retained locally includes downloaded run/job metadata and logs, offline JSON package tests, and the six successful SQLite probe variants under ignored `bin/ci-investigation/`. No new GitHub run was triggered, no hosted concurrency experiment was performed, and no complete suite was rerun for this documentation-only investigation. Local Go 1.27.0 and hosted Go 1.27.1 measurements are identified separately. Application performance and musical quality were not measured by these CI experiments.
