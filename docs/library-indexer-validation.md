# Playlist indexer executed validation — through September 18, 2026

This report distinguishes commands actually executed in the implementation
workspace from remaining release gates.

## Analysis scheduler and reuse check

An isolated real-audio check used two 20-second, 44.1 kHz stereo
FLAC files containing synthetic random PCM, the fast three-window profile, and
the verified CPU MERT bundle. The host was WSL2 Linux x86-64 on an Intel Core
Ultra 9 285H with 16 effective cores. Every pre-change and updated concurrency
run produced semantic digest
`e682c9f156cbba155efe37f2141a3614b041bb4485bbccf2342d8d7ab4164d39`.

The pre-change binary measured 7.210 s serial, 3.596 s with two one-thread
sessions, and 1.795 s/1.114 tracks per second under auto. The updated scheduler
measured 7.379 s serial, 6.155 s for one session/one thread, 3.548 s for one
session/two threads, 3.823 s for two sessions/one thread, and 1.960 s/1.020
tracks per second under auto. This two-track run establishes parity and exposes
the configuration tradeoff; it does not establish a general scheduler speedup.
The updated report attributed 2.171 aggregate worker-seconds to six auto MERT
inferences and recorded negligible session wait with two sessions.

A separate duplicate fixture copied one source twice and assigned the same valid
recording MBID and AcoustID ID. With `--integrity deferred`, both metadata, DSP,
and audio jobs completed; exactly one MERT result was reused. Only three windows
entered MERT, with 0.952 aggregate inference seconds, while both files retained
independent DSP rows and `deferred` integrity status. Their stored vectors were
byte-identical. This is a synthetic correctness/avoided-work check, not musical
quality evidence.

The host exposes an NVIDIA GeForce RTX 5060 Laptop GPU (8,151 MiB), driver
616.56. Microsoft ONNX Runtime 1.26.0's official Linux x64 GPU archive was
verified at SHA-256 `cb7df7ee2ca0f962c7ce7c839aeae36223d146a91fb4646d62fb0046f297479f`.
The test CUDA bundle included its provider plus the CUDA 12/cuBLAS/cuFFT/cuRAND,
cuDNN 9 and NVJitLink dependency set declared by the ONNX Runtime 1.26.0 Python
extras. Every native health fixture passed on the GPU under the CUDA-specific
`0.003` maximum-component / `0.9999` minimum-cosine gate; CPU keeps its `1e-4`
component gate.

A fresh run of the final dual-payload offline binary used `--offline --device
auto`, selected CUDA, completed three duplicate files with two MERT reuses, and
spent 73.366 aggregate milliseconds on the three inferred windows. The matching
explicit-CPU offline run spent 923.258 ms on those windows (about 12.6x more
inference worker time on this synthetic fixture). CUDA session warmup took 2.791
s; its 37.67 s first-run wall time was dominated by extracting and verifying the
3.4 GiB app-local CUDA closure. CPU first-run wall time was 9.87 s with its much
smaller payload. In a prior incremental run, the analyzer bulk-loaded one
persisted recording entry, reused it for a newly added duplicate, and recorded
zero MERT preprocessing/wait/inference time. This validates native CUDA
execution, both embedded backends, and the persisted-to-memory dedup path; it is
not held-out musical-quality evidence.

## Executed native evidence

- `go test -run '^$' -bench '^BenchmarkObserveFiles$' -benchtime=1s -count=1
  ./internal/libraryindex` on Linux/amd64 with an Intel Core Ultra 9 285H measured
  7,015,028 ns/file for sequential one-file state commits and 124,591 ns/file
  for 256-file directory-chunk commits (about 56x faster). This isolates SQLite
  observation/job ingestion with synthetic file descriptors; it is not an
  end-to-end filesystem or audio-analysis benchmark.

- `./scripts/test-indexer-codecs.sh` passed under `go test -race` using actual
  pinned FFmpeg processes. It covered FLAC at 44.1/48/96/192 kHz, CBR/VBR MP3,
  raw AAC-LC, M4A/AAC-LC, anti-phase stereo, over-full-scale float PCM, corrupt
  input, unusual paths, mutation fences, cancellation, unchanged hashes, and a
  real base64-compressed algorithm-1 AcoustID/Chromaprint fingerprint from the
  statically linked Chromaprint 1.6.1 runtime.
- `go run ./cmd/mertparity <validated-linux-amd64-pack>` passed against the real
  MERT graph. Observed cold inference was 5.471 s, warm inference 1.602 s, reload
  5.869 s, and worker cancellation passed on this host. These are observations,
  not general performance promises.
