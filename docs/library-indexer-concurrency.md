# Playlist indexer concurrency, ownership, and recovery

## Effective plan

`--concurrency auto` is the default. `manual` applies explicit positive stage
ceilings, and `serial` admits one heavy unit, one source operation, one warm MERT
session, and one inference thread. Serial retains the same durable plumbing and
is the correctness baseline. Conflicting multi-worker serial overrides fail.

The global heavy budget is derived from `GOMAXPROCS`, Linux CPU affinity, and
cgroup quota. Fractional quotas are rounded down, with a minimum of one slot.
Auto reserves one slot for control when more than two are available. Stage
worker flags are ceilings sharing this budget, not independently multiplied
allocations. Unknown/HDD/NAS storage defaults to two source operations; SSD may
use more only within CPU, memory, and descriptor capacity.

Each admitted operation atomically reserves the complete tuple:

```text
(CPU slots, source-I/O slots, RAM bytes, file descriptors, PCM bytes)
```

Decode initially owns source I/O, descriptors, CPU, and bounded PCM bytes. Once
the decoder closes, it releases source resources and decode CPU while a
reservation lease retains immutable PCM ownership. DSP and MERT acquire
separate global CPU reservations; PCM is cleared only after both branches end.
MERT CPU cost
is `inference-workers × inference-threads`; auto permits a second warm session
only with at least four effective slots and session-memory headroom. The plan
reserves model residency, native input/output, Go heap, PCM, database, snapshot,
and index scratch. `--max-ram` is admission and monitoring policy, not an OS RSS
guarantee. Native allocations are outside `GOMEMLIMIT`.

Implemented flags are `--workers`, `--scan-workers`, `--metadata-workers`,
`--decode-workers`, `--dsp-workers`, `--io-workers`, `--io-profile`,
`--inference-workers`, `--inference-threads`, `--fit-workers`,
`--index-workers`, `--queue-depth`, `--max-ram`, `--max-open-files`, and
`--shutdown-timeout`. `--no-progress` disables the TTY-only PTerm progress bar;
the same live area contains a PTerm box showing the most recent file admitted by
scan or analysis. With multiple workers this is intentionally an activity
indicator, not a claim that only one file is active. Non-TTY stderr never
receives cursor-control output. Effective values are printed and included in
JSON.
`--config` accepts the bounded JSON shape in
[library-indexer-config.example.json](library-indexer-config.example.json).
Precedence is command-line flag, then configuration value, then automatic
default; unknown configuration fields fail closed.

`--root-alias ALIAS=PATH` is repeatable and is the stable-root interface for a
remountable library. The logical alias determines root/file identity; later
runs may change its physical path without reindexing unchanged files. Plain
`--root` remains compatible, but its generated aliases are unsuitable when
mount ordering can change.

## Pipeline and ownership

The bounded flow is:

```text
durable directory frontier -> inventory writer -> probe workers -> durable jobs
-> decode workers -> immutable PCM window -> local DSP + MERT preprocessing
-> warm one-request/session MERT pool -> ordered track result -> single writer
-> drain -> online SQLite backup + immutable vector generation
-> parallel fixed-block fit/assignment/index -> atomic pack publication
```

Discovery and analysis overlap across recordings. A directory is read in
256-entry chunks; each child batch is idempotently committed to the durable
frontier before the parent is completed. Pool-level heartbeats renew directory
and analysis leases. A full in-memory queue therefore cannot lose or deadlock
frontier work. Scanning shares the source-I/O/descriptor admission controller
with probing and decoding. Channels contain bounded
descriptors; decoded bytes require a separate byte reservation. A window is
cleared only after both branches release it. Native requests own their tensor
until the framed response or killed/reaped worker completes.

Directory completion also commits its mtime/size revision. Resuming an
interrupted epoch stats completed frontier entries in bounded batches and
requeues only changed or unavailable directories, incrementing the durable
scope barrier for each successful transition. Enumeration compares the
directory revision before and after reading; a concurrently changing directory
is retried up to the bounded attempt limit rather than committed as complete.
Regular rescans create a new epoch, while unchanged per-file semantic jobs
remain completed and are never claimed again.

