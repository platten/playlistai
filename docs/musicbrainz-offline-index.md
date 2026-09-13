# Offline MusicBrainz index and R2 bundle

`cmd/musicbrainzpack` turns the official MusicBrainz artist and recording JSON
dumps into the compact SQLite index used by Playlist AI. A normal run performs
the complete pipeline: it discovers the current snapshot, reads the publisher's
SHA-256 list, downloads and verifies both archives, imports the required fields,
compresses the SQLite file with Zstandard, splits the compressed stream, and
verifies the upload bundle.

The artist and recording archives download concurrently. During indexing, each
archive is decoded into an independent SQLite stage in parallel; the recording
stage becomes the final database and absorbs the smaller artist stage before
indexes are created. This preserves SQLite's single-writer behavior while
overlapping XZ decoding, JSON parsing, normalization, and table writes. Existing
archives with the expected size and SHA-256 are verified and reused.

Within each import, a producer decodes JSON directly into the retained fields
and feeds a 32-record queue while a single writer inserts into SQLite. This
avoids copying entire objects into `json.RawMessage` and then decoding them
again. An exact cache of up to 65,536 artist MBIDs suppresses redundant fallback
artist writes; recording-specific credit names and authoritative artist metadata
are still preserved.

This index is independent of the Deej-AI catalog. In Enhanced hybrid, prompt
terms first pass through the music thesaurus and select MusicBrainz artist/tag
pools. A recording already in Deej-AI keeps its catalog identity. A recording
outside Deej-AI is admitted only after the strict Deezer resolver finds one
unambiguous artist/title/version match with a playable preview. The app assigns
that track a stable `deezer:<id>` identity and records its non-audio metadata in
the local candidate catalog for saved-history hydration.

The preview then follows the ordinary analysis and recommendation path: CLAP
checks prompt/audio fit when installed, MERT and local DSP add enhanced audio
evidence when enabled, archived AcousticBrainz measurements are joined by the
MusicBrainz recording MBID, and the shared eligibility, deduplication, ranking,
selection, and sequencing rules decide whether the candidate appears. A
MusicBrainz tag nominates an artist or recording; it never proves the requested
sound by itself. Deezer preview URLs remain transient and are not persisted.

Dynamic resolution is bounded to 20–100 recording attempts per request (eight
times the requested track count within those limits). It is enabled only for
Enhanced hybrid, so Deej-AI-only, AcousticBrainz-first, and CLAP-first policies
retain their existing candidate spaces and version semantics. Dynamic tracks do
not become dense Deej-AI rows and never receive fabricated Deej-AI vectors.

## One-shot build

Use an empty bundle directory. The work directory retains the verified source
archives and intermediate SQLite file so an interrupted or later packaging run
does not need another full download.

The command shows separate progress bars for artist and recording downloads,
parallel archive processing, final index preparation, SQLite hashing, Zstandard
compression, bundle-part verification, and verification of the packed index.
Byte-based stages show throughput and an estimated completion time once enough
data has been processed; the finalization bar reports completed SQLite steps and
an approximate ETA after its first step. Import byte counters refer to processed
uncompressed JSON member bytes, so decoder read-ahead cannot mark queued records
as processed. Hashing and compression now happen in the same pass.

```powershell
go run ./cmd/musicbrainzpack `
  -work-dir D:\playlistai\musicbrainz-work `
  -bundle-dir D:\playlistai\musicbrainz-upload
```

```sh
go run ./cmd/musicbrainzpack \
  -work-dir /srv/playlistai/musicbrainz-work \
  -bundle-dir /srv/playlistai/musicbrainz-upload
```

