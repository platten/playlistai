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
the same live area contains a PTerm box showing directory enumeration until an
audio file is found, then the most recent file admitted by scan or analysis.
Activity rendering does not wait on the SQLite progress snapshot, so a briefly
busy read connection cannot leave the box stuck at its initial message. A
once-per-second active-time heartbeat makes a long decode or inference request
visibly live even when durable counts have not changed. Counts render on a
separate summary line, leaving stable width for the PTerm count/percentage bar;
the bar is regenerated every 30 seconds even when the snapshot is unchanged.
A growing job total can reduce the displayed percentage, but cannot remove the
bar or retain an obsolete denominator. FLAC activity over 500,000,000 bytes is
styled red when color is enabled. `NO_COLOR` is honored. With multiple workers
this is intentionally an activity indicator, not a claim that only one file is
active. Non-TTY stderr never receives cursor-control output. Effective values
are printed and included in JSON.
`--config` accepts the bounded JSON shape in
[library-indexer-config.example.json](library-indexer-config.example.json).
Precedence is command-line flag, then configuration value, then automatic
default; unknown configuration fields fail closed.

`--root-alias ALIAS=PATH` is repeatable and is the stable-root interface for a
remountable library. The logical alias determines root/file identity; later
runs may change its physical path without reindexing unchanged files. Plain
`--root` remains compatible, but its generated aliases are unsuitable when
mount ordering can change.

`--append-root ALIAS=PATH` is the explicit incremental-source interface. A run
using it enumerates only the named additional roots while retaining inventory
and compatible completed jobs for roots already present in the state. It is
repeatable and may be rerun with the same alias to discover additions. Root
aliases remain the identity boundary, so independently mounted folders need
distinct aliases; a deliberate remount/path update continues to use
`--root-alias`.

Directory claims are scoped to one enumeration epoch. If an interrupted epoch
has a different root set from the next invocation, its remaining frontier is
marked interrupted/abandoned before a fresh epoch begins; stale tasks can never
be claimed by the new root set. This allows an append-only run for an unrelated
folder to preserve earlier inventory without waiting on a directory belonging
to a prior interrupted scan.

## Pipeline and ownership

The bounded flow is:

```text
durable directory frontier -> complete inventory -> immutable manifest + pending-job diff -> barrier
-> probe + FLAC/MP3 integrity workers -> durable jobs
-> decode workers -> immutable PCM window -> local DSP + MERT preprocessing
-> warm one-request/session MERT pool -> ordered track result -> single writer
-> drain -> online SQLite backup + immutable vector generation
-> parallel fixed-block fit/assignment/index -> atomic pack publication
```

Each `run` completes discovery before analysis. The barrier publishes an
`inventory.jsonl` containing one row for each unique audio file with compatible
pending work plus a `diff.jsonl` with the same file rows and nested stage jobs.
Directories, non-audio entries, and already-settled files are excluded from both
manifest streams and from progress totals. The stage jobs are frozen in
`scan_diff_jobs`; analysis can claim only that epoch's diff. A directory is read
in 256-entry chunks; each child batch is idempotently committed to the durable
frontier before the parent is completed. Pool-level heartbeats renew directory
and analysis leases. A full in-memory queue therefore cannot lose or deadlock
frontier work. Scanning and later analysis both obey the source-I/O/descriptor
admission controller. Channels contain bounded
descriptors; decoded bytes require a separate byte reservation. A window is
cleared only after both branches release it. Native requests own their tensor
until the framed response or killed/reaped worker completes.

For supported audio streams without an embedded AcoustID ID or fingerprint,
the metadata stage reserves one CPU/source-I/O slot and runs a full
pinned-FFmpeg decode that produces a bounded, base64-compressed
AcoustID/Chromaprint fingerprint before it completes the prerequisite job. The
fingerprint is retained locally and never submitted. Tagged fingerprints are
copied without regeneration, and no missing MBID is looked up. For FLAC and
MP3, a generated fingerprint also satisfies the full-decode integrity contract;
tagged identities or failed generation use the decode-to-discard integrity
fallback. Neither path consumes the sampled PCM queue budget. The subprocess has a 30-minute bound for
exceptionally large sources. Its source revision is checked before and after;
size, mtime, native identity, and Linux change time must remain stable. A media
decode error is permanent `corrupt_media`, while a changed source is refreshed
and requeued. During a manifest-bound `run`, a changed source is instead skipped
as `source_changed_after_manifest` and can be queued only by a later scan. The
integrity child has a 30-second decoded-output activity
watchdog in addition to its 30-minute total bound; a stall is terminated and
retried with a fresh child. AAC retains the existing probe plus selected-window
decode checks; full AAC integrity validation is not claimed by this contract.

Directory completion also commits its mtime/size revision. Resuming an
interrupted epoch stats completed frontier entries in bounded batches and
requeues only changed or unavailable directories, incrementing the durable
scope barrier for each successful transition. Enumeration compares the
directory revision before and after reading; a concurrently changing directory
is retried through a fenced durable transition up to eight attempts rather than
committed as complete. Counts from abandoned attempts do not inflate the scan
report. Regular rescans create a new epoch, while unchanged per-file semantic
jobs remain completed and are never claimed again.

Each MERT session is a long-lived child with exactly one active request,
independent mutable tensors, explicit ONNX Runtime intra-op threads, sequential
graph execution, and disabled idle spinning. The fixed batch-1 graph is never
given a larger batch. A request that produces no framed response for 30 seconds
is treated as a transient native failure: the child is killed and reaped before
its slot returns, and the durable analysis job receives at most two automatic
retries, each able to launch a fresh process. Caller cancellation remains a
separate outcome. Initial session warmup also makes at most two restart attempts
for errors explicitly classified as native-worker failures; model/configuration
errors fail immediately. FFmpeg children use absolute argv paths, a private process
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

Schema v4 stores each immutable epoch's stage work in `scan_diff_jobs` and its
directory-symlink traversal policy on the scan epoch, preventing a resumed
frontier from mixing enabled and disabled traversal.
Manifest format v2 groups that work into one diff row per audio file and records
the stage-job count separately from the file count. Manifest generations are
atomically renamed under `STATE/manifests`, and include hashes for their
inventory and diff JSONL streams. Operational problems are appended
to the private `STATE/issues.jsonl`; structured locations contain aliases and
relative paths. Native diagnostic detail can contain physical paths, so this is
an administrator log rather than a shareable report.

The first SIGINT/SIGTERM closes a separate admission signal rather than
canceling the work context. Directory and job dispatchers stop taking new claim
batches, close their bounded queues after all already-claimed descriptors have
been handed to workers, and wait for those operations to commit through the
single writer. No later fit/export phase begins. SQLite, issue logs, admission
state, and native sessions then close normally; pending frontier/jobs remain
resumable. The resolved `--shutdown-timeout` bounds this drain (30 seconds by
default), after which the coordinator cancels remaining owned subprocesses and
waits briefly for cleanup. A second signal forces immediate exit. Cancellation
does not promise instant interruption of blocked kernel I/O; fencing prevents a
late result from being committed as current.

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