- `go test -race ./internal/librarylearn ./internal/librarysearch` passed, as did
  their repeated deterministic tests, vet, and lint. Serial/2/4 worker fixture
  tests cover fixed-block SVD, spherical fitting/assignment, exact top-K ties,
  cancellation, corruption, and pinned index replacement.
- `go test -race ./internal/librarypack ./internal/localcatalog` passed. Tests use
  real pack write/stage/activate/read paths and cover checksum/limit failures,
  metadata-only and MERT retrieval, incompatible spaces, channel completion
  order, cancellation, path escape, replacement/removal, pinned readers, and
  ISRC/recording-MBID/AcoustID-ID duplicate detection, tagged fingerprint
  round trips, and fingerprint duplicate detection with metadata
  corroboration.
- `GOOS=windows GOARCH=amd64 go test -run '^$' ./...` passed for every package,
  and the available Windows runner executed the changed `librarypack`,
  `librarysearch`, `localcatalog`, and indexer CLI suites successfully. A broader
  app-suite execution on that runner remains blocked by its pre-existing
  `LockFileEx` `Incorrect function` behavior in catalog installation tests; it
  is not reported as a native-Windows desktop pass.
- The rebuilt standard executable was run through a real pseudo-terminal. Its
  PTerm bar reported discovered files and durable queued work on stderr. After
  the resume/rescan update, another real TTY run displayed the PTerm
  `Currently processing` box with `Artist/New Song.flac`. The latest source
  launcher was also run against a real FLAC: before discovery the box advanced
  through `Scanning directory inventory…`, `primary`, and `primary/Artist`,
  then displayed `Artist/one.flac`. The separately redirected `--json` stdout
  parsed successfully, and a TTY run with `--no-progress` emitted no
  cursor/progress output.
- Before the file-oriented manifest update, a real metadata-only run processed
  one MP3, then the same state was rerun after adding one FLAC. It reported the
  two discovered audio files but exactly one metadata completion; final state
  contained two completed jobs. The current manifest-v2 validation below now
  reports only the one file that will actually be processed. Focused race tests
  also exercised selective changed-directory resume, v1-to-v3 state migration,
  and preservation of the completed job's attempt count.
- A separate real metadata-only run completed one FLAC under logical root
  `primary`, then reused the same state with
  `--append-root archive=<second-path>` and one M4A/AAC file. The append run
  reported exactly one discovery and one metadata completion; final status was
  two present files and two completed metadata jobs. Race tests additionally
  verify that the original job attempt remains unchanged, a repeated append is
  idempotent, and an append alias cannot silently remount an existing root.
- The rebuilt standard executable was run with two unrelated folders supplied
  together as `--append-root primary=... --append-root archive=...`. It
  discovered three files and committed three metadata records into one state.
  The same executable was then exercised across three invocations: the first
  root completed one record, appending the second root completed only its two
  records, and an unchanged third scan completed zero metadata jobs. Final
  status retained all three files and three completed metadata jobs.
- The scan-first executable was run against two roots containing three files.
  It published a three-row inventory and three-row diff with verified SHA-256
  fields before processing began, then completed all three metadata jobs. A
  missing-root run exited with the documented partial outcome and appended its
  directory error to `STATE/issues.jsonl`.
- The rebuilt manifest-v2 executable was run against one real FLAC beside an
  empty directory and a non-audio text file. It emitted one inventory row, one
  diff row, and one processing-file count; neither unrelated entry appeared in
  either JSONL stream. An unchanged rerun emitted zero rows and zero processing
  files. After adding one real M4A/AAC source, the next run again emitted exactly
  one row/count and completed exactly one metadata result. Focused race tests
  additionally verify that an audio-mode file with two stage jobs remains one
  manifest/progress unit, and that the activity callback omits a compatible
  completed file. A real pseudo-terminal run showed a stable `1 audio files`
  total while that FLAC moved from queued to active to finished; the directory
  and text file did not increase the bar total.
- The rebuilt standard executable was interrupted with SIGINT during real
  bundled-FFmpeg analysis of 250 FLAC files in serial mode. It stopped new
  admission, drained and committed its bounded 32-file admitted set, exited
  130, and reported 32 completed plus 218 pending with zero active/leased or
  failed files. Reopening the same state queued exactly those 218 files,
  completed all of them, and ended with 250 metadata records and no pending,
  active, or failed work. Repeated race tests also interrupt a claimed directory
  task, verify one completed plus one pending frontier row with no lease, close
  and reopen SQLite, and resume the same enumeration epoch. The resumed real
  state returned `ok` from `PRAGMA integrity_check` and no rows from
  `PRAGMA foreign_key_check`.
