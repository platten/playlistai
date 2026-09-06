# python

Build-time tooling. Not shipped with the app; needs only the Python standard
library (`convert_pickles.py` and `fetch_pickles.py` additionally need the
packages in `requirements.txt`: `numpy` and `gdown`). Semantic-pilot tools use
the separate, optional `semantic-requirements.txt` environment.

| script | purpose |
|---|---|
| `catalogfmt.py` | Shared on-disk catalog format: `vectors.i8` header + writer, the SQLite schema, and `normalize_search()` (reproduced byte-for-byte in `internal/catalog/search.go`). Both scripts below go through it. |
| `fetch_pickles.py` | Download Deej-AI's four pickles from Google Drive (by the file IDs `deej-ai.online-app/scripts/download.py` uses) and sanity-check each one. See `docs/CATALOG.md`. |
| `convert_pickles.py` | Convert `spotifytovec.p` / `tracktovec.p` / `spotify_tracks.p` / `spotify_urls.p` into `vectors.i8` + `catalog.sqlite` + `catalog-manifest.json`. |
| `make_test_catalog.py` | Write a small deterministic synthetic catalog in the same format. |
| `parity_playlist.py` | Stdlib-only reimplementation of upstream `backend/deejai.py` (`make_playlist` / `most_similar` / `join_the_dots`, `noise=0`) run over `internal/catalog/testdata` → golden playlist fixtures under `internal/reco/deejai/testdata/golden/` for the Go parity test. |
| `build_semantic_sidecar.py` | Validate grounded JSONL against a real catalog, embed descriptions with an already-local model, and write the bounded semantic sidecar plus coverage report. |
| `embed_semantic_query.py` | Encode one query using that same already-local model; network downloads are disabled. |

## Regenerating the test fixtures

`internal/catalog/testdata/{vectors.i8,catalog.sqlite,catalog-manifest.json}` is
committed so `go test ./...` is self-contained. To regenerate:

```bash
cd python && python3 make_test_catalog.py --out ../internal/catalog/testdata
```
