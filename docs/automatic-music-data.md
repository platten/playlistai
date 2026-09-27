# Prepared automatic music data

`internal/musicgraph` provides optional, public discovery priors for the local
playlist engine. It stores exact MusicBrainz artist/recording IDs, nullable unique
listener and listen counts, artist-recording suggestions and directed neighboring
artist suggestions. It contains no user history, prompts, API keys, genre claims
or audio. Names never join identities. Catalog resolution must still identify the
exact recording/version before playback or musical ranking.

## Prepare and verify

Create a JSON array of explicit seed artist MBIDs in an external work directory.
Then run the opt-in preparation command:

```sh
go run ./cmd/musicgraph fetch -input /external/work/seeds.json \
  -output /external/work/musicgraph-2026-09.json
go run ./cmd/musicgraph inspect -input /external/work/musicgraph-2026-09.json \
  -sha256 THE_SHA256_PRINTED_BY_FETCH
```

No command chooses a default output directory, installs an asset, opens a user
store or replaces an existing file. `prepare -input projection.json -output
new-snapshot.json` validates and canonicalizes an already prepared projection
without network access. Its input uses the exported `musicgraph.Snapshot` JSON
schema. All counts are JSON integers or null; an unknown count is never zero.

Each seed uses one radio response containing up to 10 seed-artist suggestions
and 20 neighboring artists, with up to 10 recordings per neighbor. Radio's seed
group becomes the artist-recording list and is excluded from neighbor edges.
Preparation then batches public aggregate counts for all resulting artist and
recording IDs. These are bounded suggestions, not an exhaustive or guaranteed
all-time top list; `TopRecordings` retains that API name for compatibility and
orders only the returned candidates. Radio may return different suggestions on
a later preparation, but each saved snapshot stays fixed.

Similar-artist edges express provider discovery suggestions, not a measured
similarity probability. Radio identifies an associated artist, not complete
performer billing or track title/version. Conflicting discovery batches are omitted;
exact recording IDs must resolve in the catalog. Empty neighborhoods are valid,
and missing aggregate counts remain null. The radio response receipt remains
in `source`; recording-count provenance is separately retained in `countsSource`.

The single JSON artifact is bounded to 64 MiB, schema-versioned and validated
before publication. Publication uses a synced temporary file and an atomic
create-only hard link in the same directory. Unsupported filesystems fail without
replacing any installed snapshot. Choose a new versioned file for each update.
The asset installer must pin the exact file SHA256 in its trusted manifest, call
`musicgraph.Open(ctx, path, expectedSHA256)`, then activate it through the existing
asset flow. A checksum detects corruption; it does not authenticate an untrusted
download unless the expected checksum comes from that trusted manifest.

The reader loads immutable MBID indexes into memory and returns copies. It never
fetches data. `SnapshotIdentity` is the artifact hash; save it with generation
inputs so replay remains reproducible. `LookupArtistPopularity` implements the
identity reader, and `TopRecordings`, `SimilarArtists`, `Recording` and `Artist`
serve candidate preparation. `PopularityRank` is a listener-count percentile
within this snapshot's known recordings, with equal counts sharing a rank. It is
an ordinal tie-break, not a probability or global popularity measure. Fewer than
two known recordings or a missing count produces unknown.

## Public provider boundaries

