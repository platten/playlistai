# Playlist AI portable library pack (`.paipack`)

Status: format version 5.

A paipack is a portable, immutable recommendation-data snapshot. It contains
metadata and derived analysis only. It never contains audio or PCM. Source
locations are represented, when explicitly requested by the pack producer, as
a logical root alias plus a safe relative path. Absolute source paths and root
mount mappings are not portable pack data.

The Go implementation is `internal/librarypack`. It has no Wails dependency and
is shared by the analyzer and desktop integration.

Version 5 is intentionally incompatible with version 4 because the SQLite row
schema and capability contract gained an AcoustID ID field and index. Rebuild
an older pack with the current `playlist-indexer`; activation remains atomic,
so rebuilding does not mutate an already installed generation or any source
audio.

## Envelope and members

The file is a Zstandard-compressed POSIX tar stream. Version 5 contains exactly
these regular-file members, in this order:

1. `manifest.json`
2. `metadata.sqlite`
3. `mert.f32`

No directories, links, devices, sparse files, duplicate names, alternate data
streams, absolute paths, or nested member paths are accepted. Tar timestamps
are the Unix epoch, modes are `0600`, and the writer uses one deterministic
Zstandard encoder worker. A writer first completes and syncs a temporary file
on the destination filesystem and then atomically renames it over the requested
destination. Cancellation or a write error leaves the previous destination
untouched.

The reader bounds the compressed archive, member count, each member, total
expanded bytes, the manifest, record count, individual database records, JSON
fields, and vector dimension before exposing a generation. Default bounds are
defined by `librarypack.DefaultLimits`; callers may choose tighter positive
limits. Zstandard decoder memory and window sizes are independently bounded.

## Manifest

`manifest.json` uses strict JSON: unknown fields and trailing values are
rejected. It records:

- the format and schema version;
- a semantic `packId`;
- corpus, metadata, MERT, clustering, and statistics generations;
- track, metadata, MERT, DSP, failed, and unsupported coverage counts;
- the complete `library_mert` representation contract;
- logical root aliases, never their machine-specific mappings; and
- the byte size and SHA-256 of both payload files.

The MERT contract includes dimension, dtype, byte order, normalization,
checkpoint and graph identity, decoder, preprocessing, sampling, pooling,
observed-audio scope, and missingness semantics. Version 1 permits only
little-endian normalized float32 vectors. Cosine scores from unequal contracts
must remain in separate spaces.

`packId` is SHA-256 over canonical JSON for the manifest with an empty
`packId`, sorted file entries, and sorted root aliases. It identifies semantic
contents and provenance. Import also records a SHA-256 over the complete
compressed archive; that distinct hash identifies the exact imported file.

## Metadata snapshot

`metadata.sqlite` is an immutable SQLite snapshot. `pack_info` identifies the
format and normalized resource schema. Version 5 has no monolithic
`learning_json` or `statistics_json` value. Vocabulary/IDF values, sparse
artist rows and associations, SVD values, training sample identities,
spherical centroids/counts, and DSP summaries are stored in canonical keyed
tables. Consumers load only the resources they query. Per-track assignments
remain on `tracks`, avoiding a corpus-sized in-memory assignment object.

The normalized DSP tables contain deterministic library-relative distributions partitioned by the
exact DSP version, sampling policy, and observed-audio scope. It records
known/missing/partial counts and reasons, deterministic sampled breakpoints,
Type-7 p05/p25/p50/p75/p95 quantiles, and its own generation identity; a
percentile is approximate whenever not every known observation fits the
declared bounded sample. `tracks` is keyed by the stable, source-namespaced track ID
and is inserted in ascending ID order. It preserves artist, title, album
artist, album, optional root alias and safe relative path, raw tag JSON, DSP
JSON, explicit missingness JSON, reliable duration/provenance, normalized
artist/title values, source identity, optional authoritative ISRC/MusicBrainz
recording/AcoustID identities, and a base64-compressed AcoustID Chromaprint
value when copied from a tag or generated locally. The fingerprint contract,
algorithm, embedded-tag or full-selected-stream scope, decoder identity, and a
verified SHA-256 lookup digest are stored with the value. Rows also preserve recoverable
failure/unsupported reasons, a canonical capability list, an optional MERT row, and optional primary/alternate
spherical cluster assignments with their cosine similarities. Assignments are
stored by track row rather than as one giant JSON array.

