# Shared discovery assets

Shared discovery releases provide recording metadata, artist/album profiles,
MERT representations, and DSP measurements without requiring users to scan
their own files. Personal `playlist-indexer` imports remain optional and are
kept separate from shared discovery data. Shared recordings do not establish
that the user owns a local audio file.

The first implementation targets Enhanced hybrid. Artist patterns guide
retrieval; recording-level evidence and existing recommendation checks still
determine suitability. Unknown evidence remains unknown. Neither a matching
artist profile nor a MERT neighbor verifies every requested musical attribute.

Submitted Enhanced hybrid generation can use existing cached MusicBrainz and
Wikidata providers for external discovery. Only bounded artist/recording queries
are handed off; source file paths, raw prompts, and private listening history
are not sent by this feature. The extra artist-context stage has an eight-second,
eight-request budget, considers at most four artist names, and follows typed
influence links only after reciprocal MusicBrainz/Wikidata identity checks.
Provider unavailability leaves local retrieval available. A valid MusicBrainz
recording can be registered without a preview; absence of audio remains explicit.

Enhanced generation considers a bounded pack-aware local candidate batch before
waiting on provider discovery. Shared recording evidence participates in the
existing eligibility, ranking, and diversity checks; it does not bypass them.
Online discovery still supplies additional candidates when needed. Recording
registration does not resolve previews for an entire provider page: it retains
the authoritative MusicBrainz identity and metadata, while the audio service
corroborates preview identity when checking a candidate. Available queued
candidates can use the existing bounded parallel audio workers even while the
provider stream remains open. Missing previews remain unknown evidence.

Embedded multi-value tags retain their separate values. Original album date
ranges and album-balanced artist tag distributions describe the sampled corpus,
not popularity trends or verified artist-wide moods. Exact recording identities
can link existing catalog references to pack evidence; name similarity alone
does not transfer MERT, DSP, or recording tags. Saved discovery streams retain
their profiles and source revisions; incompatible saved generations require a
fresh generation rather than silently querying a newer release.
The recommendation algorithm version advances to `multichannel/v34`; saved
history remains readable, while deterministic replay still requires matching
algorithm and catalog generations.
The additive intent contract advances to version 11 so older saved results do
not claim that the new explicit other-artist output requirement was enforced.

## Required setup, local override, and offline downloads

