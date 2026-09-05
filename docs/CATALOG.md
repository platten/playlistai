# The bundled embedding catalog

Playlist AI ships its recommendation catalog **in the repo**, compressed, at
`build/catalog-dist/catalog.tar.zst` (~210 MB), tracked with **Git LFS**
(`.gitattributes`). ~957k tracks, derived from the Deej-AI pre-computed
dataset.

- **Packaging** (`build/Taskfile.yml`'s `stage:catalog`, a dependency of
  every per-OS packaging task) copies it next to the app binary as
  `bin/catalog.tar.zst`; nfpm / NSIS / the `.app` bundle then include it.
- **First launch**: if the app finds `catalog.tar.zst` beside its executable
  and hasn't decompressed it yet, `internal/dataset.Unpack` extracts it into
  the data dir — a blocking "Decompressing dataset" step
  (`frontend/src/components/CatalogUnpackGate.tsx`), no button, no network,
  typically under a second. `internal/dataset.FindBundledArchive` is what
  locates it; `Container.EnsureCatalog` prefers it over the
  `catalog.manifest_url` network-download path.

Nothing to download, no account, no config for the end user. The download
path (`catalog.manifest_url`) and a pre-populated `catalog.dir` still work
and still take precedence if set — see "Other ways to point the app at a
catalog" below — but the default install needs neither.

**If you cloned without Git LFS**, `build/catalog-dist/catalog.tar.zst` is a
~130-byte text pointer, not the archive. Run `git lfs pull` once. `go test
./...` and `wails3 build` don't care; only `wails3 package` does, and
`stage:catalog` guards against it (it packs `build/catalog/` on the fly if
present, else stages a 0-byte placeholder that the app reads as "no
catalog").

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

## Regenerating the catalog

Do this when the upstream dataset changes, or to rebuild from scratch. The
output is `build/catalog-dist/catalog.tar.zst` — commit it (Git LFS handles
the rest).

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

3. **Compress it** into the committed archive:
   ```sh
   go run ./cmd/catalogpack -in build/catalog -out build/catalog-dist/catalog.tar.zst
   ```

4. **Commit** `build/catalog-dist/catalog.tar.zst` (Git LFS tracks it via
   `.gitattributes`; `git add` + `git commit` as usual, `git push` uploads
   the LFS object).

`build/catalog/` (the intermediate) stays git-ignored — only the compressed
archive is committed.

## Other ways to point the app at a catalog

The bundled archive is the default, but two overrides still work and take
precedence over it:

- **Download on demand** — set `catalog.manifest_url` to a hosted
  `catalog-manifest.json` (with `vectors.i8` + `catalog.sqlite` beside it) in
  a TOML config, then run with `PLAYLISTAI_CONFIG=/path/to/config.toml`. Host
  the three files together with `catalogpack`'s inputs, e.g. on GitHub
  Releases or an S3/R2 bucket; pass `convert_pickles.py --base-url` if the
  manifest lives apart from the blobs.
- **Pre-populate locally** — set `catalog.dir` to a directory that already
  holds `vectors.i8` + `catalog.sqlite`. Useful for dev against
  `build/catalog/` directly:
  ```toml
  [catalog]
  dir = "build/catalog"
  ```
  There's also `catalog.bundle_path` to point at a `catalog.tar.zst`
  somewhere other than beside the executable (mostly for testing the unpack
  path without a real install).

## Licensing note

The catalog is derivative of Deej-AI's GPL-3.0-licensed model + data, and
this repo now redistributes it. `NOTICE` documents the attribution and
written-offer-of-source requirement that implies — keep it accurate if you
fork and re-host.

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
Deej-AI dataset (e.g. "Justice"). To exercise the *bundled* path instead,
point `catalog.bundle_path` at your `build/catalog-dist/catalog.tar.zst`.
