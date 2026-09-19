# Playlist indexer concurrency, ownership, and recovery

## Effective plan

`--concurrency auto` is the default. `manual` applies explicit positive stage
ceilings, and `serial` admits one heavy unit, one source operation, one warm MERT
session, and one inference thread. Serial retains the same durable plumbing and
is the correctness baseline. Conflicting multi-worker serial overrides fail.

The logical CPU ceiling is derived from `GOMAXPROCS`, Linux CPU affinity, and
cgroup quota. Linux sysfs `(physical_package_id, core_id)` pairs identify the
physical cores inside the allowed affinity set; quota and `GOMAXPROCS` also cap
that compute count. Fractional quotas are rounded down, with a minimum of one
slot. Auto reserves one physical compute slot for control, storage, and SQLite
when at least four are available. An explicit `--workers` value can use the
larger logical ceiling deliberately. Stage
worker flags are ceilings sharing this budget, not independently multiplied
allocations. HDD defaults to one scan, metadata, and source-I/O operation, up to
eight track/decode workflows, and at most two DSP operations. Unknown/NAS
storage defaults to two source operations; SSD may use more only within CPU,
memory, and descriptor capacity. Diagnostics state whether the profile was
explicit or the portable automatic default.

Each admitted operation atomically reserves the complete tuple:

```text
(CPU slots, source-I/O slots, RAM bytes, file descriptors, PCM bytes)
```

HDD workflows reserve all sampled PCM for one track up front when the complete
reservation fits `min(4 GiB, maxRAM/8)`, decode every requested window
consecutively while holding one source-I/O lease, then release decode resources
before processing. The PCM reservation is released per completed window. When
the complete reservation cannot fit, that track uses the single-window path.
Other profiles use the single-window path directly. DSP and MERT preprocessing
acquire separate global CPU reservations. A DSP waiter obtains its DSP-stage
slot before CPU admission, so stage backpressure cannot retain compute
capacity; PCM is cleared after both branches end. A
workflow waits for a warm MERT session without holding CPU admission, then
reserves the native inference budget only while that session executes. CPU MERT
cost is `inference-workers × inference-threads`; auto permits a second warm session
only with at least four effective slots and session-memory headroom. The plan
reserves model residency, native input/output, Go heap, PCM, database, snapshot,
and index scratch. `--max-ram` is admission and monitoring policy, not an OS RSS
guarantee. Native allocations are outside `GOMEMLIMIT`.

Admission is cancellable and grants only the oldest request that fits without
consuming a resource currently blocking an older waiter. This avoids wake-all
contention and prevents younger source/PCM requests from starving an older
track reservation while still permitting CPU-only continuation work to release
PCM already in use.

