# Playlist indexer executed validation — through September 17, 2026

This report distinguishes commands actually executed in the implementation
workspace from remaining release gates.

## Executed native evidence

- `./scripts/test-indexer-codecs.sh` passed under `go test -race` using actual
  pinned FFmpeg processes. It covered FLAC at 44.1/48/96/192 kHz, CBR/VBR MP3,
  raw AAC-LC, M4A/AAC-LC, anti-phase stereo, over-full-scale float PCM, corrupt
  input, unusual paths, mutation fences, cancellation, and unchanged hashes.
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
  order, cancellation, path escape, replacement/removal, and pinned readers.
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
bin/playlist-indexer          5825f07eb76b9c32f5d6c40c5aede016b46455b0a4b7e5d1a6dd5d0221fc75e2
bin/playlist-indexer-offline  d9a055309b09b48377d6c8f1f6cf2c2c6ee1e6fce8e242d7817ab64d20909d46
```

The standard file is 24,064,453 bytes and the offline file is 425,534,021
bytes. `./scripts/test.sh` passed after the September 17 scan-manifest,
live activity rendering, append-root, and graceful-shutdown changes: 221 frontend tests,
production frontend build, `go vet`, the full race-enabled Go suite, and
golangci-lint with zero findings. `GOOS=windows GOARCH=amd64 go test -run '^$'
./...` also passed after these changes; this is a cross-compile check, not a
native Windows execution claim.

## Honest limits

HE-AAC has not been exercised by a positively identified fixture and is not
claimed. Linux arm64, musl/Alpine, macOS generation cleanup, GPU/CUDA, and
native GUI execution are unverified. The exact index is implemented; no ANN
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
