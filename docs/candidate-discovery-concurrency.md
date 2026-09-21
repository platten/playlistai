# Candidate discovery scheduling

Candidate searches begin after intent parsing and reference confirmation. The
frontend submission contract, persisted schemas, RNG seeds, recommendation modes,
and recording eligibility rules are unchanged.

## Ownership and limits

Local retrieval builds independent search jobs and commits their results in the
previous channel order. Search backends explicitly advertise concurrent support;
other backends keep their invocation order. Workers never share the candidate
map or exploration RNG. Optional query errors retain their existing policy, and
cancellation joins every lane.

Coordinators run at most `min(8, GOMAXPROCS)` jobs. Actual brute-force, semantic,
installed metadata, and installed vector scans share a process-wide
`GOMAXPROCS` ceiling. Coordinators do not acquire CPU permits. Installed catalog
pins also retain their existing shared generation admission budget, including
rejection of inconsistent capacities.

Exact-search sessions publish immutable cached prefixes under short bookkeeping
locks. Identical concurrent queries share a fill; canceled waiters leave without
canceling other callers. A canceled fill can be retried by a surviving waiter.
The cache still holds at most 64 queries with at most 4,096 matches per query.

## Discovery and audio

The optional `CandidatePrefetcher` lifecycle starts after resolution checks and
before local retrieval. Planning has a private snapshot and RNG; only the stream
coordinator adopts the plan. At most four recording pages may be queued, running,
or completed but unused. Speculation fetches raw responses, using the existing
response cache, request coalescing, provider limiter, retry handling, and budgets.
The next offset for an artist is established only after its preceding page is
consumed. Required lookups take priority over queued speculative HTTP work.

Candidate consumption retains ownership of artist rotation, identity matching,
registration, deduplication, evidence, and snapshot changes. Unused responses may
remain in the response cache, but do not register tracks or enter replay history.
Unused failures do not invalidate a completed playlist. An installed offline
MusicBrainz index retains its synchronous offline-first path without speculative
network pages.

Prefetch continues during existing bounded audio batches. Assessments and
checked-track callbacks are consumed in shortlist order, including mixtures of
packed evidence and concurrently checked previews. Continuation queries still
wait for accepted selections. Stop-and-keep cancels discovery while retaining
eligible accepted tracks; generation cancellation discards the result. Cleanup
cancels and joins all prefetch work before final snapshots and pin release.

## Verification and measurements

The deterministic regressions cover reversed search completion, serial fallback,
ordered errors, identical-query coalescing, cache eviction, cancellation, shared
scan admission, base/pack overlap, bounded page speculation, replay-equivalent
snapshots, required-request priority, and mixed packed/preview shortlist order.
The repository gate also exercises existing recommendation modes, journeys,
exclusions, partial results, catalog replacement, and frontend submission and
cancellation tests.

Measurements use synthetic fixed inputs and offline provider/audio fixtures;
they establish scheduling behavior and latency, not recommendation quality or
production Submit latency. No real provider or model download is needed.

### Measured fixtures

Measured on Linux/amd64 (WSL virtual machine), Intel Core Ultra 9 285H, 16 logical
CPUs, Go 1.27.1, `GOMAXPROCS=16`, baseline commit `109651e` plus this working-tree
change. Seeds are 42; catalogs and embeddings are synthetic. Cold means a new
request-local exact-search cache, not eviction of operating-system file caches.
No live provider or production musical-quality measurements were performed.

| Workload | Serial | Concurrent |
| --- | ---: | ---: |
| Cold 16,384 rows × 32 dimensions, one reference | 0.837–0.852 ms | 0.587–0.603 ms |
| Same cold catalog, four references | 3.01–3.07 ms | 1.13–1.14 ms |
| Four references, fixed 2 ms delay per search | 17.76–17.78 ms | 2.33–2.34 ms |
| 4,096-track pack, two metadata and two MERT queries | 234.7–237.7 ms | 206.1–208.2 ms |
| Warm 16K catalog, one reference | 47.4–49.3 µs | 51.5–52.1 µs |
| Warm 16K catalog, four references | 198–240 µs | 196–199 µs |

The first warm-cache prototype paid unnecessary goroutine and result-slot costs.
Ready exact-cache queries now stay serial. The remaining single-reference
readiness lookup cost is about 3.5 µs and 640 allocated bytes on the 16K fixture;
it does not change the returned results. Cold multi-query and installed-pack
improvements justify the default scheduling, while warm hits avoid the new pool.

The provider fixture uses eight artists, FIFO pages, a real provider admission
limiter with a 2 ms interval, 2 ms responses (one artist takes 4 ms), and a fixed
2 ms simulated audio check after each candidate. At 50 iterations, completed
candidate/check loops took **37.79 → 23.20 ms**, first checked took
**4.47 → 4.69 ms**, and both paths dispatched eight requests. The roughly 0.22 ms
first-result startup cost is recorded rather than claimed as a first-track
speedup. Allocations rose from 219,479 to 310,904 bytes per operation. This loop
excludes parsing, reference confirmation, actual inference, and UI delivery.