Implemented flags are `--workers`, `--scan-workers`, `--metadata-workers`,
`--decode-workers`, `--dsp-workers`, `--io-workers`, `--io-profile`,
`--inference-workers`, `--inference-threads`, `--fit-workers`,
`--index-workers`, `--queue-depth`, `--max-ram`, `--max-open-files`, and
`--shutdown-timeout`. `--no-progress` disables the TTY-only PTerm progress bar;
the same live area contains a PTerm box showing directory enumeration until an
audio file is found, then the most recent file admitted by scan or analysis.
Activity rendering does not wait on the SQLite progress snapshot, so a briefly
busy read connection cannot leave the box stuck at its initial message. A
file or directory activity event redraws only that transient box; it never
issues a durable count query. One cancellable background poller owns durable snapshots; initial, phase, stop,
and interruption renders immediately use cached counts. Epoch/freeze and phase
generations reject stale responses. Polls run at most once per second and wait
at least four times the previous query duration to bound database load; the
active-time heartbeat still refreshes once per second. Counts
render on a separate summary line, leaving stable width for the PTerm count/percentage bar;
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
-> decode workflows -> bounded immutable PCM windows -> local DSP + MERT preprocessing
-> warm one-request/session MERT pool -> ordered track result -> single writer
-> drain -> online SQLite backup + immutable vector generation
-> parallel fixed-block fit/assignment/index -> atomic pack publication
```

Each `run` completes discovery before analysis. The barrier publishes an
`inventory.jsonl` containing one row for each unique audio file with compatible
pending work plus a `diff.jsonl` with the same file rows and nested stage jobs.
Directories, non-audio entries, and already-settled files are excluded from both
manifest streams. Scan progress reports every supported file observed in the
current epoch and separately reports the pending-work subset. The stage jobs are
frozen in `scan_diff_jobs`; analysis can claim only that epoch's diff. A directory is read
in 256-entry chunks and fully enumerated before its children are published.
Oversized directories use private disk-backed staging; successful results replay
in bounded, lexically ordered batches to the durable frontier before the parent
is completed. Failed enumeration never exposes unpublished children.
Pool-level heartbeats renew directory
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
fallback under the default `--integrity full` policy. `--integrity deferred` is
an explicit throughput tradeoff for trusted tagged libraries: it records
integrity as deferred and relies on later sampled decoding rather than claiming
a complete-file validation. Neither path consumes the sampled PCM queue budget. The subprocess has a 30-minute bound for
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
given a larger batch. The matched parent and worker exchange bounded request
headers and little-endian float32 PCM directly; responses retain the existing
bounded framed encoding. A request that produces no framed response for 30 seconds
is treated as a transient native failure: the child is killed and reaped before
its slot returns, and the durable analysis job receives at most two automatic
retries, each able to launch a fresh process. Caller cancellation remains a
separate outcome. Initial session warmup also makes at most two restart attempts
for errors explicitly classified as native-worker failures; model/configuration
errors fail immediately. CUDA health startup has a separate two-minute bound for
provider loading and first-use kernel initialization; normal inference retains
the 30-second watchdog. FFmpeg children use absolute argv paths, a private process
group, and Linux parent-death behavior. MERT workers likewise use their own
process group and Linux parent-death signal, in addition to framed pipes,
explicit close/kill/reap, deadlines, and durable fencing.

CPU bundles use ONNX Runtime's CPU execution provider. A CUDA bundle has a
distinct runtime identity, carries the verified ONNX Runtime CUDA provider and
app-local CUDA/cuDNN dependency closure, and is accepted only on Linux/Windows
amd64. The dual Linux offline payload selects CUDA first on a detected NVIDIA
host and visibly falls back to its embedded CPU bundle if native health fails;
`--device cuda[:INDEX]` requires CUDA and is fail-closed. The
worker appends CUDA before constructing the session, allowing unsupported graph
nodes to use ONNX Runtime's CPU fallback, and runs the same bundled numerical
health fixtures before any library job is admitted. Provider initialization or
parity failure is fatal for explicit CUDA. CPU health retains a `1e-4` maximum
component error; CUDA permits `0.003` with the same `0.9999` minimum cosine to
account for provider reduction order while keeping the spaces version-separated.

Compatible MERT vectors may be reused across current files with the same valid
recording MBID, ISRC, AcoustID ID, or exact fingerprint plus normalized
artist/title identity. The audio semantic contract must match exactly, and an
exact-contract key/vector map is bulk-loaded from durable state before workers
start. A mutex-protected lookup and in-process single-flight prevent concurrent
duplicates from running inference twice without a per-file SQLite query. A new
result enters the map only after its fenced disk commit succeeds. DSP is always measured from each file because loudness and other
mastering-sensitive values are not recording-invariant.

DSP uses a separate stage contract and cache. A change to the MERT graph or
execution provider can therefore reuse compatible DSP from the same unchanged
source, while the combined audio result remains fenced by the new job contract.
If both same-file DSP and recording-level MERT are reusable, no sampled decode
is needed for that compatible reanalysis.

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

Successful directory enumeration additionally submits each bounded replay chunk
as one file-observation request. Its file upserts, move-identity checks, semantic-job
supersession, and job upserts share prepared statements and one transaction.
This preserves atomic per-chunk discovery while avoiding a commit round trip for
every track in a large directory.

Schema v5 stores recording identity beside metadata for indexed MERT reuse and
adds a separate DSP stage cache. Schema v4's immutable epoch work remains in `scan_diff_jobs` and its
directory-symlink traversal policy on the scan epoch, preventing a resumed
frontier from mixing enabled and disabled traversal.
Manifest format v2 groups that work into one diff row per audio file and records
the stage-job count separately from the file count. Inventory and diff JSONL are
emitted together from one ordered database cursor, avoiding a second full sort
and traversal as the manifest grows. Manifest generations are
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
serial, one-session/one-thread, one-session/two-thread,
two-session/one-thread, and auto configurations. It reports and compares a semantic digest over identities, metadata,
DSP, and vectors while excluding leases, timestamps, DB layout, logs, and
execution settings. Configurations that exceed the measured budget are skipped
with a reason. Cold setup/warmup and real-audio analysis are timed separately.
`--matrix pipeline` expands heavy workers `8/10/11`, decode workers `4/6/8/10`,
DSP workers `1/2/3`, source-I/O workers `1/2`, and track buffering on/off while
holding MERT at one session and one thread. It runs at least three trials and
selects the lowest-resource semantically equivalent, complete CUDA
configuration whose median wall time is within 3% of the fastest and whose p95
is within 5% of the fastest configuration's p95. Each point explicitly
pre-reads the corpus immediately before timed analysis, configuration order is
rotated and reversed between trials, and pipeline-matrix runs omit unrelated
fit, export, and query work.

Results include analysis tracks/second and peak aggregate owned RSS on Linux
(launcher RSS plus resident MERT workers). Other platforms report the Go
runtime committed-memory fallback and do not label it OS RSS. Each comparable
non-matrix run also fits and exports its frozen input, records pack bytes and bytes/track,
measures 25 exact queries against one pinned index (p50/p95), and times a clean
state reopen as the resume gate. `ramTargetMet` compares measured peak owned RSS
with the configured admission target; it does not turn that target into an OS
hard limit. `serialEquivalent` remains the semantic gate for every parallel and
automatic plan. The analysis report's stage durations are accumulated worker
time, not exclusive wall-clock phase durations. Aggregate and per-track JSON
separates CPU/source/PCM admission, aggregate source reads, probe, fingerprint,
full integrity, decode, DSP-slot wait, DSP, downmix, resampling, MERT-session
wait, worker preprocessing, bounded binary request IPC, CUDA execution, total inference, and
durable commit time. Admission queue/blocker counters,
physical/logical topology, filesystem type, affinity, GPU identity, power
profile, and before/after/delta ZFS ARC counters are diagnostic evidence;
unavailable host counters remain zero or omitted rather than inferred.

`--matrix gpu` is a smaller fixed matrix: decode workers `{4,8}`, DSP
workers `{2,4}`, and CUDA sessions `{1,2}`, plus a serial correctness baseline.
It requires exactly three trials per point and CUDA (`--device auto` selects
explicit CUDA for this mode). All 27 analyses use the same deterministically
sampled source paths, full integrity, isolated scratch state, one inference
thread, and the existing CPU/RAM/I/O/descriptor limits. Unsupported resource
points are reported as skipped. Each trial pre-reads sources and order rotates
and reverses. For example:

```sh
playlist-indexer bench concurrency --matrix gpu --root /authorized/sample \
  --sample-tracks 32 --device cuda --offline --accept-model-license \
  --model-bundle /verified/bundle \
  --stage-trace-events 20000 --max-ram 8GiB > gpu-matrix.json