Capabilities are evidence availability, not inferred labels:

- `metadata` is present for every row;
- `mert` means that an actual valid packed vector exists;
- `dsp` means DSP evidence is present; and
- `local_path` means an alias-relative source reference is present; and
- `audio_fingerprint` means a complete validated local AcoustID/Chromaprint
  fingerprint is present; and
- `acoustid` means a valid tagged AcoustID ID is present.

Valid ISRCs, recording MBIDs, and AcoustID IDs are authoritative duplicate evidence. A
fingerprint match is accepted as the same recording only when its compatible
full-stream Chromaprint value is exact and artist/title metadata matches or is
very similar; reliable durations must also be close. Invalid identifier tags
remain available as raw metadata but are not used to collapse recordings.
Fingerprint values are computed and compared locally and are never submitted
to AcoustID or AcousticBrainz. AcousticBrainz data is MBID-indexed analysis,
not the fingerprint format used here.

Missing capabilities never receive placeholder or zero evidence. The importer
checks capability declarations against the actual fields and vector mapping.
Before scanning variable-sized values, it queries record and JSON lengths and
rejects snapshots above the configured allocation limits.

Rows and JSON objects are semantically canonicalized independently of worker
completion or database insertion order. A creation timestamp may intentionally
distinguish separately produced snapshots; byte-for-byte reproducibility is
claimed only when all manifest inputs, including that timestamp, are equal.

## Packed MERT vectors

`mert.f32` begins with a 32-byte header:

| Offset | Size | Value |
| --- | ---: | --- |
| 0 | 8 | `PAIMERT\0` |
| 8 | 4 | little-endian format version (`1`) |
| 12 | 4 | little-endian vector dimension |
| 16 | 8 | little-endian vector row count |
| 24 | 8 | reserved zero bytes |

The header is followed by tightly packed, row-major, little-endian float32
vectors. Vector rows follow ascending track ID among tracks with MERT evidence;
`tracks.mert_row` is contiguous from zero. Every imported vector must be finite,
nonzero, and normalized within the documented serialization tolerance. Missing
MERT evidence has a null row, not a fake vector.

Readers use bounded `ReadAt` calls and return owned vector slices. They never
construct a full pairwise matrix or load the complete vector file merely to
answer one lookup.

## Import, publication, and reader lifetime

`librarypack.Manager` owns a private state root:

```text
state/
  active.json
  generations/
    <pack-id>-<unique-stage-id>/
      manifest.json
      metadata.sqlite
      mert.f32
      local-index-v1/  # consumer-built derivative; not an archive member
```

`Stage` extracts into a unique private directory, verifies every member hash,
validates the manifest, SQLite rows, capability/missingness data, vector header,
file length, row mapping, and every vector, and then opens an immutable
generation. The desktop builds deterministic metadata/artist inverted indexes
and exact MERT shards in that private directory before activation. A derivative
manifest and atomic directory rename prevent partial indexes from becoming
visible. Only one mutation may be staged at a time; overlapping mutation
attempts receive `ErrMutationInProgress`. Read pins continue concurrently.

`Activate` writes and syncs a temporary `active.json`, atomically renames it,
syncs the state directory, and only then publishes the new in-process pointer.
`Remove` publishes an empty active record by the same protocol. Neither action
touches the original music tree or the source paipack.

`Pin` increments the active generation's reference count. Replacement or
removal retires the old generation but does not close its SQLite/vector handles
or delete its managed directory until the final lease calls `Release`. Thus one
playlist request cannot observe metadata from one generation and vectors from
another. Manager shutdown closes handles after outstanding leases release but
preserves the durably active generation for restart.

This manager coordinates one application process. Multi-process mutation of
the same state directory is outside this package's contract; the desktop host
must retain its existing single-mutating-coordinator policy.

## Failure behavior and privacy

Checksum mismatch, malformed SQLite, unsafe paths, invalid JSON, inconsistent
coverage, oversized input, non-finite or zero vectors, cancellation, and
short/disk-full writes fail before publication. A failed stage or activation
does not replace the prior active generation. Imported data is read-only after
activation.

Root mappings are application-private settings stored outside the pack. A
consumer must validate a mapping and join it with the relative path without
permitting escape before local playback or M3U8 export. Pack metadata remains
useful when the corresponding source filesystem is offline.
