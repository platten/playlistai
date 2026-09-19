# Indexer durability and throughput repair — September 19, 2026

This repair addresses durable scan correctness, bounded memory use, late-run job
selection, inference-outage handling, progress ownership, and sustained indexing
throughput. It is based on `9e7ec2d`; unrelated local-library recommendation work
was kept outside this change.

## Changes and compatibility

- Successful directory enumeration precedes publication of children. The durable
  frontier supplies bounded claim batches; pending and expired leases use separate
  indexed ranges. Large directories use a private disk-backed sorted staging file,
  with bounded read buffers retained under admission through commit. Interrupted
  directory replay reconciles membership; final errors include earlier attempts.
- The scan inventory cache is capped, with indexed SQL fallback. SQLite page caches
  are bounded (32 MiB writer, 8 MiB per reader, at most eight readers); temporary
  sorts spill to disk. WAL auto-checkpointing remains enabled during scanning,
  and resumed-directory revalidation closes its read snapshot after each page.
  Both enumeration and spill replay enforce byte and record limits. Each 8 MiB
  directory reservation allows 4 MiB of conservatively charged retained records
  and 4 MiB for fixed/transient storage. The inventory drops its snapshot at its
  16 MiB byte budget or 4,096-row limit and uses indexed SQL. Synthetic long-path
  tests cover early spilling, sorted replay, and cache fallback below the row cap.
  Spill replay preserves original filename bytes, including non-UTF-8 Unix names,
  using the SQLite path key instead of JSON's lossy string representation.
- A disposable `eligible_jobs` projection separates epoch, stage, readiness, and
  state. Writer-side triggers update it atomically with durable jobs; startup
  rebuilds it. Candidate leases recheck eligibility and source/semantic fences.
- Failed MERT sessions remain quarantined until those exact sessions pass fixture
  validation. Dispatch rechecks health after waiting for a session and CPU. An
  outage defers work without charging track retries; completed DSP remains cached.
  Recovery validates all sessions, and transition notifications remain ordered.
- One cancellable progress poller publishes generation-tagged snapshots; rendering
  uses cached counts. Timing aggregation uses a file-ID index and retains sorted
  report output.
- `--stage-trace-events N` enables a bounded, path-free trace (maximum 100,000
  events). Reports retain legacy fields and add ONNX execution, native failures,
  restart/check durations, outage duration, and reservation peaks. `cudaExecution`
  remains compatible; ONNX execution includes host work and is not GPU occupancy.
- `bench concurrency --matrix gpu` measures decode workers `{4,8}`, DSP workers
  `{2,4}`, and CUDA sessions `{1,2}`, plus serial execution, with three trials per
  configuration. It uses isolated state, full integrity, fixed sampled inputs,
  semantic/coverage checks, and measured memory guards. It recommends a change
  only with repeated throughput gains of at least 10% and at most 5% p95 track
  latency regression. Two sessions must also beat the matched one-session case.

No model, embedding, pack-format, or authoritative state-schema version changes
are required. The workset is derived state. Existing commands and report fields
remain available. Automatic CUDA session defaults remain unchanged; measurements did not justify
additional sessions.

## Synthetic measurements

All synthetic tests used temporary stores on Linux amd64, Intel Core Ultra 9
285H, with 16 effective Go processors. They establish algorithmic behavior, not
musical quality or end-to-end indexing gains.

| Measurement | Result |
|---|---:|
| Appended-work selection, previous global query, same database | 3.838 ms median |
| Appended-work selection, epoch workset, same database | 0.151 ms median |
| Appended-work selection, 1,000 / 20,000 unrelated files | 0.139 / 0.145 ms median |
| Metadata-blocked selection, 1,000 / 20,000 files | 0.138 / 0.178 ms median |
| Mostly-completed selection, 1,000 / 20,000 files | 0.141 / 0.204 ms median |
| Late-run durable claim of 64 jobs | 10.649 ms median |
| Timing merge at 1,000 / 100,000 entries | 26.15 / 28.44 ns median |