The default source is the [official MusicBrainz JSON dump
directory](https://ftp.musicbrainz.org/pub/musicbrainz/data/json-dumps/). The
tool downloads `artist.tar.xz` and `recording.tar.xz` from the snapshot named by
`LATEST`. The [MusicBrainz database download
documentation](https://musicbrainz.org/doc/MusicBrainz_Database/Download)
describes the dump publication and replication schedule.

For an already downloaded snapshot, provide both archives and the snapshot ID:

```sh
go run ./cmd/musicbrainzpack \
  -artist-archive /dumps/artist.tar.xz \
  -recording-archive /dumps/recording.tar.xz \
  -snapshot 20260912-001001 \
  -work-dir /srv/playlistai/musicbrainz-work \
  -bundle-dir /srv/playlistai/musicbrainz-upload
```

Rerunning the command automatically reuses a completed intermediate SQLite index,
keeping its embedded snapshot and skipping downloads and imports. Use
`-replace-index` to rebuild it (and fetch the latest snapshot for official downloads).
An explicit `-snapshot` must match the reused index. Invalid indexes require
`-replace-index`. The bundle directory must be new or empty; existing upload files
are never overwritten. These checks run before downloading. Progress uses pterm
bars on stderr, with throughput, per-stage ETA, and imported row counts; stages
appear only after they start. JSON results remain on stdout. `-download-only` stops after
verified official downloads. `-verify-bundle PATH` checks every part, joins and
decompresses the stream, verifies the extracted SQLite hash, and opens its
embedded manifest.

`-sqlite-cache-mib` controls the page-cache target per database (default **64**,
range 1–4096). Both import databases run concurrently, so the default targets
128 MiB of SQLite cache in total during import. SQLite sort buffers, decoder
buffers, retained JSON fields, the artist cache, and Go runtime memory are
additional. The queue is bounded by record count, not bytes; unusually large
records remain supported. The database stays on disk and need not fit RAM.
The cache and import PRAGMAs are reapplied when finalization opens a new connection.

The same command uses the faster defaults automatically. A machine with spare
memory can opt into a larger cache, for example `-sqlite-cache-mib 256` (up to
512 MiB of cache across the concurrent import databases). This is a tuning
option, not a measured guarantee of additional speed. See
[the performance investigation](musicbrainzpack-performance.md) for measurements
and comparisons against in-memory SQLite and intermediate staging tables.

## Retained data

The importer streams JSON objects directly from the XZ-compressed tar archives.
It does not stage expanded JSON files. The SQLite database retains only:

- artist MBID, name, sort name, disambiguation, aliases, genres, and tags;
- recording MBID, title, disambiguation, duration, first-release date, ISRCs,
  artist credits, genres, and tags;
- normalized lookup keys and the indexes needed for tag, artist, alias, title,
  and recording searches;
- snapshot, build time, source checksums, row counts, format versions, and data
  licenses.

Release payloads, cover art, URLs, ratings, user collections, edit history,
relationships, and other unused MusicBrainz entities are omitted. Videos and
recordings without a title or artist credit are skipped. Python, PostgreSQL,
and a MusicBrainz server are not required.

MusicBrainz [core data is
CC0](https://musicbrainz.org/doc/About/Data_License). Community tags and genres
are supplementary data under CC BY-NC-SA 3.0, so both license identifiers are
recorded in the SQLite metadata and distribution manifest. Review those terms
for the intended distribution. The tooling does not combine all data under the
core-data license.

## Bundle layout and size boundary

The upload directory contains only the manifest and ordered compressed parts:

| File | Purpose |
| --- | --- |
| `musicbrainz-manifest.json` | Snapshot, format and license metadata; extracted file and ordered part sizes/SHA-256 values |
| `musicbrainz.sqlite.zst.part-00001` ... | Consecutive sections of one Zstandard stream |

The default part limit is **199,000,000 bytes**, safely below a decimal 200 MB
object limit. `-part-bytes` may lower it but cannot exceed 200,000,000. The
compressor writes directly into the part files, so no additional monolithic
`.zst` file is created. It uses Zstandard's `BetterCompression` with an 8 MiB
encoder window and calculates the SQLite SHA-256 during that same input pass.
The installer still accepts the 128 MiB window used by earlier bundles. Do not
rename, reorder, concatenate, or independently
recompress the files after packaging.

The actual number of parts, download bytes, SQLite size, import duration, and
recording coverage depend on the selected MusicBrainz snapshot. Record these
from a real completed build; this repository does not claim measurements from a
synthetic fixture.

## Upload to Cloudflare R2

Upload every file in the directory under one immutable prefix. Cloudflare
documents `rclone copy` for [bulk directory uploads to
R2](https://developers.cloudflare.com/r2/objects/upload-objects/):

```sh
rclone copy /srv/playlistai/musicbrainz-upload r2:YOUR_BUCKET/musicbrainz/20260912-001001
rclone ls r2:YOUR_BUCKET/musicbrainz/20260912-001001
```

The public manifest URL then resembles:

```text
https://YOUR_PUBLIC_HOST/musicbrainz/20260912-001001/musicbrainz-manifest.json
```

All part URLs are resolved relative to that manifest, so the names and directory
must stay together. Serve the JSON as `application/json` and the parts as opaque
binary objects. Byte-range support allows interrupted downloads to resume. An
immutable cache policy is suitable for the versioned prefix. Publish a new
prefix before changing the app's manifest URL; never replace only some objects
inside a published bundle.

`rclone` is the simplest bulk path. Cloudflare's Wrangler object command uploads
one object at a time, while its R2 documentation recommends `rclone` for multiple
files. The packer never receives R2 credentials and does not upload or publish
anything itself.

## Configure desktop downloads

The desktop default points to the published Cloudflare R2 manifest:

```toml
[metadata]
musicbrainz_manifest_url = "https://pub-233adf724b7e476db67cf787cd301c9e.r2.dev/musicbrainz/musicbrainz-manifest.json"
```

The URL can be overridden in the configuration loaded through
`PLAYLISTAI_CONFIG`, or by setting
`github.com/platten/playlistai/internal/config.DefaultMusicBrainzManifestURL`
with a Go linker value for another verified distribution.

When configured, **Local music knowledge** in the wizard and **Settings → Music
metadata** offer the download. The installer downloads each part with bounded
resumption, verifies every size and SHA-256, streams the ordered files through a
memory-bounded Zstandard decoder, verifies the SQLite file and embedded
snapshot, then switches the active pointer. It removes downloaded part files
after successful activation. A failed update leaves the prior active index
available.

The checksums detect corruption and mismatched objects; they are not signatures.
The HTTPS manifest host is the trust root. Resetting app-managed assets removes
the installed index, while clearing the metadata query cache keeps it.

## Validation

```sh
go test ./internal/mbindex ./internal/app ./internal/enrich/musicbrainz ./internal/bridge
go run ./cmd/musicbrainzpack -verify-bundle /srv/playlistai/musicbrainz-upload
./scripts/test.sh
```

Tests use small generated `.tar.xz` fixtures. They cover streaming import,
artist-tag and alias lookup, recording details, forced multipart packaging,
per-part limits and checksums, HTTP installation, activation, and cleanup. A
production snapshot should also be built and verified before uploading because
the fixture does not measure full-dump resource use or detect future source
schema changes.
