#!/usr/bin/env python3
"""Convert the Deej-AI pre-computed pickles into a Playlist AI catalog.

Inputs (from https://github.com/teticio/deej-ai.online-app -> scripts/download.py,
hosted only on Google Drive; ~1.4 GB total, not fetched by this repo):

  spotifytovec.p   dict[str, np.ndarray(100,)]   audio-content embedding
  tracktovec.p     dict[str, np.ndarray(100,)]   playlist co-occurrence embedding
  spotify_tracks.p dict[str, str]                "Artist - Title"
  spotify_urls.p   dict[str, str]                30s preview URL ("" when absent)

Output: <out_dir>/vectors.i8, <out_dir>/catalog.sqlite, and a
models/catalog-manifest.json describing the pair (sha256 + size) for the
first-launch downloader.

Only the intersection of all four key sets is kept (matches deej-ai.online-app's
install step). Rows are ordered by sorted track id, so the conversion is
deterministic.

Usage:
  python convert_pickles.py --pickles ./model --out ./build/catalog \\
      --manifest ./models/catalog-manifest.json [--base-url https://.../catalog/]
"""

from __future__ import annotations

import argparse
import pickle
import sys
from pathlib import Path

from catalogfmt import Track, iter_common_keys, split_display, write_catalog, write_manifest

PICKLES = {
    "audio": "spotifytovec.p",
    "track": "tracktovec.p",
    "tracks": "spotify_tracks.p",
    "urls": "spotify_urls.p",
}


def _load(path: Path):
    with open(path, "rb") as f:
        return pickle.load(f)


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--pickles", type=Path, required=True, help="dir holding the 4 .p files")
    ap.add_argument("--out", type=Path, required=True, help="output catalog dir")
    ap.add_argument("--manifest", type=Path, required=True, help="catalog-manifest.json path")
    ap.add_argument(
        "--base-url",
        default="",
        help="prefix prepended to each file name in the manifest (where the "
        "files will be hosted for first-launch download)",
    )
    ap.add_argument("--limit", type=int, default=0, help="cap track count (debug)")
    args = ap.parse_args(argv)

    for name in PICKLES.values():
        if not (args.pickles / name).is_file():
            ap.error(f"missing pickle: {args.pickles / name}")

    print("loading pickles ...", file=sys.stderr)
    audio = _load(args.pickles / PICKLES["audio"])
    track = _load(args.pickles / PICKLES["track"])
    names = _load(args.pickles / PICKLES["tracks"])
    urls = _load(args.pickles / PICKLES["urls"])

    keys = list(iter_common_keys(audio, track, names, urls))
    if args.limit:
        keys = keys[: args.limit]
    print(f"{len(keys):,} common tracks", file=sys.stderr)

    def tracks():
        for k in keys:
            artist, title = split_display(names[k])
            yield Track(
                id=k,
                artist=artist,
                title=title,
                preview=urls.get(k, "") or "",
                audio=[float(x) for x in audio[k]],
                track=[float(x) for x in track[k]],
            )

    manifest = write_catalog(
        args.out, tracks(), source="teticio/Deej-AI pre-computed pickles"
    )

    if args.base_url:
        base = args.base_url if args.base_url.endswith("/") else args.base_url + "/"
        for entry in manifest["files"]:
            entry["url"] = base + entry["name"]

    args.manifest.parent.mkdir(parents=True, exist_ok=True)
    write_manifest(args.manifest, manifest)

    print(
        f"wrote {args.out}/vectors.i8 + {args.out}/catalog.sqlite "
        f"and {args.manifest}",
        file=sys.stderr,
    )
    for entry in manifest["files"]:
        print(f"  {entry['name']:16} {entry['size']:>14,}  {entry['sha256']}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