Each MERT session is a long-lived child with exactly one active request,
independent mutable tensors, explicit ONNX Runtime intra-op threads, sequential
graph execution, and disabled idle spinning. The fixed batch-1 graph is never
given a larger batch. FFmpeg children use absolute argv paths, a private process
group, and Linux parent-death behavior. MERT workers likewise use their own
process group and Linux parent-death signal, in addition to framed pipes,
explicit close/kill/reap, deadlines, and durable fencing.

Segment results carry file/source revision, job fence, semantic contract,
segment index and observed interval. Assembly sorts canonical segment order,
never completion order. Seeds use stable IDs rather than worker numbers.

## Durable state machine and locks

Lock order is asset-install lock before state coordinator lock. A single OS-backed
exclusive coordinator lock protects each canonical state directory; status uses
bounded read-only connections. The lock file is diagnostic only and is never
unlinked to “break” a live kernel lock.

Jobs move through:

```text
pending -> leased(attempt++, random fence, deadline)
       -> completed(result + state in one transaction)
       -> failed/unsupported
expired leased -> leased(new attempt and fence)
recoverable failure -> pending(retry count bounded)
```

A commit checks the current fence, expected source revision, and unique semantic
result key in the same transaction. A superseded worker cannot overwrite a newer
attempt. The guarantee is at-least-once computation with idempotent durable
commit, not exactly-once physical execution.

File observations, result commits, partial-DSP commits, and failure transitions
use a count/time-bounded writer batch (64 operations or 5 ms). Every item keeps
its fence and source-revision predicate inside the shared transaction. If one
item is stale, the batch rolls back and retries items individually, so it cannot
discard unrelated valid commits.

SIGINT/SIGTERM cancels the shared context, stops discovery and new claims,
kills/reaps cancelable owned child groups, and leaves uncommitted work durable
for lease recovery. The resolved `--shutdown-timeout` bounds process teardown;
a second signal forces immediate exit. The current implementation cancels
admitted work on the first signal rather than attempting a finish-first drain;
already committed writer transactions remain durable.

## Frozen generations and readers

After analysis drains, SQLite's online backup API creates a consistent immutable
corpus file without copying only the main file of a live WAL database. Learning
uses canonical IDs, fixed logical blocks, worker-local accumulators, and ordered
merges. Spherical centroids update only at a completed batch boundary. Index
shards are built under unique generation directories and activated only after
hash validation.

Pack and desktop managers publish one short atomic active pointer. Readers pin a
complete generation; replacement/removal retires old files only after all pinned
readers and mmaps release them. Completion order cannot affect exact top-K or
metadata/MERT channel merge ordering.

## Benchmark interface

`playlist-indexer bench concurrency` selects a deterministic bounded sample by a
keyed hash over stable relative paths, creates isolated scratch state, and runs
serial, 2-worker, 4-worker, and auto configurations with explicit native thread
budgets. It reports and compares a semantic digest over identities, metadata,
DSP, and vectors while excluding leases, timestamps, DB layout, logs, and
execution settings. Configurations that exceed the measured budget are skipped
with a reason. Cold setup/warmup and real-audio analysis are timed separately.
Results include analysis tracks/second and peak aggregate owned RSS on Linux
(launcher RSS plus resident MERT workers). Other platforms report the Go
runtime committed-memory fallback and do not label it OS RSS. Each comparable
run also fits and exports its frozen input, records pack bytes and bytes/track,
measures 25 exact queries against one pinned index (p50/p95), and times a clean
state reopen as the resume gate. `ramTargetMet` compares measured peak owned RSS
with the configured admission target; it does not turn that target into an OS
hard limit. `serialEquivalent` remains the semantic gate for the 2-worker,
4-worker, and automatic plans.

`playlist-indexer bench scale --rows 2000000 --dimension 768 --max-ram 8GiB`
is the explicit synthetic scale gate. It streams canonical rows instead of
materializing the corpus, builds the durable exact shards, runs repeated pinned
queries, exports a v3 pack, and reports peak RSS, index size/build time, pack
size/export time, bytes per track, and p50/p95 query latency. Its vectors are
declared deterministic four-sparse synthetic data; this exercises storage and
query scale but is neither decoded audio nor musical-quality evidence. Use a
dedicated scratch filesystem with at least twice the reported uncompressed
vector payload plus pack/index overhead.

No throughput or speedup is claimed unless it appears in the executed validation
report. Synthetic queue/index tests are not audio throughput or musical-quality
evidence.
