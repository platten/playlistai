# Self-hosting the embedding catalog

**This project does not ship, embed, or host the recommendation catalog.**
`config.Default()` leaves `catalog.manifest_url` empty, so a fresh install has
no catalog and no way to get one until an operator (you) builds and hosts one
and points the app at it. The plumbing for all of this exists and is tested —
what's missing is the ~1M-track dataset itself, which the app was designed
around but which nobody has run end-to-end against real data yet (see
`git log` / `docs/ARCHITECTURE.md` milestone 3 for the wiring; this doc is
about the one manual step that was never finished).

## What "the catalog" is

Two things, read entirely by `internal/catalog`:

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
`scripts/download.py` for where those live (Google Drive; not fetched by this
repo, and not linked here because that link moves — check the source repos
for the current one).

## Step by step

1. **Get the pickles.** Download the four `.p` files above into one directory
   (e.g. `./model/`).

2. **Convert them** (needs `numpy`; everything else here is stdlib):
   ```sh
   cd python
   python3 convert_pickles.py \
     --pickles ./model \
     --out ../build/catalog \
     --manifest ../build/catalog/catalog-manifest.json
   ```
   This writes `build/catalog/{vectors.i8,catalog.sqlite,catalog-manifest.json}`
   and prints each file's exact size + SHA-256 (also embedded in the
   manifest, so the app's downloader verifies them automatically — same
   mechanism as the model-hash pinning in `internal/intent/modelmgr`).
   `--limit N` caps the track count if you want a smaller/faster test catalog
   first.

3. **Host all three files together**, at any URL the app can reach —
   GitHub Releases (works well: this repo already publishes installers there,
   and Release assets support files up to 2 GB), a Cloudflare R2 / S3 bucket,
   or your own server. Upload `catalog-manifest.json`, `vectors.i8`, and
   `catalog.sqlite` to the *same* location: `dataset.Manifest` resolves each
   file's URL relative to wherever the manifest itself was fetched from when
   the manifest doesn't set an explicit per-file `url` (skip `--base-url` in
   step 2 for this — the default). If you'd rather host the manifest
   separately from the blobs (e.g. a versioned manifest in this repo pointing
   at blobs on R2), pass `--base-url https://your-host/path/` instead and
   it'll write absolute URLs into the manifest.

4. **Point the app at it.** Two ways:
   - **Download on demand** (matches the original "first launch" design):
     set `catalog.manifest_url` to the hosted manifest's URL in a TOML config,
     then run with `PLAYLISTAI_CONFIG=/path/to/config.toml`. The Catalog
     screen's "Download catalog" button (and the first-run wizard's catalog
     step) will then actually work instead of showing "no catalog source
     configured."
   - **Pre-populate locally** (no download UI at all, useful for your own
     testing): just set `catalog.dir` to a directory that already has
     `vectors.i8` + `catalog.sqlite` in it — `manifest_url` isn't needed in
     this case.

   Example `config.toml`:
   ```toml
   [catalog]
   manifest_url = "https://github.com/<you>/<repo>/releases/download/catalog-v1/catalog-manifest.json"
   ```

## Licensing note

The converted catalog is derivative of Deej-AI's GPL-3.0-licensed model +
data. If you host and distribute it, `NOTICE` already documents the
attribution and source-offer requirement this implies — read that before
publishing a `manifest_url` anyone else will use.

## Verifying it worked

`internal/catalog`'s tests run against a tiny synthetic fixture
(`python3 make_test_catalog.py`, already committed under
`internal/catalog/testdata/`) and don't need any of the above — they pass
today regardless of whether a real catalog exists anywhere. To check your
real conversion:

```sh
# from the repo root, with build/catalog/ populated per step 2 above
cat > /tmp/playlistai-dev.toml <<'EOF'
[catalog]
dir = "build/catalog"
EOF
PLAYLISTAI_CONFIG=/tmp/playlistai-dev.toml go run .
```
then open the Catalog screen and search for something you know is in the
Deej-AI dataset (e.g. "Justice").