Standalone compiled-test processes also measured CPU and maximum resident memory:

| Process workload | Serial CPU (user + system) | Concurrent CPU | Serial / concurrent peak RSS |
| --- | ---: | ---: | ---: |
| 100 cold four-reference retrievals | 0.35 + 0.02 s | 0.39 + 0.02 s | 48,160 / 50,032 KiB |
| 50 provider/audio fixture loops | 0.09 + 0.02 s | 0.11 + 0.02 s | 18,916 / 19,224 KiB |

These process figures include fixture construction, benchmark harness startup,
and calibration; RSS is not a per-generation allocation measurement. Concurrency
reduces elapsed latency by overlapping work and uses somewhat more CPU/memory.

Reproduce stage benchmarks:

```sh
go test ./internal/reco/multichannel -run '^$' \
  -bench '^BenchmarkRetrieval(Scheduling|Scans)$' -benchtime=500ms -count=2
go test ./internal/localcatalog -run '^$' \
  -bench '^BenchmarkInstalledPackQueries$' -benchtime=500ms -count=2
go test ./internal/enrich/musicbrainz -run '^$' \
  -bench '^BenchmarkDiscoveryPrefetch$' -benchtime=50x
```

For process resources, compile with `go test -c <package> -o /tmp/fixture.test`,
then run `/usr/bin/time -f 'user_s=%U system_s=%S elapsed_s=%e max_rss_kib=%M'`
on the binary with `-test.run '^$'`, `-test.bench` selecting the exact serial or
parallel sub-benchmark, and `-test.benchtime=100x` (retrieval) or `50x` (provider).
Run the variants separately to avoid competing measurements.

### Full backend generation

`BenchmarkBuildRecommendationPrefetch` exercises actual `BuildRecommendation`,
the MusicBrainz client against a loopback-only HTTP mirror, normal ranking and
selection, and fixed recorded audio evidence with a 2 ms read delay. The fixture
uses eight artist pages, requests four tracks, and retains a 2 ms provider
interval. It compares the complete playlist, scores, assessments, and saved
knowledge snapshot; only audio elapsed-time telemetry is excluded from equality.

In 30-iteration runs repeated twice:

| Provider state | Serial first checked | Prefetched first checked | Serial playlist | Prefetched playlist |
| --- | ---: | ---: | ---: | ---: |
| Back-to-back generations | 4.74–4.80 ms | 6.37–6.45 ms | 19.15–19.29 ms | 14.02–14.38 ms |
| Idle before generation | 4.81–4.84 ms | 5.13–5.16 ms | 19.22–19.31 ms | 13.04–13.05 ms |

The idle variant waits 3 ms outside both timers. The application-wide limiter
retains the last dispatch across client instances, including canceled speculative
requests. Immediate repeat submissions therefore inherit more provider spacing.
FIFO page scheduling prevents later pages overtaking the first page, but a roughly
0.3 ms startup overhead remains even with an idle limiter.

Requests increase from **4 to 5.7–5.87 per generation**: speculative requests count
even when early success leaves their pages unused. Idle-provider allocations were
approximately **405–410 KB versus 520–530 KB**. These measurements include the
backend generation lifecycle but exclude frontend submission, parsing, reference
confirmation, and real model inference. The request/first-track tradeoff is retained
alongside the approximately 32% completed-playlist improvement for idle providers.

```sh
go test ./internal/reco/multichannel -run '^TestPipelinePrefetchEquivalent$' \
  -bench '^BenchmarkBuildRecommendationPrefetch$' -benchtime=30x -count=2
```

For 30 idle-provider full-generation operations in separate compiled-test
processes, serial used 0.09 s user + 0.02 s system CPU and 22,488 KiB peak RSS;
prefetch used 0.08 s user + 0.04 s system CPU and 22,668 KiB peak RSS. Resource
measurements include loopback-server/harness setup, baseline runs, and the idle
waits; they are not measurements of the desktop process or real inference.

### Executed validation

- `./scripts/test.sh`: shell checks, binding generation, frontend typecheck,
  all 24 frontend test files (233 tests), production build, `go vet`, pure-Go
  core compilation, and the complete `go test -race -count=1 ./...` passed.
  Its final lint step found a trailing blank line in the new pipeline test;
  that formatting issue was fixed and `golangci-lint run ./...` then passed
  with zero issues. No gate tools were skipped.
- Focused race runs passed for similarity, shared workers, installed search,
  local catalogs, multichannel orchestration, semantic search, MusicBrainz, and
  retry handling. Independent review rechecked the provider-priority and
  terminal-budget-error fixes.
- `git diff --check` passed. The existing frontend spelling-confirmation edits
  were preserved.

Validation ran on Linux/amd64. Live providers, real models, production catalog
assets, native desktop timing, and Windows/macOS execution were not exercised.