- Race-enabled regressions freeze a diff, change a source's size, verify that it
  cannot re-enter that epoch after being marked
  `source_changed_after_manifest`, and verify that the next scan creates a new
  pending revision. Another regression confirms that an append-only manifest
  cannot claim pending work belonging to an unscanned root.
- The rebuilt standard executable was also exercised against 121 test FLAC
  files while one file was enlarged after manifest publication. The first run
  completed 120 jobs, reported one `skippedChanged`, exited with the documented
  partial code, and wrote the expected-size/observed-size issue. The next run's
  diff contained exactly that one new revision and completed it successfully.
- Real bundled FFmpeg 8.1.2 tests fully decoded valid FLAC and MP3 fixtures, then
  damaged 2,048 bytes in the middle of each file. Both damaged files still
  passed the lightweight ffprobe step and both failed the full decode integrity
  step as `ErrCorrupt`. An actual metadata-only indexer run retained metadata
  for both damaged files with `integrity.status=corrupt`; an offline audio run
  warmed the real MERT runtime and recorded both audio jobs as permanent
  `corrupt_media` failures without entering DSP/MERT. A process regression also
  changes file size while validation is running and observes
  `ErrSourceChanged` from the second revision check.
- A deterministic process test starts an integrity child that produces no
  decoded output, shortens the production 30-second activity threshold, and
  verifies controlled termination as retryable `ErrProcessStalled` rather than
  misclassifying the file as corrupt.
- The current source launcher was executed in a real pseudo-terminal against a
  500,000,001-byte sparse FLAC and a second file. Directory/file activity, the
  summary, count bar, and percentage remained present while discovered and
  queued totals advanced from zero to two. The validation shell exports
  `NO_COLOR=1`, so red pixels are not claimed from that run; focused tests verify
  that only FLAC files strictly over the decimal 500 MB threshold select the
  PTerm warning style. A deterministic bar test advances a completed 1/1 bar to
  a growing 1/3 total and observes a nonempty 33% rendering, and verifies the
  30-second forced-redraw boundary.
- A process-isolation test runs a deliberately silent MERT child with a shortened
  watchdog, verifies an error matching both `ErrNativeWorker` and deadline
  exceeded, verifies that the child is killed/reaped, then successfully starts
  a fresh healthy worker. A concurrent-directory test adds a FLAC from the scan
  callback, forces a directory revision change, and verifies a complete two-file
  inventory/report without retry double-counting.
- The current rebuilt offline executable completed a fresh serial fast-profile
  run over a real FLAC with the scan/diff barrier. Its manifest contained one
  inventory row and two pending jobs. One native session warmed in 3.725 seconds
  with measured 670,334,976-byte RSS, and the run committed one metadata, DSP,
  and real 768-dimensional MERT result with zero failures or retries. The source
  SHA-256 was unchanged. This verifies normal inference on this host; it is not
  a general latency guarantee.
- The opt-in real-audio concurrency benchmark executed on an identical
  deterministic two-track sample. Serial, 2-worker, 4-worker, and auto runs all
  committed two metadata and two audio results with semantic digest
  `86aac444565764ef5892e82b24364d60982db37861ebaee939628bd3c6db880d`.
  Current-artifact warm analysis observations were 2.100 s, 2.035 s, 1.159 s,
  and 0.549 s; cold setup/warmup was 7.38–7.85 s. Peak aggregate owned RSS was
  745 MB, 768 MB, 1.268 GB, and 1.312 GB respectively, all below the configured
  4 GiB target. The same run completed v3 fit/export, state reopen, and 25 pinned
  exact queries per configuration; observed p95 query latency ranged from 3.479
  to 17.007 microseconds and resume-open time from 1.417 to 1.994 ms. This sample
  is too small for a general speedup claim and is recorded only as executed
  overlap/equivalence and measurement-path evidence.

## Clean runtime run

The current rebuilt offline artifact was executed as UID/GID 65534 in
`debian:12.12-slim` with `--network none`, a read-only two-track FLAC/M4A
source, and no runtime Go, Python, FFmpeg, Wails, or desktop libraries. The
serial run committed 2 metadata + DSP + real MERT records, fit and indexed the
frozen generation, and exported a valid 11,788-byte v3 pack with SHA-256
`144065d1e933036e432858f4025f71c1bda028ddc18b770cb5879a0c9fb0f76c`.
Before/after hashes for both source files were identical.

