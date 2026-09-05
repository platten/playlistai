# The embedding catalog

Playlist AI's recommendation catalog is a compressed **~210 MB**
`catalog.tar.zst` (~957k tracks, derived from the Deej-AI pre-computed
dataset). It is **not** in the repo and **not** in the installers — the app
**downloads it on first launch** and decompresses it into the data dir.

- **Where it's hosted**: `config.Default()` sets `catalog.archive_url` to a hosted `catalog.tar.zst` (Cloudflare R2), plus a pinned `catalog.archive_size` and
  `catalog.archive_sha256`. Override any of these in a TOML config to
  self-host (clear the size/hash if you point at a different build).
- **First-run wizard**: the first-run wizard's catalog step calls
  `DownloadCatalog` automatically (no button), showing a progress popup:
  `internal/dataset.DownloadArchive` fetches the archive (resumable HTTP
  range, size + SHA-256 verified) to `<data dir>/catalog.tar.zst`, then
  `internal/dataset.Unpack` decompresses it into `catalog.dir` and the
  archive is deleted. `Container.EnsureCatalog` drives this — see its doc
  comment for the full source-precedence order.

Nothing to configure, no account. A pre-staged local archive
(`catalog.bundle_path`, or `catalog.tar.zst` next to the executable) and
`catalog.manifest_url` also still work and take precedence — see "Other ways
to point the app at a catalog".

## What "the catalog" is

Two files, read entirely by `internal/catalog`, packed together into
`catalog.tar.zst` (tar + zstd `SpeedBestCompression`) alongside the
`catalog-manifest.json` that describes them:

- `vectors.i8` — one row per track, two L2-normalized 100-dim embedding
  spaces (Deej-AI's audio-content space and its playlist-co-occurrence
  space) quantized to int8. Format: `python/catalogfmt.py`'s `write_catalog`
  (32-byte header + `PAIVEC1` magic), mirrored in Go by `internal/catalog`.
- `catalog.sqlite` — `id`, `artist`, `title`, `preview` (a bundled Spotify CDN
  URL, often empty), and a normalized `search` column, one row per track in
  the same order as `vectors.i8`.

Both come from converting **Deej-AI's pre-computed pickles**
(`spotifytovec.p`, `tracktovec.p`, `spotify_tracks.p`, `spotify_urls.p`,
~1.4 GB) — see [teticio/Deej-AI](https://github.com/teticio/Deej-AI) and
[teticio/deej-ai.online-app](https://github.com/teticio/deej-ai.online-app)'s
`scripts/download.py` for where those live (Google Drive).

## Regenerating and re-hosting the catalog

Do this when the upstream dataset changes, or to rebuild from scratch. The
output, `build/catalog-dist/catalog.tar.zst`, is git-ignored — you upload it
to a host and point `catalog.archive_url` at it.

1. **Get the pickles.** `python/fetch_pickles.py` downloads all four from
   Google Drive by their known file IDs and sanity-checks each one (Drive
   serves an HTML interstitial instead of the file for large/popular
   downloads, or quota-blocks them outright — the script catches both and
   tells you the manual-download URL rather than leaving a corrupt `.p` file
   behind):
   ```sh
   python3 -m venv .venv && .venv/bin/pip install -r python/requirements.txt
   .venv/bin/python python/fetch_pickles.py --out ./pickles
   ```
   If a file fails automated fetch (Drive quota-exceeded is the most likely
   failure), the script prints `https://drive.google.com/uc?id=<ID>` — open
   that in a browser and save the result as `<name>.p` in the same directory.

2. **Convert them** (needs `numpy`; everything else here is stdlib):
   ```sh
   cd python
   python3 convert_pickles.py \
     --pickles ../pickles \
     --out ../build/catalog \
     --manifest ../build/catalog/catalog-manifest.json
   ```
   This writes `build/catalog/{vectors.i8,catalog.sqlite,catalog-manifest.json}`
   and prints each file's exact size + SHA-256 (also embedded in the
   manifest; `internal/dataset.Unpack` verifies extracted files against it).
   `--limit N` caps the track count if you want a smaller/faster test catalog
   first.

3. **Compress it** (tar + zstd `SpeedBestCompression`, ~50% smaller):
   ```sh
   go run ./cmd/catalogpack -in build/catalog -out build/catalog-dist/catalog.tar.zst
   ```
   It prints the archive's size and SHA-256.

4. **Upload** `build/catalog-dist/catalog.tar.zst` to a host that supports
   HTTP range requests (Google Drive's `drive.usercontent.google.com/download`
   form, an S3/R2 bucket, GitHub Releases, ...). Update `config.Default()`'s
   `catalog.archive_url` / `catalog.archive_size` / `catalog.archive_sha256`
   in `internal/config/config.go` to match (or ship a TOML config that does).

Both `build/catalog/` and `build/catalog-dist/` are git-ignored — nothing
about the dataset is committed.

## Other ways to point the app at a catalog

`EnsureCatalog` tries these in order (first hit wins):

1. **A staged local archive** — `catalog.tar.zst` next to the executable, or
   wherever `catalog.bundle_path` points. Decompressed, no network. Handy for
   testing the unpack path without a download.
2. **`catalog.archive_url`** — the default (a hosted archive). Download + verify +
   decompress.
3. **`catalog.manifest_url`** — a hosted `catalog-manifest.json` listing the
   two raw files (`vectors.i8` + `catalog.sqlite`) instead of one archive.
   Pass `convert_pickles.py --base-url` if the manifest lives apart from the
   blobs.
4. **`catalog.dir`** already holding `vectors.i8` + `catalog.sqlite` — no
   setup at all. Useful for dev against `build/catalog/` directly:
   ```toml
   [catalog]
   dir = "build/catalog"
   ```

## Licensing note

The catalog is derivative of Deej-AI's GPL-3.0-licensed model + data, and the
project redistributes it (from the hosted archive). `NOTICE` documents the
attribution and written-offer-of-source requirement that implies — keep it
accurate if you fork and re-host.

## Verifying it worked

`internal/catalog` / `internal/dataset` tests run against tiny synthetic
fixtures (`python3 make_test_catalog.py`, committed under
`internal/*/testdata/`) and don't need the real catalog. To check a
regenerated one end to end:

```sh
# from the repo root, with build/catalog/ populated per step 2 above
cat > /tmp/playlistai-dev.toml <<'EOF'
[catalog]
dir = "build/catalog"
EOF
PLAYLISTAI_CONFIG=/tmp/playlistai-dev.toml go run .
```
then open the Catalog screen and search for something you know is in the
Deej-AI dataset (e.g. "Justice"). To exercise the download+unpack path
instead, leave `catalog.dir` at its default and let first launch fetch from
`catalog.archive_url`, or point `catalog.bundle_path` at a
`catalog.tar.zst` you built with `catalogpack`.
