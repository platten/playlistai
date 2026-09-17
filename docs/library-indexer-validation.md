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
- A real metadata-only run processed one MP3, then the same state was rerun
  after adding one FLAC. The second run reported two discovered audio files but
  exactly one metadata completion; final state contained two completed jobs.
  A third unchanged run reported zero metadata completions. Focused race tests
  also exercised selective changed-directory resume, v1-to-v2 state migration,
  and preservation of the completed job's attempt count.
- A separate real metadata-only run completed one FLAC under logical root
  `primary`, then reused the same state with
  `--append-root archive=<second-path>` and one M4A/AAC file. The append run
  reported exactly one discovery and one metadata completion; final status was
  two present files and two completed metadata jobs. Race tests additionally
  verify that the original job attempt remains unchanged, a repeated append is
  idempotent, and an append alias cannot silently remount an existing root.
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
bin/playlist-indexer          dc0d2de7432e1f7d686a6da148d665cc0e63212babe2755390dd58372200a8dc
bin/playlist-indexer-offline  9a9de0a6b8517a174cb5ddc545bc46830b698aedf43c1042f8f8ff113cfd9815
```

The standard file is 23,936,013 bytes and the offline file is 425,405,581
bytes. `./scripts/test.sh` passed after the September 17 production-readiness,
live activity rendering, and append-root changes: 221 frontend tests,
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