An earlier broader offline artifact was executed as UID/GID 65534 in
`debian:12.12-slim` with `--network none`. The container had no Go, Python, or
system FFmpeg. Source fixtures were mounted read-only. The serial command
successfully extracted both native payloads, warmed the actual CPU MERT worker,
processed seven FLAC/MP3/raw-AAC/M4A files, committed 7 metadata + DSP + MERT
records, built a 7-row exact 768-dimensional index, learned compatible DSP
percentiles, and published a 26,575-byte
v2 paipack. Before/after SHA-256 lists for every source file were identical.
The pack SHA-256 was
`6fb2f203e0ee826fdf0c67812db2cec20d758485e1f10ca4b1204f136527e39d`.
An additional real 192 kHz anti-phase fixture produced the intended partial
outcome: exit 2, one valid metadata record, one valid DSP record, no MERT row,
and one persisted failed audio job. This verifies that severe downmix silence
does not fabricate an embedding and no longer discards the independent DSP
branch.

One earlier clean run failed and is retained as evidence: explicit child launch
used the CLAP worker flag instead of `--mert-worker`. A focused worker regression
and `go test -race ./internal/audio ./internal/audioruntime` passed after repair.
A second run reached export and found untagged fixtures violated the pack's
nonempty display fields; export now records a filename title fallback and an
explicit unknown artist/missingness provenance. The subsequent run passed.

The Linux amd64 standard and offline executables were rebuilt from this
worktree using the previously verified pinned codec/MERT payloads:

```text
bin/playlist-indexer          50d2bc47be1c86b6823fcf65810a832a62fd608c87df3ce5bd727d0aee82768e
bin/playlist-indexer-offline  39fdb7d7f1b583b2e586407c023ce2bdb2dc4ebada236ab496779b5b9c170bc7
```

The standard file is 24,673,349 bytes. The dual CPU/CUDA offline file is
4,036,423,266 bytes; downloaded runtime/model inputs remain excluded from Git.
`./scripts/test.sh` passed after the in-memory reuse, dual offline packaging,
native CUDA, and fingerprint-tag changes: 221 frontend tests, production
frontend build, `go vet`, the full race-enabled Go suite, and golangci-lint with
zero findings. The
`GOOS=windows GOARCH=amd64 go test -run '^$' ./...` cross-compile check also
passed after these changes; this is not a native Windows execution claim.

## Honest limits

HE-AAC has not been exercised by a positively identified fixture and is not
claimed. Linux arm64, musl/Alpine, macOS generation cleanup, Windows CUDA
inference, and native GUI execution are unverified. The exact index is implemented; no ANN
recall or GPU parity claim is made. A synthetic 2,000,000-row benchmark and a
200-track authorized real-library concurrency benchmark have not yet been run,
so there is no two-million-track duration, RSS, or speedup claim.

The new scale harness itself was executed with 128 rows, dimension 8, two
workers, three pinned exact queries, and a 512 MiB target. It completed index
construction and v3 pack export, reporting 13,930,496 bytes peak owned RSS,
7,609 index bytes, 5,475 pack bytes, 53.925 microsecond p50 and 66.715
microsecond p95 query latency. These tiny synthetic smoke values validate the
measurement path only; they are not the missing two-million-row result.

Fit now streams its frozen metadata and vectors, spills deterministic diverse
sampling to SQLite, caps the training set from the RAM plan, checkpoints
completed spherical-k-means boundaries, streams assignments to an immutable
SQLite store, and feeds the exact index from a bounded row source. Export uses
the streaming `librarypack.WriteSource` path: one current track/vector, bounded
SQLite and zstd buffers, fixed-size learning/statistics payload limits, and an
atomic final publisher. The legacy slice writer remains for small callers but
is not used by the indexer.

The v3 pack contains the fitted metadata model, per-track cluster assignments,
and deterministic compatible-contract DSP percentile distributions. The
desktop metadata channel loads weighted TF-IDF and SVD values from normalized
tables, combines them with sourced-tag matching, uses cluster membership as
soft rank-fusion/diversity evidence, and maps only reviewed acoustic concepts
to compatible sampled-DSP percentile boosts.

The metadata fitter still retains corpus-scale artist/album/genre association
maps while fitting. Runtime RSS/quota-shrink feedback, complete required status
telemetry, and the unexecuted 2M stress gate remain release blockers until the
current worktree is rebuilt and those measurements are executed.
