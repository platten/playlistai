# Playlist AI portable library pack (`.paipack`)

Status: format version 7; readers retain version 5 and 6 compatibility.

A paipack is a portable, immutable recommendation-data snapshot. It contains
metadata and derived analysis only. It never contains audio or PCM. Source
locations are represented, when explicitly requested by the pack producer, as
a logical root alias plus a safe relative path. Absolute source paths and root
mount mappings are not portable pack data.

The Go implementation is `internal/librarypack`. It has no Wails dependency and
is shared by the analyzer and desktop integration.

Version 7 adds optional paired CLAP model identity and per-excerpt evidence in a
bounded `clap_evidence` SQLite table. Version 6 added optional pooled CLAP
vectors. Current writers emit version 7; existing version 5/6 archives retain
their original semantic IDs and are never rewritten during import. Missing
legacy CLAP segments, observed coverage, padding, or runtime identity remain
unavailable. Re-exporting stored indexer CLAP records can recover the segments
without new inference; the pooled-only version 6 file cannot reconstruct them.

Version 5 was intentionally incompatible with version 4 because the SQLite row
schema and capability contract gained an AcoustID ID field and index. Rebuild
an older pack with the current `playlist-indexer`; activation remains atomic,
so rebuilding does not mutate an already installed generation or any source
audio.

## Envelope and members

The file is a Zstandard-compressed POSIX tar stream. It contains these
regular-file members, in this order:

1. `manifest.json`
2. `metadata.sqlite`
3. `mert.f32`
4. `clap.f32`, only in version 6/7 when CLAP vector coverage is nonzero

Version 7's segment evidence stays inside the checksummed SQLite payload;
it adds no archive member. Version 5 contains only the first three members.

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
- corpus, metadata, MERT, CLAP, clustering, and statistics generations;
- track, metadata, MERT, CLAP, DSP, failed, and unsupported coverage counts;
- the complete `library_mert` and optional `library_clap` contracts;
- an optional version-7 `clapModel` with model, revision, preprocessing,
  dimension, paired-weights fingerprint, and runtime identity;
- logical root aliases, never their machine-specific mappings; and
- the byte size and SHA-256 of every payload file.

The MERT contract includes dimension, dtype, byte order, normalization,
checkpoint and graph identity, decoder, preprocessing, sampling, pooling,
observed-audio scope, and missingness semantics. Vector encoding version 1 permits only
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
failure/unsupported reasons, a canonical capability list, optional MERT/CLAP rows, and optional primary/alternate
spherical cluster assignments with their cosine similarities. Assignments are
stored by track row rather than as one giant JSON array.

Capabilities are evidence availability, not inferred labels:

- `metadata` is present for every row;
- `mert` means that an actual valid packed vector exists;
- `clap` means that an actual valid pooled CLAP vector exists; optional excerpt
  evidence is queried separately and is never implied by this capability;
- `dsp` means DSP evidence is present; and
- `local_path` means an alias-relative source reference is present; and
- `audio_fingerprint` means a complete validated local AcoustID/Chromaprint
  fingerprint is present; and
- `acoustid` means a valid tagged AcoustID ID is present.

Valid ISRCs, recording MBIDs, and AcoustID IDs are authoritative duplicate evidence. A
fingerprint match is accepted as the same recording only when its compatible
tagged or locally generated Chromaprint value is exact and artist/title metadata
matches or is very similar; reliable durations must also be close. Fingerprint
contracts and scopes must match exactly. Invalid identifier tags
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

## Combining packs

The standalone `paipack-combine` command combines two or more fully validated
version-5/6/7 packs and writes version 7. It does not accept version 4 or
older packs; rebuild those with the current `playlist-indexer`. Combining never
uses paths, track IDs, source identities, or previously stored
`recording_identity` strings as duplicate evidence. See
[paipack-combine.md](paipack-combine.md) for the complete merge and resource
rebuild contract.

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

## Packed CLAP evidence and pairing

`clap.f32` uses the same 32-byte header/row encoding as MERT, with magic
`PAICLAP\0`. Its dimension/count match the independent `library_clap` manifest
contract, and `tracks.clap_row` is contiguous in track-ID order. MERT and CLAP
vectors are never interchangeable.

The current indexer stores the **paired embedding fingerprint** in the CLAP
contract's historical `graphSha256` field. This fingerprint covers the exact
audio graph, text graph, tokenizer vocabulary/merges, output names and text
padding configuration; it is not just the checksum of an audio graph. A v6
text encoder may match only when that fingerprint, model, revision,
preprocessing and dimension all match the installed paired bundle. A model
name or vector dimension alone does not establish compatibility. V7's optional
`clapModel` makes the paired identity explicit and additionally requires an
exact runtime identity. Consumers must not infer runtime equivalence across
CPU/CUDA exports. V7 archives lacking an explicit identity remain unpaired
unless a compatible per-record identity is available.

The optional v7 `clap_evidence` table contains one bounded JSON record keyed by
track ID. It preserves a paired `model` when known, sampling policy, scope
`recording-relative-excerpts`, `coveredSeconds`, `incomplete`, `partialReason`,
and ordered segments. Each segment records:

- index and observed start/end offsets into the recording;
- observed seconds independently of encoder input duration;
- padding (`none`, `repeat`, `zero`, or explicitly `unknown`);
- validity (`valid`, `invalid`, or `unavailable`) and failure reason; and
- the normalized vector only for valid observations.

Repeat/zero padding adds no observed coverage. Covered seconds equal the sum
of non-overlapping valid observed intervals. The pooled vector must agree with
their duration-weighted, normalized aggregate. Invalid/missing segments contain
no vector; a partial record retains its reason. Import validates dimensions,
finite vectors, interval ordering, duration bounds, pool consistency, paired
identity, JSON/record allocation limits and track ownership before activation.
The per-row maximum is 128 segments, further bounded by the configured JSON
limit. The current indexer produces at most two excerpts.

Older indexer records already contain observed intervals. Their known
10-second input/repeat-padding producer contract allows that padding to be
expressed during re-export; unknown producer padding stays unknown. Nominal
sampling policy never supplies invented observed seconds. Pooled CLAP and
sampled excerpts remain soft musical evidence rather than a guarantee about
unheard portions of the recording.

Combining preserves evidence with its corresponding pooled vector. It may add
rich evidence to an identical pooled vector, but never attach segments from a
conflicting pooled vector. Conflicts retain deterministic first-input evidence
and are reported. Different explicit paired model identities are rejected.
When a CLAP input has unknown legacy runtime identity, no manifest-wide runtime
identity is invented; known model identity stays with individual rich records.

DSP statistics include current valid DSP results even when the corresponding
MERT job failed. Superseded contracts, obsolete source revisions and removed
files remain excluded. Changes to these partial DSP results invalidate the
corpus snapshot identity. Existing stored statistics require a fresh learning
generation to incorporate the corrected coverage; ordinary re-export preserves
the already fitted statistics rather than silently refitting them.

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
