# Optional ListenBrainz discovery connection

Settings → ListenBrainz accepts a user token from your
[ListenBrainz profile](https://listenbrainz.org/profile/). Connecting validates
that token before replacing the current connection. The connection supplies
aggregate discovery metadata: top recordings and recording-mapping proposals.
It does not read listening history or submit listens.

Tokens are stored with the operating system credential service (Secret Service
on Linux, Keychain on macOS, Credential Manager on Windows), through
[go-keyring v0.2.6](https://github.com/zalando/go-keyring/tree/v0.2.6). If that
service is unavailable, Settings identifies the connection as session-only.
Tokens never appear in settings responses, saved playlists, exports, or logs.
A non-secret connection marker in the application profile prevents a failed
credential deletion or replacement from reconnecting an old token after restart.
If deletion fails, the credential remains disabled and Settings offers retry.

Top recordings are popularity-ranked discovery suggestions, not evidence that a
song meets a musical description. The client validates recording and artist
MBIDs, retains compound artist credits, and limits the returned list locally to
100 records. The upstream endpoint has no assumed `count` parameter. Public
radio suggestions remain the fallback after missing or failed authentication.

Authenticated metadata lookup returns an untrusted mapping proposal. The
existing recording resolver must independently establish catalog recording and
performer identity before a proposed mapping can provide a seed. Similar artist
and title strings do not authenticate a recording or version.

All requests use HTTPS with redirects and cookies disabled, the shared
one-request-per-second limiter, bounded responses, and caller cancellation.
Top-recording and mapping responses use bounded ten-minute session caches.
Requests never include the original playlist prompt. Generation's existing
preparation deadline also bounds optional online discovery.

Reference contracts:

- [ListenBrainz API requirements](https://listenbrainz.readthedocs.io/en/latest/users/api/index.html)
- [Authenticated top-recording handler](https://github.com/metabrainz/listenbrainz-server/blob/master/listenbrainz/webserver/views/popularity_api.py)
- [Metadata mapping API](https://listenbrainz.readthedocs.io/en/latest/users/api/metadata.html)

Verification uses synthetic responses and fake credential stores, including
failed persistence/removal and cancellation. Linux rendered checks exercise
connect, replace, disconnect, and cancellation in both themes. Cross-compiling
the credential tests for Windows and macOS does not establish native keychain
behavior; those integrations still require validation on their respective hosts.
