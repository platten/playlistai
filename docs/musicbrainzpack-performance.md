# MusicBrainz packer performance investigation

Measured on 2026-09-13 on Windows amd64, Intel Core Ultra 9 285H,
Go 1.27.0 (`GOMAXPROCS=16`), modernc.org/sqlite 1.58.0, ulikunitz/xz 0.5.15,
and klauspost/compress 1.20.0. The working branch was
`codex/miniml-mert-optimize`, based on `0a57303`, with the existing uncommitted
MusicBrainz packer implementation. Baseline and changed import binaries were
compiled from the worktree before and after this change; the base commit alone
does not contain that baseline packer. Each result below is the median of three
single-iteration runs. CPU-intensive experiments ran sequentially.

These are deterministic synthetic fixtures. No full MusicBrainz snapshot was
downloaded or benchmarked. Allocation counts are cumulative Go allocations,
not peak process memory. Source archives, generated indexes and profiling
artifacts are not committed.

## End-to-end index build

`BenchmarkBuildSynthetic` creates 1,000 artists and 10,000 recordings with tags,
ISRCs, credits, permuted recording IDs and large ignored metadata strings. It
measures XZ decoding, JSON import, both SQLite stages, merging, index creation,
and VACUUM. Fixture generation and packaging are excluded.

| Implementation | Median build time | Allocated bytes per build |
| --- | ---: | ---: |
| Previous parallel-stage importer | 0.928 s | 117.9 MB |
| Typed decoding pipeline, 64 MiB cache, artist deduplication | 0.566 s | 61.2 MB |

This fixture built **1.64 times as fast**, taking about **39% less time** and
allocating about **48% fewer bytes**. Raw baseline times were 0.928, 0.941,
0.900 seconds; changed times were 0.566, 0.570, 0.563 seconds. An earlier
CPU-profiled baseline was slower and is not used for this comparison.

## Database transactions, memory and intermediate tables

The previous importer already used one transaction per archive, prepared
statements, deferred secondary indexes, and disabled synchronous/journal writes
on disposable staging files. More transaction batching alone would not remove
the other costs.

`BenchmarkRecordingSQL` isolates database writes, index creation and compact
disk output. The default fixture has 30,000 random UUID recordings, 6,000 artists,
one credit and ISRC, and two tags per recording. Random generator: PCG(31,97).
Every variant verifies all five final table counts and yields a 25,686,016-byte
database. JSON, normalization and XZ are excluded.

| Strategy | Median |
| --- | ---: |
| Previous disk cache defaults | 2.091 s |
| Disk with 64 MiB cache | 0.695 s |
| In-memory SQLite plus export to disk | 0.662 s |
| Disk, 64 MiB, batches of 64 | 0.635 s |
| Disk, 64 MiB, skip repeated artist writes | 0.585 s |
| Disk, 64 MiB, batches and artist deduplication | 0.603 s |

The 64 MiB cache holds this small fixture, so a second experiment used 150,000
recordings, 30,000 artists and a **128,135,168-byte** final database, exceeding
the cache size:

| Strategy | Median |
| --- | ---: |
| Previous disk defaults | 13.249 s |
| Disk, 64 MiB, artist deduplication | 3.925 s |
| In-memory SQLite plus export | 3.351 s |
| Append-only intermediate tables, then sorted insert into final tables | 3.449 s |

The selected disk approach is **3.38 times as fast** as the old database path
on the larger fixture. Full in-memory SQLite is about 15% faster than the
selected disk approach here, but requires the entire database to fit RAM.
Sorted staging preserves duplicate handling and gains about 12% on this fixture,
at the cost of copying all retained tables and additional temporary storage.
It is retained as an experiment, not enabled in production. Large multirow
batches reduce allocations but did not improve elapsed time once artist
deduplication was applied on the smaller fixture.

Production uses a configurable 64 MiB page-cache target per active database,
fresh connection settings on finalization, and a bounded exact artist cache.
On a cache miss, `INSERT OR IGNORE` still preserves the first fallback artist.
The authoritative artist archive subsequently overrides fallback artist rows.

SQLite documents that [`cache_size` is connection-local and lazily allocated](https://www.sqlite.org/pragma.html#pragma_cache_size),
and that [`:memory:` databases remain entirely resident](https://www.sqlite.org/inmemorydb.html).
The page-cache setting is not a total process-memory limit.

## Compression and checksum pass

`BenchmarkBundleCompression` and `BenchmarkBundleCompressionWindow` use a
25,665,536-byte SQLite fixture with 40,000 recordings and 4,000 artists. They
measure encoding to a counting writer, including encoder close. Fixture
creation, disk output, hashing and bundle verification are excluded.

| Encoder setting | Median | Compressed bytes | Approximate allocated bytes |
| --- | ---: | ---: | ---: |
| Previous Best, 128 MiB window | 899.5 ms | 3,960,489 | 307 MB |
| Better, 128 MiB window | 147.1 ms | 4,140,231 | 275 MB |
| Selected Better, 8 MiB window | 144.2 ms | 4,179,001 | 23 MB |

Selected encoding is **6.24 times as fast** on this fixture, with **5.52% larger
output**. The Default level took about 105.6 ms but produced 9,236,275 bytes,
so its size tradeoff was rejected. The encoder uses two workers. The smaller
window reduces retained encoder state; the decoder limit remains 128 MiB so
previously generated bundles stay compatible.

SQLite hashing is now performed through a tee during compression. This removes
one full input read without changing SHA-256 verification, the manifest or the
199,000,000-byte default part limit. Compression completion is emitted after the
encoder and part files close successfully. See [the upstream encoder](https://github.com/klauspost/compress/tree/master/zstd)
and the pinned module's `encoder_options.go` for option definitions.

## Reproduce

From the repository root in PowerShell:

```powershell
$env:GOCACHE = (Resolve-Path .tmp/gocache)
go test ./internal/mbindex -run '^$' -bench '^BenchmarkBuildSynthetic$' -benchtime=1x -count=3 -benchmem
go test ./internal/mbindex -run '^$' -bench '^BenchmarkRecordingSQL$' -benchtime=1x -count=3 -benchmem
go test ./internal/mbindex -run '^$' -bench '^BenchmarkBundleCompression$|^BenchmarkBundleCompressionWindow$' -benchtime=1x -count=3 -benchmem

$env:MBPACK_BENCH_SQL_ROWS = '150000'
go test ./internal/mbindex -run '^$' -bench '^BenchmarkRecordingSQL/(disk_default_single|disk_64MiB_single_dedup|memory_single_export|disk_64MiB_sorted_stage_dedup)$' -benchtime=1x -count=3 -benchmem
Remove-Item Env:MBPACK_BENCH_SQL_ROWS
```

The normal regression suite stays small and offline. It covers metadata member
selection, records over 16 MiB, typed projection and field reset, ordered delivery,
malformed JSON, producer cancellation/join, prior-output preservation, artist
identity, checksums, compression completion and package/install roundtrips.
Full-snapshot throughput, peak RSS, compression ratio and performance on other
operating systems remain unmeasured.