The producer calls ListenBrainz's public artist and recording aggregate counts
and artist radio. The current contracts are documented in the
[popularity API](https://listenbrainz.readthedocs.io/en/latest/users/api/popularity.html)
and [radio API](https://listenbrainz.readthedocs.io/en/latest/users/api/core.html#get--1-lb-radio-artist-(seed_artist_mbid)).

A bounded check on 2026-09-27 found that the documented artist top-recordings
endpoint requires authentication (HTTP 401), while radio and the two aggregate
POST endpoints accepted requests without a token. This producer does not call
or bypass the restricted endpoint. Historical offline projections using its
source receipt remain readable. Public access can change: each unavailable or invalid radio batch is omitted
whole and recorded in `omittedDiscovery` with its seed, reason and response hash
when a response was validated far enough to retain one. Working seed artist
counts and other valid batches remain usable. Parent cancellation or invalid
aggregate-count responses still abort publication; existing artifacts remain
unchanged. `inspect` reports the omitted-batch count; the projection retains
each receipt.

Requests carry the project/contact User-Agent, no cookies or credentials, at most
1000 IDs per count batch and a 4 MiB response limit. All clients share a
one-second dispatch interval. Exhausted rate headers and Retry-After can extend
the wait; redirects are disabled. Each radio seed permits one dispatch without retry. Aggregate operations permit
at most two dispatches, retrying only 429/502/503/504, inside a parent-preserving
two-minute deadline.
Preparation accepts up to 1000 seeds and has an explicit overall CLI deadline
(30 minutes by default; maximum one hour). This is offline preparation, not
playlist latency. Large provider responses fail at the size limit rather
than silently truncating the JSON.

`ForegroundArtists` is a separately opt-in aggregate refresh, one batch and at
most five seconds including waits/retries. It returns cached rows when the
optional refresh fails and propagates parent cancellation. It never updates the
snapshot or disk; callers must not mistake refreshed data for the pinned snapshot
used by an earlier generation. Routine playlist generation should use the reader.

Each saved source includes endpoint, response SHA256, retrieval date and CC0
license. Retrieval time does not claim the upstream aggregation itself is current.
Monthly replacement is an installer policy, not an automatic network timer in
this package. The public listen data is [CC0](https://listenbrainz.org/download/),
and clients must respect the documented [API rate limits and User-Agent rules](https://listenbrainz.readthedocs.io/en/latest/users/api/index.html).
This graph is intentionally separate from imported tags, recording claims and
musical confidence. It does not establish genre, mood, absence of vocals or
whether a catalog contains playable versions of the suggested recordings.

## Verification limits

Normal tests use temporary artifacts and loopback HTTP only. They cover nulls,
IDs, deterministic ordering, source receipts, corruption/version rejection,
atomic failure, response bounds, rate waits, retry and cancellation. Preparing an
artifact does not measure catalog coverage, recommendation quality or the
two-minute playlist target. Broad artist coverage requires an explicit maintainer
seed set; v1 does not discover every artist or download the full listen history.

## Desktop activation

`Container.ImportMusicGraph(ctx, path, expectedSHA256)` verifies a locally
provided artifact and retains it as `DataDir/music-graph/graph-SHA256.json`.
Only after validation and synced publication does it replace `active.json`.
An installation lock excludes concurrent desktop processes. Existing snapshots
remain available through `PreparedMusicGraphSnapshot(ctx, hash)` for pinned
requests; a missing historical hash is an error, never a substitute with newer
data. `PreparedMusicGraph(ctx)` returns nil when nothing is installed and errors
for invalid active data. Normal reads remain offline.

`CheckMusicGraphUpdate` and `InstallMusicGraph` take an explicitly configured
public HTTPS manifest URL. The installer reuses the dataset downloader with the
declared size and SHA256, rejects redirects, verifies the schema and matching
preparation date, and activates only a completed artifact. A release manifest is:

```json
{
  "version": "prepared-music-graph/v1",
  "preparedAt": "2026-09-01T00:00:00Z",
  "snapshot": {
    "name": "graph-<64 lowercase hexadecimal SHA256>.json",
    "size": 12345,
    "sha256": "<64 lowercase hexadecimal SHA256>",
    "url": "https://your-published-asset-host/musicgraph-2026-09.json"
  }
}
```

Those values must describe the actual published artifact; the example is not a
downloadable release. The manifest URL is a trusted application/configuration
input. This change supplies verified import/update mechanics, not a public data
release, automatic monthly job or new default network dependency. Activation is
tested on the current host; native Windows/macOS behavior is not established by
the offline tests.
