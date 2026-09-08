# Music metadata cache and fallback

## Settings

**Settings → Music metadata → Clear metadata cache** removes cached MusicBrainz,
Discogs and Deezer metadata and parsed-intent reuse. It stops active generation;
an already-running response cannot refill the cleared cache. Saved playlists,
their evidence snapshots, taste data, audio features, models and credentials are
kept. Audio features have their own **Clear analysis** button.

To enable optional Discogs fallback, create a personal token in
[Discogs developer settings](https://www.discogs.com/settings/developers), review
the [API terms](https://support.discogs.com/hc/en-us/articles/360009334593-API-Terms-of-Use),
then paste it into Settings and select **Save token**. Saving confirms local
configuration, not remote authentication. **Remove token** disables fallback.
An invalid/revoked token produces a provider error, never an empty successful
search. No application secret or additional runtime is bundled.

The token lives in `<data_dir>/credentials/discogs-token`, not `prefs.json`.
It is plaintext: directory/file permissions are 0700/0600 on POSIX; Windows
relies on account-directory ACLs. It is not an encrypted keychain. The token is
sent in the Authorization header, never query URLs, saved playlists or cache
keys. Redirects are not followed. Do not share your credentials directory.

## Cache rules

| Provider | Freshness | When unavailable |
| --- | --- | --- |
| MusicBrainz | Always reuse successful responses younger than 7 days, including empty searches | Valid older responses may be used without changing their original timestamp |
| Discogs | Reuse for less than 6 hours | Expired content is not served; purged on startup and subsequent Discogs access |
| Deezer metadata | Existing 24-hour policy | Existing stale-data fallback remains |

Every MusicBrainz endpoint uses the same raw-response cache: recording
enrichment, artist/recording searches, release groups, works, genre pages,
relationships, aliases and pagination. At seven days a response needs refresh;
there is no background refresh of fresh data. Enrichment reinterprets cached
evidence under the current identity/minimum-score rules rather than caching a
permanent matching decision. Cache-only reads remain offline.

SQLite uses the configured `enrich.cache_path`. Without a path, a bounded
256-entry/32 MiB memory cache is used. Keys include the provider origin,
representation/version and full query; parameter ordering is canonicalized,
but Unicode, search text and filters are preserved. Identical simultaneous
queries share a fetch. Canceled calls, HTTP/provider errors, invalid JSON and
unreadable genre responses are not cached as empty results.

Existing hostless cache rows and permanent identity projections are retained
until clearing, but not reused: they cannot establish which mirror or matching
policy produced them. This causes a one-time refresh. History remains readable;
replaying a saved candidate stream does not re-query either provider.

## Fallback boundaries

```mermaid
flowchart TD
    Q[Extracted genre or reference] --> C[MusicBrainz query cache]
    C -->|fresh| M[Reuse response]
    C -->|missing or expired| MB[Rate-limited MusicBrainz]
    MB -->|failure with valid stale data| M
    MB -->|failure without usable data| D[Optional Discogs cache and API]
    D --> R[Bounded release tracklists]
    R --> I[Exact catalog artist and track identity]
    I --> F[Existing musical-fit and exclusion checks]
    F --> O[Playlist or honest partial result]
```

Discogs backs up genre candidate discovery and unavailable artist/album
lookups. A healthy empty or ambiguous MusicBrainz response is not overridden.
Existing Deezer artist/album recovery remains available. Discogs is not a
drop-in replacement for MusicBrainz's genre graph, work/composition dates,
canonical recording IDs or ISRC enrichment; unavailable evidence stays unknown.

Genre discovery tries the complete phrase as a Discogs genre, then as a style
if the genre search is empty. It requests 100 search results per page and can
iterate up to three pages per phrase (at most eight phrases), with a shared
60-release-tracklist budget. Later pages are fetched only after current-page
release candidates and buffered tracks have been consumed. Release IDs are
deduplicated across pages and genres. Seeded sampling and genre interleaving
remain deterministic. It may return fewer tracks; this is not exhaustive
Discogs coverage. Explicit artist/album recovery retains its eight-detail-read
bound within the interactive metadata deadline. Album lookup rejects an
incomplete inspection rather than assuming unchecked results cannot match.
Track credits must match real catalog entries; headings and unknown tracks
are skipped. Release genres never become recording-level genre evidence.
Personalization, hard exclusions, duplicate checks and preview analysis remain
in the normal pipeline. No ranking model or default LLM changes.

All Discogs clients in one app process share a limiter: dispatches are at least
2.4 seconds apart (25/minute), including retries. Retry-After survives subsequent
queries and cache clearing. Queue waits honor cancellation. Search results are
sanitized before caching: no images, marketplace or user data are retained.
Only catalog-owned track identities and source links enter saved discovery
snapshots, not Discogs descriptions. The playlist links to source releases.

Discogs searches are consolidated by release ID before caching, preserving
provider ordering and original pagination counts for ambiguity checks. Genre,
artist and album searches reuse the same cached release tracklist. A mismatched
release ID is rejected before caching, including invalid entries from an older
app version. Concurrent callers share a fetch but receive independent decoded
results; query defaults do not mutate caller-owned search parameters.

MusicBrainz artist pools now target 300 artists using 100-row pages, capped at
five search pages even when a provider repeats results. Candidate discovery
can read up to five 100-record pages per artist and 100 recording pages overall.
Initial artist rotation is preserved; existing buffered tracks are consumed
before artist continuation pages. Per-artist catalog identity maps are reused
across pages. Album identity search also requests 100 results instead of five.
These sizes follow MusicBrainz's documented [search limits and offsets](https://musicbrainz.org/doc/MusicBrainz_API/Search);
Discogs uses its [documented paginated client protocol](https://github.com/discogs/discogs_client/blob/master/discogs_client/models.py).

All pages use the existing persistent response cache and shared keep-alive
HTTP transport. No speculative background fetch or parallel provider burst is
introduced. Fresh MusicBrainz and Discogs pages retain their one-week and
six-hour policies respectively; changing the page size deliberately creates a
different query key, while existing release-detail entries remain reusable.
Larger cold searches can take longer under the existing rate limits, but stop
when generation has enough eligible tracks or is canceled.

## Validation and limitations

Executed deterministic tests cover all MusicBrainz endpoint families, week
boundaries, persistence, policy reinterpretation, malformed responses,
coalescing and clearing while requests run. The eight-endpoint cache fixture
made **8 cold requests, 0 additional warm requests, and 8 refresh requests**
after expiry. These are request-count tests, not network latency benchmarks.

Discogs fixtures cover authorization placement, token save/load/removal,
six-hour expiry, sanitization, no fallback on a healthy empty MusicBrainz result,
real catalog identity, exclusions and offline history replay. A virtual-clock
test dispatched 27 attempts (including a retry), each at least 2.4 seconds
apart; the 26th could not start before one minute. Separate tests cover
cancellation and a 120-second Retry-After across cache clearing.

Additional consolidation regressions passed: two different searches sharing
one release made three total requests, reopening SQLite made zero additional
requests, and 20 concurrent identical searches made one request. Tests also
cover independent returned data, incorrect release IDs (live and previously
cached), and complete album searches containing duplicate hits.

Larger-batch fixture results: 300 MusicBrainz artists required three requests
over **one TCP connection**, then zero additional warm requests. MusicBrainz
recording continuation found a match beyond row 100 with three total requests
(artist pool plus two recording pages). Discogs two-page iteration required
four requests (two search pages, two distinct releases), then zero additional
warm Discogs requests. Both iterators used buffered tracks before requesting a
continuation. Repeated Discogs hits were read once across three bounded pages.
These are deterministic HTTP/clock fixtures, not live catalog coverage tests.

The complete `./scripts/test.sh` gate passed: binding generation, frontend
typecheck/build, Go vet, pure-Go core compilation, race-enabled Go tests,
shell checks and lint (zero issues). The sandbox initially blocked pnpm's
database and test sockets; rerunning with approved permissions resolved both.

Reproduce with `go test ./internal/enrich/musicbrainz ./internal/bridge` and
`./scripts/test.sh`. The rendered UI fixture now checks token controls, clear
confirmation, disabled-in-flight behavior and errors. Its JavaScript syntax
check passed; the browser smoke run was not executed because Playwright and a
Chromium executable were not available in this environment.

No authenticated live Discogs search was executed: no
personal token was supplied. Fixtures prove lifecycle/identity behavior, not
live provider coverage or musical recommendation quality.

Primary references: [official client search examples](https://github.com/discogs/discogs_client/blob/master/docs/quickstart.md),
[official token documentation](https://github.com/discogs/discogs_client/blob/master/docs/authentication.md),
[Discogs API terms](https://support.discogs.com/hc/en-us/articles/360009334593-API-Terms-of-Use).
The developer portal returned HTTP 403 during inspection; accessible official
client documentation and API terms were used instead.
