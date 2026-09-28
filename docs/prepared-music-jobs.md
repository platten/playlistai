# Prepared music jobs

The offline `musicgraph` command now prepares catalog-driven discovery data in
resumable batches and attaches validated specialist predictions to indexed
paipacks. The desktop reads those artifacts locally. These mechanics do not
claim that a broad public audio corpus has already been prepared or that more
returned tracks establish musical quality.

## Catalog identity inventory and graph preparation

Export the complete identity denominator from a verified paipack. Source and
license fields are mandatory producer declarations; the tool does not infer
redistribution permission from a downloadable file.

```sh
go run ./cmd/musicgraph inventory \
  -input public-catalog.paipack -output catalog-identities.json \
  -source 'your source URL or identifier' -license 'applicable source terms'

go run ./cmd/musicgraph resume \
  -input catalog-identities.json -state preparation-state \
  -output public-graph.json -batch-size 16 -max-artists 1000 -timeout 1h
```

The inventory retains exact track/recording/performer identifiers, source genre
tags, and actual CLAP/MERT/classifier availability. It contains no audio, source
paths or listening history. All eligible catalog artists are ordered once by
round-robin across source-genre strata, starting with the least populated strata.
The order is deterministic and independent of prompts and popularity. Genre
strata are coverage labels; they do not establish musical requirements.

`plan.json` binds the catalog archive SHA256, full inventory digest, source
terms, seed order and batch size. Each completed batch has an immutable checkpoint
with its seed IDs, graph projection and SHA256. Rerunning the same command with a
new output path reuses completed batches. A provider failure or cancellation
does not checkpoint a partially completed batch. Invalid/unavailable discovery
responses are retained as explicit omissions, as in the existing graph contract;
use a new job directory for an intentional refreshed preparation. Changed inputs
or corrupt checkpoints fail closed. An OS lock excludes concurrent writers and
is released automatically after process death.

Current jobs use `prepared-music-job/v2` and merge policy
`quarantine-conflicting-recordings/v1`. If separate validated batches disagree
about a recording's performer identities, the merged artifact excludes that
recording and every top/neighbor recording reference to it. Artist relationships
remain intact. Later batches cannot reintroduce the identity. Clean recordings
remain available; no performer set is chosen or combined to resolve ambiguity.
The receipt reports every quarantined identity in its total, with at most 64
deterministically ordered examples and four source observations per example.
Examples retain exact artist sets, batch numbers, snapshot SHA256 and provider
response SHA256; raw checkpoints retain the complete provenance.

Version 1 checkpoints are never reinterpreted in place. Migrate them explicitly
into a separate v2 state using the same inventory, seed budget and batch size:

```sh
go run ./cmd/musicgraph resume \
  -input catalog-identities.json -state preparation-state-v2 \
  -reuse-state preparation-state-v1 -output public-graph-v2.json \
  -batch-size 16 -max-artists 1000 -timeout 1h
```

Migration validates the v1 plan and each imported checkpoint, copies their exact
bytes, and binds the old plan SHA256 in the new plan and receipt. It never changes
the source directory. Future v2 resumptions can omit `-reuse-state`; copied
checkpoints remain usable even when the original directory is unavailable.

