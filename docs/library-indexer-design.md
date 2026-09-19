# Playlist indexer implementation contract

This document records the implementation inspected and extended at commit
`d7f68c36a1e0d4a28d40b80aaad87c581470627c` on September 16, 2026. The work
was performed in the existing `github.com/platten/playlistai` Go module. The
command is `cmd/playlist-indexer`; its dependency graph does not import Wails,
GTK, or WebKit.

## Reused and separated code

The repository already had the real MERT-v1-95M ONNX graph contract, the native
ONNX Runtime worker, sinc64 mono/24 kHz preprocessing, padding masks, final-layer
masked pooling, normalized 768-dimensional output, bundle verification, and
parity fixtures. Those components are reused. The worker protocol now supports
multiple independently owned warm processes and an explicit intra-op thread
budget.

The old original-audio input was not a general library decoder. It accepted a
bounded MP3 preview (8 MiB and at most 60 seconds). It did not decode arbitrary
FLAC or AAC merely by changing a filename. The library analyzer therefore uses
`internal/localaudio` and a pinned private FFmpeg 8.1.2 payload. The old preview
contract remains unchanged.

Likewise, preview DSP rejected float samples outside nominal full scale and
rates above 96 kHz. Library analysis uses the separately identified local DSP
and MERT preprocessing contracts. They accept finite float32 decode output,
preserve values above nominal full scale, and support ordinary mono/stereo input
through 192 kHz. Original PCM branches before any MERT normalization. No source
file is rewritten, tagged, normalized, transcoded, or deleted.

## State and identities

SQLite stores roots, durable directory frontiers, scan epochs, file assets,
fenced jobs, frozen `scan_diff_jobs`, raw metadata, DSP, MERT vectors, and
immutable generation pointers.
WAL uses `synchronous=FULL`; the embedded modernc SQLite is 3.53.4, newer than
the upstream 3.51.3 WAL-reset correction. Only the dedicated writer connection
mutates state. File observations, job results, and failure transitions are
coalesced by count/time while retaining per-item source/fence checks. Workers
never retain SQL transactions during probe, decode, DSP, or inference.

Root identities derive from explicit logical aliases, not mount paths. File IDs
derive from root identity plus safe relative path and remain stable across
mount-point changes and isolated serial/parallel validation states. Device and
inode preserve identity across same-root moves when available. Source revisions
use stat data only as a change fence; they are not described as cryptographic
audio identity. Full hashes remain an explicit future verification option.

An epoch reconciles absence only after every durable directory task in that
root finishes successfully. Permission errors, missing mounts, interruption,
and incomplete frontiers preserve existing inventory and tombstones.
Completed directory tasks retain a stat revision. When an interrupted epoch is
resumed, only completed directories whose revision changed (or can no longer be
statted) are returned to the durable frontier; legacy v1 state is migrated and
rechecks completed directories once. A new invocation after a completed epoch
still enumerates the full configured scope. Per-file source revisions and
semantic job keys keep unchanged completed analysis out of the claim queue,
while new or changed files become pending.

Enumeration claims bounded batches (at most 256 directories) from SQLite's
indexed durable frontier. A single committer groups successful results; children
become eligible only after their parent enumeration succeeded and published
them. Failed directories retain partial-root inventory and never enqueue their
unpublished children. Walkers read 256 directory entries at a time and spill
large results to private temporary SQLite files with a 1 MiB page cache. Their
path index preserves whole-directory ordering, including hardlink identity.
Successful spills commit in bounded chunks; the parent remains leased until
the final chunk, so interruption safely re-enumerates it. Successful replay
first clears that directory's prior attempt epoch marks, so files deleted
between attempts cannot survive a complete scan; partial roots still retain
prior inventory. Final error totals come from durable scopes, including failures
before an interruption. Spill files are removed after commit or cancellation;
stale spill files under `STATE/scan-staging` are discarded on resume.

The optional inventory snapshot is capped at 4,096 files and bounded job rows.
Within that cap, matching paths, revisions, native identities, and settled jobs
need only a `last_seen_epoch` update. Larger inventories use a prepared indexed
read to prove the same unchanged identity and settled-job conditions before
touching the epoch mark; other files use full per-file observation. Neither path
retains a library-sized map. Scan buffers and configured SQLite page caches stay
reserved through commit. Source I/O descriptors release when enumeration ends;
spill replay descriptors remain reserved through commit. The optional inventory
cache is skipped when the remaining memory budget cannot fit it alongside one
directory buffer. SQLite's periodic automatic WAL checkpoints remain enabled
throughout enumeration. Earlier synthetic throughput measurements used an
unbounded scan implementation and do not establish performance of these bounds.

Every `run` completes enumeration before any file-analysis claim. It atomically
publishes a privacy-safe manifest generation under `STATE/manifests`. Both
streams contain only unique audio files with compatible pending work: the
inventory records logical root aliases, relative paths, sizes, and source
revisions, while each diff row nests the file's stage jobs. Directories,
non-audio entries, and unchanged files with settled work remain in their proper
durable catalog/frontier state but are not processing-manifest rows. Analysis is
restricted to that diff; writing a manifest replaces the previous epoch's diff
rows. Size, mtime, and native identity
are checked against the frozen revision before and after
probe/integrity/decode; native operations also fence Linux change time across
their own reads. A mismatch is persisted as
`source_changed_after_manifest` and is not reconsidered until a later scan
observes the new revision. Issues are durably appended to
the private `STATE/issues.jsonl`; structured source locations remain logical,
while native diagnostic detail may contain a physical path.

