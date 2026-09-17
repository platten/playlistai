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
- The rebuilt standard executable was run through a real pseudo-terminal. Its
  PTerm bar reported discovered files and durable queued work on stderr. After
  the resume/rescan update, another real TTY run displayed the PTerm
  `Currently processing` box with `Artist/New Song.flac`. The separately
  redirected `--json` stdout parsed successfully, and a TTY run with
  `--no-progress` emitted no cursor/progress output.
- A real metadata-only run processed one MP3, then the same state was rerun
  after adding one FLAC. The second run reported two discovered audio files but
  exactly one metadata completion; final state contained two completed jobs.
  A third unchanged run reported zero metadata completions. Focused race tests
  also exercised selective changed-directory resume, v1-to-v2 state migration,
  and preservation of the completed job's attempt count.
- The opt-in real-audio concurrency benchmark executed on an identical
  deterministic two-track sample. Serial, 2-worker, 4-worker, and auto runs all
  committed two metadata and two audio results with semantic digest
  `ef69b24f683d79197635d352119b8f6cc90978488ad8083207af83b284910484`.
  Final-artifact warm analysis observations were 1.921 s, 1.941 s, 1.137 s,
  and 0.546 s; cold setup/warmup was 6.47–6.77 s. Measured resident MERT worker
  memory was 669 MB, 674 MB, 1.321 GB, and 1.351 GB respectively and was
  subtracted from subsequent memory admission. This sample is too small for a general
  speedup claim and is recorded only as executed overlap/equivalence evidence.

## Clean runtime run

The offline artifact was executed as UID/GID 65534 in
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

Current locally built artifacts (not published) are:

```text
bin/playlist-indexer          12f43d2ebe0243cd7929b078ed8ced771815fd0d7a3b79742d05f0ecf9f5b1eb
bin/playlist-indexer-offline  2695b757d2f7bfacb5ea0cf44a30cb823ce91d0f6c6111dac809ca2172601667
```

The standard file is 23,832,077 bytes and the offline file is 425,301,645
bytes. `./scripts/test.sh` passed after the September 17 resume/rescan and live
file-box changes: 221 frontend tests, production frontend build, `go vet`, the
full race-enabled Go suite, and golangci-lint with zero findings.

## Honest limits

HE-AAC has not been exercised by a positively identified fixture and is not
claimed. Linux arm64, musl/Alpine, macOS/Windows generation cleanup, GPU/CUDA,
and native GUI execution are unverified. The exact index is implemented; no ANN
recall or GPU parity claim is made. A synthetic 2,000,000-row benchmark and a
200-track authorized real-library concurrency benchmark have not yet been run,
so there is no two-million-track duration, RSS, or speedup claim.

Fit now streams its frozen metadata and vectors, spills deterministic diverse
sampling to SQLite, caps the training set from the RAM plan, checkpoints
completed spherical-k-means boundaries, streams assignments to an immutable
SQLite store, and feeds the exact index from a bounded row source. Export uses
the streaming `librarypack.WriteSource` path: one current track/vector, bounded
SQLite and zstd buffers, fixed-size learning/statistics payload limits, and an
atomic final publisher. The legacy slice writer remains for small callers but
is not used by the indexer.

The v2 pack contains the fitted metadata model, per-track cluster assignments,
and deterministic compatible-contract DSP percentile distributions. The
desktop metadata channel loads the weighted TF-IDF baseline,
combines it with normalized sourced-tag matching, and has an executed
genre-only local-library recommendation regression. Its SVD representation is
preserved in the pack but is not yet used in desktop scoring, and the exported
DSP percentile resource is not yet surfaced by the desktop.

The metadata fitter still retains corpus-scale artist/album/genre association
maps, and its portable model is one JSON value capped at 64 MiB. A legitimate
high-cardinality corpus can therefore exceed RAM or fail export even though
track/vector export itself is bounded. Runtime RSS/quota-shrink feedback,
complete required status telemetry, and the unexecuted 2M stress gate also
remain release blockers for calling the requested v2 turnkey release complete.
