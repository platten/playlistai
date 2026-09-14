# Music metadata

Playlist AI uses MusicBrainz for recording identity, artist aliases, release
context, ISRCs, durations, tags, and genre candidate discovery. An optional
offline MusicBrainz index is consulted before the public API. AcousticBrainz and
Deezer can add recording-level evidence after a candidate has been matched to
the recommendation catalog.

## Setup

The first-run wizard and **Settings → Music metadata** download the published
MusicBrainz archive from the configured Cloudflare R2 manifest. The installer
downloads parts no larger than 200 MB, verifies their checksums, joins and
decompresses the archive locally, validates the SQLite index, and activates it
atomically. See [Offline MusicBrainz index](musicbrainz-offline-index.md) for the
maintainer-side build and publishing process.

The offline index is optional. Without it, discovery uses the public MusicBrainz
API within the existing request budget. Installation does not require the
recommendation catalog because MusicBrainz identities are resolved against the
catalog only when candidates enter recommendation processing.

## Cache and privacy

MusicBrainz, AcousticBrainz, and Deezer results, including empty responses, are
cached for one week. Older MusicBrainz results may be used during a provider
outage. **Clear metadata cache** stops active generation and removes provider
responses while retaining the offline index, models, saved playlists, taste
data, and derived audio analysis.

Provider requests contain public artist, release, recording, or recording-ID
data. They do not include the full playlist prompt or listening history. Preview
audio is temporary and is removed after analysis.

## Recommendation boundaries

MusicBrainz metadata proposes and identifies candidates. Catalog membership
establishes track identity but does not prove musical suitability. Genre tags,
dates, durations, ISRCs, and aliases are retained as their specific evidence
types. Acoustic or preview analysis must independently support requested sound,
mood, instrumentation, vocal, energy, or transition properties.

Unknown or conflicting evidence remains explicit. Essential criteria and hard
exclusions still apply, so a request can return a partial result when the
available evidence cannot justify enough tracks.

## Verification

Provider tests use local HTTP fixtures and temporary databases. They cover
offline-first lookup, online fallback, cache expiry, negative-cache reuse,
pagination, bounded request budgets, catalog identity matching, cancellation,
and behavior when the offline index is unavailable. Normal regression tests do
not contact live providers.