Selection benchmarks used three 100-iteration trials and 64 eligible jobs. Timing
benchmarks used three 100 ms trials. The original linear timing lookup measurements
in the planning review are separate observations, not a same-run comparison.

## Verification record

The final integrated `./scripts/test.sh` passed after all review fixes: script
checks, Wails binding generation, frontend typechecking, 221 frontend tests,
production frontend build, Go vet, pure-Go core compilation, the full Go race
suite, and golangci-lint (zero issues). No gate tools were skipped. Focused
audio/indexer/CLI race tests also passed, including scan cancellation/resume,
partial-DSP fencing, queued dispatch during outages, stale progress snapshots,
long-path bounds, and exact filename-byte preservation. `git diff --check` passed.

Independent read-only review closed with no actionable findings. Verification
ran on Linux/WSL2; native Windows/macOS execution and desktop presentation traces
were not performed.

## Full-integrity GPU matrix

Executed on WSL2 Linux amd64, Intel Core Ultra 9 285H (16 effective processors),
RTX 5060 Laptop GPU (8,151 MiB), Go 1.27.1, FFmpeg 8.1.2/Chromaprint 1.6.1,
MERT-v1-95M revision `12af15fef9d0ac838c3f475bfbbf26d2060dd4f5`, ONNX Runtime
1.26.0 CUDA. The binary SHA-256 was
`07d94f41133e88c3d86fe7dea24f47747ca188d1a63e7b35b366e8951719f44e`.

The private fixed corpus contained 72 FLAC files, 432 balanced-profile windows.
Every point used full integrity, seed 42, eight heavy CPU slots, two source-I/O
slots, the HDD buffering profile, an 8 GiB admission target, and a 20,000-event
trace limit. Model setup and source pre-reading occurred outside timed analysis;
configuration order rotated across three trials. No CPU-heavy test suites ran
concurrently. The complete run lasted about 14 minutes, including isolated model
setup for all 27 trials. Raw results and timestamped telemetry were retained
privately and were not added to the repository.

| Decode / DSP / CUDA sessions | Median tracks/s | P95 track service time | All three exact serial comparisons |
|---|---:|---:|---|
| Serial | 1.716 | 2.066 s | Pass |
| 4 / 2 / 1 | **4.919** | 0.680 s | Pass |
| 4 / 2 / 2 | 4.636 | 0.706 s | Fail (trial 1) |
| 4 / 4 / 1 | 4.404 | 0.825 s | Pass |
| 4 / 4 / 2 | 4.446 | 0.710 s | Fail (trial 1) |
| 8 / 2 / 1 | 4.679 | 0.645 s | Pass |
| 8 / 2 / 2 | 4.514 | 0.650 s | Fail (trial 1) |
| 8 / 4 / 1 | 4.546 | 0.657 s | Pass |
| 8 / 4 / 2 | 4.359 | 0.771 s | Pass |

All 27 trials completed 72 metadata jobs, 72 audio jobs, and 432 windows, with
zero failed/skipped tracks, native failures, restarts, or outages. All one-session
results exactly matched serial digest
`61569bb4d550ed955fd2ca6c367991569c105932e713971f09346253c2fac64b`.
Three experimental two-session trials produced different exact digests and were
correctly disqualified; their cause was not established, and they are not
represented as equivalent. No two-session point demonstrated an improvement.
The matrix selected 4 decode / 2 DSP / 1 CUDA and did not change defaults.

Sampled launcher-plus-worker RSS ranged from 1,469 to 2,962 MiB and every trial
met the configured RAM target. GPU memory samples ranged from 262 to 2,925 MiB,
including setup and teardown. RSS sampling excludes device VRAM and short-lived
codec subprocesses; reservations and host telemetry complement these samples.
All 72 source SHA-256 hashes matched afterward, and each trial's mutable state
was created and removed in a temporary directory.

