# python

Build-time tooling. Not shipped with the app; needs only the Python standard
library (`convert_pickles.py` additionally needs `numpy` to read the pickles).

| script | purpose |
|---|---|
| `catalogfmt.py` | Shared on-disk catalog format: `vectors.i8` header + writer, the SQLite schema, and `normalize_search()` (reproduced byte-for-byte in `internal/catalog/search.go`). Both scripts below go through it. |
| `convert_pickles.py` | Convert Deej-AI's `spotifytovec.p` / `tracktovec.p` / `spotify_tracks.p` / `spotify_urls.p` into `vectors.i8` + `catalog.sqlite` + `models/catalog-manifest.json`. The pickles live only on Google Drive (~1.4 GB) and are not fetched here. |
| `make_test_catalog.py` | Write a small deterministic synthetic catalog in the same format. |
| `parity_playlist.py` | [M5] run upstream `backend/deejai.py` to produce golden playlist fixtures for the Go port. |

## Regenerating the test fixtures

`internal/catalog/testdata/{vectors.i8,catalog.sqlite,catalog-manifest.json}` is
committed so `go test ./...` is self-contained. To regenerate:

```bash
cd python && python3 make_test_catalog.py --out ../internal/catalog/testdata
```