The default wizard source is the
[hosted paipack manifest](https://pub-233adf724b7e476db67cf787cd301c9e.r2.dev/paipack/manifest.json).
New installations and existing installations missing discovery data must complete
this setup step. An already validated installation remains usable offline;
startup does not require a live manifest check.

The hosted manifest uses the existing version-1 multipart `modelpack` transport.
The installer reads the manifest for each explicit install/update, resolves its
listed parts relative to the manifest's directory, and verifies lengths and
SHA-256 hashes. It reconstructs the ordered tar+Zstandard stream, verifies the
listed extracted `.paipack` files, validates their pack schema and embedded
prebuilt search/profile indexes, and activates without building local indexes.
New local and hosted imports require an indexed version-8 paipack; re-export
older packs from `playlist-indexer` state. Existing installed legacy releases
with valid indexes remain usable.
Part counts, filenames, and the bundle's display name are not hard-coded.
Updates are detected by manifest content, even when its name stays unchanged.
One immutable manifest snapshot governs an entire download; publication changes
mid-download cannot silently mix generations.

In **Settings → Music discovery data**, select a local `.paipack` to override the
hosted collection. The app validates and copies it into managed storage; it does
not modify the selected file or depend on that file remaining at its original
location. A successful override satisfies the required asset and survives
restart. Checking for updates does not replace it. Switching back to the hosted
archive is an explicit action. Failed or canceled replacement keeps the active
collection. This is separate from optional personal-library indexing and imports.

The same card can download the current manifest and all of its listed archive
parts into a new folder for offline storage. It preserves the relative layout
and verifies each part; this operation does not change the active collection.
All parts are fragments of one stream, not independently extractable archives.
Use the existing unpacker to reconstruct an offline download:

```sh
go run ./cmd/modelpack --manifest /path/to/download/manifest.json \
  --cache /path/to/segment-cache --out /path/to/new-unpacked-directory
```

Then choose an extracted indexed version-8 `.paipack` in Settings. An older
hosted archive must be rebuilt and republished before a fresh installation can
use it; the app will not construct missing indexes during setup. Installing
does not curate or refit its representations. Shared discovery
still does not expose paths from the pack as playable local files.

If a custom multipart manifest uses absolute part URLs, the saved original
`manifest.json` remains byte-for-byte intact and an additional
`manifest.offline.json` points to the downloaded local files. Use that offline
manifest with the unpacker. Relative-path manifests, including the default
hosted source, need only `manifest.json`.

## Build and verify a curated release

The offline CLI accepts existing v5 paipacks. Run it from the repository root:

```sh
go run ./cmd/discoverypack audit /path/to/input.paipack
go run ./cmd/discoverypack build \
  --output /path/to/new-release-directory \
  --base-url https://music.example.org/discovery/2026-09 \
  --version 2026-09 \
  --max-tracks 100000 \
  --max-per-album 8 \
  /path/to/input.paipack
go run ./cmd/discoverypack verify /path/to/new-release-directory
```

Replace the example domain with the actual production HTTPS prefix before
distribution. The builder refuses to overwrite an existing output directory.
It never uploads files or calls online enrichment providers. Inputs remain
untouched; extraction and fitting use temporary derivatives.

The builder balances selected tracks by artist, rotating albums within each
artist and limiting tracks per album. It checks all valid recording identifiers
and compatible fingerprints to deduplicate recordings, including across input
packs with the same complete MERT representation contract. Incompatible spaces
remain separate. It preserves metadata-only recordings.

Public output removes local paths, original source identities, private or
nonmusical tag keys, and free-text processing failures. Track IDs are rekeyed;
musical tag multiplicity is retained. The selected corpus gets freshly fitted
metadata TF-IDF/SVD and DSP distributions. Previous cluster assignments and
population statistics are not copied into the new corpus.

New output includes one or more indexed version-8 `.paipack` files and
`manifest.json`. Each pack embeds its own checksummed `discovery.sqlite` with
exact metadata-member binding, recording annotations with embedded-tag
attribution, and artist/album/original-decade distributions. Original-decade
profiles use original-release tags; edition/reissue dates are not substituted.
Conflicting original years remain unknown. Shared catalogs read the embedded
profiles directly and reject mismatched pack bindings. Earlier curated releases
with a separate companion remain readable when already installed.

The manifest identifies immutable release and schema versions, each file's
HTTPS URL, byte size and SHA-256, and each pack's semantic ID and expanded
member size. Limits are:

| Limit | Value |
| --- | ---: |
| Combined indexed-pack download | 6,000,000,000 bytes |
| Earlier curated release download, including companion | 3,000,000,000 bytes |
| Combined expanded pack members | 12,000,000,000 bytes |
| Packs per release | 128 |
| Default selected tracks | 100,000 |
| Maximum configured selected tracks | 500,000 |
| Retained selection metadata per source | 768 MiB |
| Selected vector allocation per source | 512 MiB |
| Combined selected-data accounting | 1 GiB |

Builder memory limits cover accounted track data, not a promised process RSS;
SQLite, fitting, Go runtime, and temporary duplicate/group indexes add overhead.
Oversized inputs fail with an actionable error instead of silently weakening
the selection or raising the limits. Reduce the selected count or split inputs
when necessary.

## Installation and publication

Configure the application with:

```toml
[discovery]
manifest_url = "https://pub-233adf724b7e476db67cf787cd301c9e.r2.dev/paipack/manifest.json"
```

Release builds can instead set
`internal/config.DefaultDiscoveryManifestURL` through the normal Go linker
configuration. The default now uses the user-supplied hosted source. Existing
configuration files without a `[discovery]` section inherit it. Explicit custom
manifest URLs remain supported, including the original whole-file curated
release format above. No personal file scan is required.

Installation uses resumable, size-bounded HTTPS downloads and SHA-256 checks.
Redirects must retain HTTPS. Before downloading, the installer checks available
disk against download bytes plus three times expanded pack bytes plus 256 MiB
of scratch allowance. This is a conservative staging estimate, not a measured
installation footprint. An update also needs space for its existing installed
release while active requests continue reading it.

All pack members, embedded profiles, and retrieval indexes are verified before a
single activation record changes; the desktop does not build indexes. Catalog
leases pin the complete release set;
retired files are removed only after readers release them. A failed pre-commit
update preserves the working release. If the activation rename succeeds but
the subsequent directory sync fails, the manager retains both release sets,
reports uncertain durability, and exposes the newly committed set consistently.
Startup integrity failures produce a repairable status.

Before publishing to R2, complete the licensing and redistribution audit for
the source corpus, embedded tags, fingerprints, model-derived data, and any
future MusicBrainz/Wikidata additions. A local collection's availability does
not establish permission to redistribute its contents. Preserve applicable
source revisions, licenses, and required attribution; the builder does not
perform this audit or grant redistribution rights.

Publish approved parts before publishing the updated manifest. Prefer new part
filenames for each generation so in-flight readers can finish; the application
also detects checksum mismatches if objects are replaced in place. The manifest
URL can remain unchanged. The supplied R2 endpoint is now configured; this work
does not upload or change its objects. Hosting does not establish redistribution
rights or measured musical-quality gains.

## Historical hosted multipart validation on 2026-09-19 (pre-v8)

The actual supplied R2 endpoint was tested with an empty temporary cache:

```sh
PLAYLISTAI_DISCOVERY_MANIFEST_URL=https://pub-233adf724b7e476db67cf787cd301c9e.r2.dev/paipack/manifest.json \
  go test ./internal/discoveryasset -run TestHostedMultipartOptIn -v -count=1
```

The then-current production install path downloaded and verified three parts totaling
541,536,374 bytes, plus a 720-byte manifest. It reconstructed the unchanged
541,522,368-byte source paipack, validated it, built search indexes and a local
companion, and activated/pinned all 67,913 tracks. The operation passed in
6m7.7s on this Linux host (368.03s including test cleanup). Those byte counts
describe manifest/part payloads, not HTTP/TLS overhead. Original metadata index
building dominated the first installation; the wizard reports this stage
explicitly. This historical test predates indexed version-8 packs and does not
validate the current install path. Timing is one observation, not a
cross-platform guarantee.

All installation data and caches were temporary and cleaned after the test;
neither the supplied files nor the application's real user data was changed.
Multipart regressions also cover changed filenames/content under the same
manifest name, failed-checksum rollback, local overrides across restart,
cancellation, source-file mutation, and complete offline export. Browser
fixtures exercised local override, explicit hosted restoration, and saving
archive parts without changing the active source, in both themes at 390/1000 px.
Native picker interaction and native Windows/macOS execution were not tested;
package cross-compilation passed on those targets.

## Historical local corpus validation on 2026-09-19 (pre-v8)

The validated source was a readable v5 pack of 541,522,368 bytes. Audit results
and the latest local curated candidate:

| Coverage | Source | Candidate v2 |
| --- | ---: | ---: |
| Tracks | 67,913 | 39,893 |
| Artist strings | 7,463 | 6,654 |
| Artist/album groups | 13,160 | 11,923 |
| Genre or style tags | 67,050 | 39,301 |
| Mood tags | 26,384 | 15,765 |
| Original-date tags | 64,599 | 38,068 |
| Canonical recording identity | 67,909 | 39,891 |
| MERT vectors | 67,766 | 39,807 |
| DSP records | 67,870 | 39,862 |

These are metadata coverage counts, not verified musical labels. The source
uses 768-dimensional `m-a-p/MERT-v1-95M` sampled-window vectors, model revision
`12af15fef9d0ac838c3f475bfbbf26d2060dd4f5`.

The candidate at that date contained a 292,198,760-byte pack and a 230,047,744-byte companion, totaling
522,246,504 bytes. Pack members expand to 556,270,624 bytes. The earlier v1
candidate was preserved. These artifacts live outside the repository and must
not be committed.

The candidate manifest intentionally uses `https://example.invalid/discovery/v2/`
URLs. It is an unpublished local validation artifact and cannot be downloaded
through the wizard until a real approved release URL is configured.

The opt-in real-corpus check was run as:

```sh
PLAYLISTAI_DISCOVERY_RELEASE=/path/to/discovery-build-v2-20260919 \
  go test ./internal/discoveryasset -run TestRealReleaseOptIn -v -count=1
```

It passed in 53.52 seconds on the available Linux host: full archive/companion
verification, temporary installation, metadata/MERT index building, activation,
shared catalog pinning, companion access, and five bounded metadata-search hits
from 39,893 installed tracks. Only temporary installation derivatives were
cleaned; source and candidate artifacts were preserved. This timing describes
that run and is not a cross-platform performance guarantee.

A follow-up run also exercised artist-profile retrieval: 12 profiles were
returned in 5.000 seconds, with at most 256 supporting recordings and eight
genre/mood values per profile. The complete verify/install/index/search/profile
check passed in 68.66 seconds. This single workload does not establish a latency
target; profile generation cost remains worth measuring on slower desktop hosts.

Targeted race-enabled package tests and `golangci-lint` passed. Windows amd64
and macOS arm64 package cross-compilation passed using `go test -exec=true`;
that did not execute either native desktop host.

The final integrated `./scripts/test.sh` gate passed: shell/release checks,
generated Wails bindings, TypeScript checking, 231 frontend tests, production
frontend build, Go vet, pure-Go compilation, race-enabled Go tests, and lint
(zero issues). The frontend build reports a non-failing chunk-size warning.
`scripts/capture-setup-readiness.mjs` also passed mocked-host wizard interactions
and screenshots in dark/light themes at 390- and 1000-pixel widths, including
keyboard focus and overflow checks. Browser checks do not replace native host
or production-R2 download verification.

## Remaining evaluation work

The current selection is deterministic artist/album balancing, not acoustic
cluster medoid optimization. Original-decade distributions are stored, but
runtime artist-profile retrieval initially selects artist/album distributions
rather than explicit period rows. Broad offline Wikidata relationship
enrichment is not part of the builder.

The installation checks and synthetic regressions establish plumbing,
identity, and failure-path behavior. They do not establish that playlists are
musically better. To assess recommendation quality, compare baseline,
metadata/profile-only, MERT-only, and combined retrieval on held-out prompts
and blind playlist preferences; report uncertainty, constraint/identity
regressions, latency, memory, and provider budgets. No blind musical-quality
improvement has been measured or claimed.

The offline evaluation CLI supports the four production-overlay variants:

```sh
go run ./cmd/recoeval \
  -dataset /path/to/held-out-evaluation.json \
  -catalog /path/to/base-catalog \
  -discovery-state /path/to/app-data/discovery-data \
  -output /path/to/report.json -markdown /path/to/report.md \
  -blind-output /path/to/blind.json -blind-key /path/to/private-key.json
```

This uses an already installed release, never live enrichment providers.
Variants are `discovery_baseline`, `discovery_metadata_only`,
`discovery_mert_only`, and `discovery_combined`; blind export defaults to
baseline versus combined. Identity and eligibility metadata remain available
in all variants so disabling a scoring channel does not bypass constraints.
`-discovery-state` and personal-library `-paipack` comparisons are mutually
exclusive. Keep the blind identity key separate from listener materials.