Job selection uses a disposable `eligible_jobs` projection keyed by epoch, kind,
readiness, state, and job ID. Metadata-blocked work occupies a separate index
range from claimable work. SQLite triggers maintain the projection within the
single writer's durable transactions; startup rebuilds it from authoritative
jobs and the current manifest. Lease commits recheck membership and prerequisite
eligibility under the writer transaction as well as source and attempt fences.
Selection and scoped counts avoid unrelated epochs and completed prefixes.

Graceful shutdown has a separate stop-admission signal. The first interrupt
prevents new frontier/job claims and later pipeline phases while keeping the
operation context alive for the bounded claimed set to finish and commit. Only
the configured timeout or a second signal escalates to cancellation. Reopening
the state resets any surviving fenced leases to pending and resumes the same
enumeration epoch or analysis jobs.

## Analysis and learning

Fast, balanced, and deep profiles request 3, 6, and 12 five-second windows.
Balanced centers are 10%, 26%, 42%, 58%, 74%, and 90%. Clamping and interval
subtraction ensure overlapping decoded time is not counted twice. The HDD
profile reserves a complete track's sampled PCM when it fits, decodes those
windows consecutively under one source lease, then releases the reservation per
processed window; an oversized track falls back to the single-window path used
by the other profiles. The decoder releases source I/O and descriptors before
DSP and MERT acquire distinct CPU reservations. DSP waits for its stage slot
before acquiring CPU. Waiting for a warm MERT session holds no CPU slot. Each
window remains immutable until both consumers finish. Backpressure is
controlled by aggregate CPU, source I/O, descriptor, RAM, and PCM byte
reservations.

A MERT worker failure is followed by a fixture health check on the restarted
worker. If the check passes, the failure is charged to the track through the
normal bounded retries. If it fails, the analyzer declares a MERT outage:
in-flight tracks keep their DSP result and return to the queue without a retry
charge, new audio tracks wait, and one recovery loop health-checks every session
with 1–30 s backoff until analysis can resume. A failed session stays quarantined
until that exact session validates. Dispatch rechecks health after both session
and CPU waits, releasing reservations when deferring. Transition notifications
are serialized so a late recovery cannot overwrite a newer outage. Idle workers are also probed
after 30 s without a successful embedding. Outages and recoveries are recorded
as `mert_unavailable`/`mert_recovered` issues and counted as `mert_outages` and
`mert_deferred` in the run summary. Metadata analysis continues during an
outage. A warm-up failure at startup remains fatal.

The frozen learning generation contains a distinct-artist/album TF-IDF genre
baseline, bounded sparse implicit SVD when the data has meaningful rank,
deterministic diverse MERT sampling, mini-batch spherical k-means, full-corpus
assignment, and an immutable exact cosine index. Missing embeddings never enter
the fit as zeros. Exact search splits fixed shards, retains bounded local top-K,
and merges by score then stable track ID. ANN is not included because no executed
2M-row measurement justified it; exact search remains the correctness backend.

The `.paipack` format is documented in [paipack-format.md](paipack-format.md).
Version 5 contains normalized sparse learning/statistics tables, a SQLite
metadata snapshot, and packed float32 vectors, not audio, PCM, absolute paths,
or giant JSON model/vector arrays. It also retains embedded ISRC, recording
MBID, AcoustID ID, and AcoustID fingerprint tags. Fingerprints are generated
only when no AcoustID identity tag is present and are not submitted to a remote
service. Desktop import builds checksum-verified
metadata/artist and exact-MERT derivative indexes before atomic activation.

## Distribution

`playlist-indexer` is a self-extracting single-file distribution, not a pure-Go
or guaranteed static ELF. `cmd/indexerpack` appends a deterministic ZIP payload
and authenticated trailer to the Go launcher. The standard artifact contains
the codec payload and performs an explicit separately licensed MERT setup/import.
The Linux amd64 offline artifact contains the codec plus CPU and CUDA MERT
payloads. Inner manifests
and hashes are checked again before private, versioned, locked atomic promotion.

Build both variants with:

```sh
PLAYLIST_INDEXER_CODEC_PAYLOAD=/absolute/codec-payload \
PLAYLIST_INDEXER_MERT_BUNDLE=/absolute/mert-linux-amd64 \
PLAYLIST_INDEXER_MERT_CUDA_BUNDLE=/absolute/mert-linux-amd64-cuda \
./scripts/build-playlist-indexer.sh
```

MERT remains CC-BY-NC-4.0 and always requires `--accept-model-license`, including
when bytes are embedded. `--yes` is deliberately absent. Network is used only by
authorized standard-mode asset setup; `--offline` rejects it.

Linux amd64 with glibc is the validated target. The codec evidence build needs
glibc 2.36 or newer; the pinned ONNX Runtime also needs its documented C/C++
runtime libraries. Alpine/musl and arm64 are not claimed. A `noexec` state mount
can be handled with an executable `--runtime-dir`; the application never asks to
remount a filesystem or weaken security.
