# python

Build-time tooling. Not shipped with the app.

- `convert_pickles.py` — [M3] convert Deej-AI's `spotifytovec.p` / `tracktovec.p` /
  `spotify_tracks.p` / `spotify_urls.p` into L2-normalized int8 vectors + a
  SQLite metadata db + `models/catalog-manifest.json`.
- `parity_playlist.py` — [M5] run upstream `backend/deejai.py` on pinned
  seeds/params to produce golden playlist fixtures for the Go port's tests.