The selected point's three runs accumulated 5.92–7.72 seconds between worker
requests, versus 7.99–8.26 seconds serving requests; maximum ready depth was two.
Median idle gaps were 8.10–12.15 ms, and p95 gaps were 70.65–85.00 ms. Claim
latency medians were 0.35–0.41 ms. Full integrity and decoding shared two source
slots, and DSP remained a substantial stage. These are scheduler/host timings,
not GPU-occupancy measurements. They justify measuring the bounded preparation
queue experiment; they do not establish the cause of desktop stutter.

`nvidia-smi` was sampled every 500 ms and `vmstat` every second alongside the
trace. NVIDIA documents limited utilization/process reporting in WSL:
[NVIDIA WSL guide](https://docs.nvidia.com/cuda/wsl-user-guide/).
No native Windows presentation/ETW trace was collected, and no desktop-stutter
cause is attributed from these measurements.

## Bounded scan fallback measurements

Three sequential, uncontended trials of three iterations each compared the same
10,000-file synthetic database. The indexed unchanged-file guard reduced direct
observation median from 981.625 ms to 301.950 ms (69.2%). A temporary Go build
overlay bypassing only that guard compared complete flat-directory rescans:
median 1.027251 s to 0.391885 s (61.9%); the slowest of the three trials improved
from 1.038296 s to 0.554661 s (46.6%). A maximum of three observations is not a
statistical p95 estimate. Total allocations fell from 84.3 to 66.6 MB per rescan;
these are allocation totals, not peak residency. The guard was retained.

Commands:

```sh
go test ./internal/libraryindex -run '^$' -bench '^BenchmarkScanIndexedUnchanged$' -benchtime=3x -count=3
go test ./internal/libraryindex -run '^$' -bench '^BenchmarkScanFlatDirectory$' -benchtime=3x -count=3
go test -overlay=BASELINE_OVERLAY.json ./internal/libraryindex -run '^$' -bench '^BenchmarkScanFlatDirectory$/^10000$' -benchtime=3x -count=3
```

The complete 1,000-file rescan median was 30.339 ms. The additional directory
claim regression merged pending/expired work in canonical order and preserved
fences; semantic changes and unrelated jobs remained outside scoped blocked-job
finalization. Focused scan tests and their race suite passed after these fixes.


## Prepared-window experiment (rejected)

A temporary Go overlay compared the selected 4 decode / 2 DSP / 1 CUDA point
with a bounded FIFO of prepared windows. The prototype reserved three prepared
buffers per decode workflow, preserved canonical window order, and joined and
cleared all queued work on cancellation. Queue unit/race checks passed before
measurement. Both configurations used the same 72 files, full integrity, existing
model/runtime, and three rotating-order trials; source pre-reading remained
outside timed analysis. No CPU-heavy tests ran concurrently.

| Configuration | Trial tracks/s | Median tracks/s | P95 track service time |
|---|---|---:|---:|
| Synchronous baseline | 5.247, 5.020, 4.618 | 5.020 | 0.608 s |
| Prepared FIFO | 5.026, 4.557, 4.641 | 4.641 | 0.669 s |

The queue reduced median throughput by 7.56% and increased p95 service time by
10.16%. It failed the adoption thresholds and was removed. No prepared-queue flag
or dormant code is shipped. The retained pipeline releases the inference CPU
reservation before health validation while retaining the worker reservation.

All six trials completed 72 audio jobs and 432 windows with zero failures,
restarts, or outages, and met the RAM target (sampled owned RSS 1,501–1,522 MiB).
All three prepared results and two baseline results matched the original serial
digest. One synchronous baseline trial produced a different exact digest; its
cause remains unresolved. This also shows that the earlier digest variation is
not established to be specific to two CUDA sessions. Exact equivalence checks
remain strict; no tolerance or assertions were weakened. All 72 source hashes
matched again afterward. Timestamped GPU/host telemetry and raw comparison JSON
remain in the private measurement directory. Windows presentation diagnostics
remain unavailable.