```

The report retains all trial reports, serial semantic equivalence, coverage,
measured owned memory, median throughput, wall p95, and per-track p95 service
latency. Selection retains the smallest one-session matrix point unless another
point improves median throughput by at least 10%, also improves each paired
trial by 10%, and increases track p95 by no more than 5%. Two sessions must pass
these guards against their matching decode/DSP one-session point too. Missing
serial evidence, incomplete coverage, failures, changed sources, mismatched
session counts, or exceeded memory targets disqualify a point. Selection is a
report recommendation and never changes configuration defaults or real library
state. A service-time p95 is not an end-to-end queueing latency measurement.

`--stage-trace-events N` opts into a bounded stage trace, capped at 100,000
events; zero (the default) disables it. Durations in JSON are nanoseconds.
Events cover claims, decode, DSP/preprocessing, resource waits, ready windows,
ONNX calls, and gaps between requests on each worker. Idle gaps include host
processing and recovery; they are not GPU-idle measurements. Native worker
counters include the lifetime of the pool, including warm fixture checks. Trace events and dropped-event counts
are additive report fields. Capture a sustained fixed-input trace together
with host CPU, storage, and GPU telemetry before attributing desktop stutter to
any stage. Owned process RSS excludes device VRAM; sample GPU memory separately.
ONNX execution duration includes host work in the runtime call and does not
measure GPU occupancy. WSL GPU utilization/process counters and unavailable
Windows presentation diagnostics must be identified as limitations in results.

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