The existing graph bounds still apply: at most 1,000 artists per request batch,
64 MiB per final snapshot, and at most one hour per invocation. Budget larger
catalog campaigns into bounded snapshots; a full unbounded catalog crawl is not
performed implicitly. API calls use the existing cancellable client, User-Agent,
rate-limit headers and one-request-per-second floor from the
[ListenBrainz API rules](https://listenbrainz.readthedocs.io/en/latest/users/api/index.html).
Graph source receipts preserve endpoint, response digest, time and
[ListenBrainz CC0 terms](https://listenbrainz.org/data/).

The receipt supplies the expected SHA256 for independent coverage reporting:

```sh
go run ./cmd/musicgraph coverage \
  -input public-graph.json -sha256 GRAPH_SHA256 \
  -catalog catalog-identities.json > coverage.json
```

Coverage uses every inventory row, with separate unique-recording and track
counts. Missing IDs stay missing. Only exact recording MBIDs join; known
conflicting performer credits exclude the row. Display-name similarity never
creates identity. Reports show catalog/graph SHA256, genre strata and existing
audio-feature availability, without confusing those with musical accuracy.

## Specialist predictions in paipacks

The [Discogs-EffNet preparation tool](../python/prepare_discogs_effnet.py)
produces JSONL with an exact `catalogSha256`, `trackId`, `preparationKey`, and
`classifierEvidence` list. Its audio source manifest must already identify the
catalog recording and permitted source; names are not resolved speculatively.

```sh
go run ./cmd/musicgraph enrich-pack \
  -input public-catalog.paipack -sha256 CATALOG_ARCHIVE_SHA256 \
  -classifiers evidence.jsonl -output prepared-classifiers.paipack \
  -installed-bytes 0
```

The command verifies the complete input pack, exact source archive hash and track
IDs. Unknown or duplicate IDs, incorrect source hashes, invalid scores, missing
model/source terms, overlapping coverage, and coverage beyond a known recording
duration prevent publication. Existing metadata, MERT/CLAP vectors, fitted
resources and optional evidence are streamed into a new pack. Existing offline
index builders then produce installable indexed version 8. The source and any
existing output remain unchanged on failure; outputs must be new paths.

An optional `classifier_evidence` SQLite table stores bounded per-track JSON.
The metadata payload SHA256 and semantic pack ID bind all class scores, ordered
labels, encoder/head identities, preprocessing, runtime, source terms, audio
digest and observed intervals. `coverage.classifier` is validated against the
table and distinguishes classifier-bearing packs for installed-size accounting.
Older v5–v8 packs without that table/count remain readable and report unknown
classifier evidence. Older applications with strict manifest readers may require
an update before importing a classifier-bearing pack.

All heads sharing an encoder remain one evidence family. Missing labels remain
unknown. Uncalibrated predictions are only ranking estimates and never satisfy
strict musical requirements. The combiner preserves later duplicate-recording
evidence, deduplicates identical observations and reports conflicting scores,
provenance or incompatible retained duration instead of silently replacing it.

## Additional installed-data ceiling

The ceiling is exactly **10,000,000,000 bytes**. Desktop graph imports/downloads,
discovery release activation (local, hosted and multipart), and classified local
library imports share one cross-process publication lock. The check runs before
the active pointer changes and includes verified staged generations, retained
classifier-bearing pack generations, retained graph snapshots, and assets in the
dedicated `music-classifiers` / `prepared-music-packs` directories. A failed check
keeps the previous active data and removes the rejected stage.

The complete installed size of a classifier-bearing pack counts, including
metadata, vectors and extracted indexes. Existing unclassified base catalogs,
CLAP/MERT installations and unclassified personal libraries are outside this
additional-data budget. Pack accounting reads manifests only; it does not inspect
private library tags or profiles. Temporary extraction/download space is governed
by the existing archive bounds and disk checks, separately from the installed
ceiling. A retained pin can therefore prevent replacement until space is freed.

Offline `resume` and `enrich-pack` also enforce this ceiling using the operator's
`-installed-bytes` declaration plus their output. `enrich-pack` applies conservative
expansion bounds while building and checks final installed members, excluding the
transient `indexes.tar`. It does not install a desktop pack or infer free space
in another machine's installation. Offline specialist preparation takes explicit
model paths. The desktop wizard and Settings separately download the verified
native Discogs-EffNet model for local preview analysis.

## Executed bounded preparation

The [2026-09-27 receipt](data/prepared-music-job-validation-2026-09-27.json)
records an actual public job with 16 catalog-stratified artists, selected from all
252,516 installed catalog rows across 547 genre strata. The installed metadata
SQLite SHA256 was verified before an identity-only, read-only export. The source
corpus's redistribution terms were not established, so its inventory remains
outside the repository and was not published.

The job produced 220 graph artists and 2,397 recordings in 2,166,805 bytes. It
joined 266 of 176,849 unique catalog recording IDs (387 track rows); two
conflicting performer rows were excluded and 34,650 rows lacked recording IDs.
Two seed artists had omitted discovery responses. Preparation took 24.34 seconds
from plan publication to graph publication. A resumed `go run` invocation took
1.20 seconds, reused all four checkpoints and produced identical bytes. These
timings exclude a musical-quality evaluation and do not measure desktop playlist
generation; the initial duration also excludes inventory export and Go startup.

Artifacts are under `/tmp/playlist-ai-prepared-data-validation/`; nothing was
activated in the user's installation. No audio/classifier evidence was added to
that catalog. This small job validates the path and exposes the remaining
coverage gap; it is not a broad-data release or a quality claim.

A subsequent 128-artist job used the same full catalog inventory, separate state,
16 artists per checkpoint, and a four-minute acquisition deadline. It stopped
after **80.86 seconds** because public provider responses assigned incompatible
performer identities to one recording MBID across the third and fourth batches.
Both responses supplied a different single artist ID. Identity checks remained
unchanged; no merged graph was published and no acquisition retry was made.

Four completed checkpoints preserve 64 planned seed artists, including two
omitted discovery responses, in 9,945,261 bytes. Each checkpoint was then
validated and measured separately with the existing offline CLI:

| Checkpoint | Recording suggestions | Exact catalog recordings joined | Catalog track rows joined |
| --- | ---: | ---: | ---: |
| 1 | 2,743 | 332 | 454 |
| 2 | 2,801 | 360 | 495 |
| 3 | 2,779 | 265 | 352 |
| 4 | 2,697 | 323 | 452 |

The denominator remains 176,849 unique recording IDs and 252,516 track rows for
every checkpoint. These results overlap and must not be added. The receipt's
`extension128` section records each snapshot SHA256, sizes, coverage, timing,
exact conflicting identifiers, and the incomplete outcome. The original v1
attempt has no merged snapshot or combined coverage result. The earlier complete
16-artist job remains the successful initial resume control.

The acquisition used public network services from 23:29:50.736Z to 23:31:11.592Z
on 2026-09-27. The native generation evaluator was notified because overlapping
provider activity can affect timing interpretation. Subsequent inspection was
offline. All extension artifacts remain under
`/tmp/playlist-ai-prepared-data-validation/extended-128/`; no models, audio,
application settings or active datasets were changed. Public-provider identity
disagreement blocked the original v1 job and motivated the explicit v2 quarantine
policy described above; the unchanged v1 result remains in the receipt.

The subsequent **v2 continuation completed all 128 seed artists**. It explicitly
imported the four original checkpoints unchanged, acquired the remaining four
batches after native timing runs finished, and quarantined nine conflicting
recording identities. All nine are absent from the final recording rows and
every top/neighbor recording reference. Three seed artists retained omitted
discovery responses; no performer credits were selected or combined to fill gaps.

The final graph contains **21,747 recording suggestions and 1,323 artists** in
19,455,005 bytes, with SHA256
`895ebc7d3dcf6cdf607b57cc029cc40aa5c17ffced79d1e620f5759a36310497`.
Against the unchanged full catalog denominator it joins **2,245 unique recording
identities and 3,005 track rows**; 69 rows with known conflicting performer
credits are excluded. It covers 908 of the catalog's 14,671 artist identities.
The 34,650 catalog rows missing recording IDs remain unknown.

Continuation took 81.41 seconds; the original partial invocation plus continuation
totaled 162.27 seconds. Each invocation retained the four-minute limit. A complete
offline resume took 1.16 seconds, reused all eight checkpoints, and reproduced
identical graph bytes with HTTP(S) proxy destinations restricted to loopback.
The `extension128V2` receipt includes quarantine provenance, original checkpoint
hashes, exact coverage and verification results. Its artifacts remain under
`/tmp/playlist-ai-prepared-data-validation/extended-128-v2/` without activation.
This is a larger bounded public metadata sample; no new audio coverage or musical
quality improvement is established.
