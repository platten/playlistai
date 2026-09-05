#!/usr/bin/env python3
"""Fetch Deej-AI's pre-computed pickles from Google Drive.

These are the only known source (see `docs/CATALOG.md`): four files, ~1.4 GB
total, hosted as individual Google Drive shares by github.com/teticio. IDs
below are copied from `deej-ai.online-app/scripts/download.py`.

Google Drive serves large files through an interstitial "can't scan this file
for viruses" confirmation page rather than the raw bytes, and rate-limits or
quota-blocks popular files outright ("Quota exceeded for this file"). Plain
HTTP GETs land on that HTML page instead of the pickle. `gdown` handles the
confirmation-token dance; the quota case it can't work around, so after each
download this script unpickles the result and checks its shape — a quota/HTML
response fails to unpickle and is reported clearly (with the manual-download
URL) instead of being left on disk as a corrupt `.p` file for
`convert_pickles.py` to choke on later.

Usage:
  python fetch_pickles.py [--out ./pickles] [--only spotifytovec,tracktovec]
"""

from __future__ import annotations

import argparse
import pickle
import sys
from pathlib import Path

import gdown

# id -> (filename, sanity check)
FILES: dict[str, str] = {
    "spotifytovec": "1Mg924qqF3iDgVW5w34m6Zaki5fNBdfSy",
    "tracktovec": "1geEALPQTRBNUvkpI08B-oN4vsIiDTb5I",
    "spotify_tracks": "1Qre4Lkym1n5UTpAveNl5ffxlaAmH1ntS",
    "spotify_urls": "1tLT_wmATWMC5UU-kERLsUNNcz0Vo19J3",
}

# Fallback IDs seen on the project README (unlabeled there; only differs for
# spotify_tracks.p from the download.py ID above, possibly a regenerated
# file from a later retraining pass). Tried only if the primary ID's sanity
# check fails.
FALLBACK_IDS: dict[str, str] = {
    "spotify_tracks": "16JKjDGW2BMP-0KKJLFvwRvJdKhwukBeB",
}

MIN_SIZE = {
    "spotifytovec": 50_000_000,
    "tracktovec": 50_000_000,
    "spotify_tracks": 1_000_000,
    "spotify_urls": 1_000_000,
}


def _sanity_check(name: str, path: Path) -> str | None:
    """Return None if `path` looks like the expected pickle, else an error."""
    try:
        with open(path, "rb") as f:
            obj = pickle.load(f)
    except Exception as exc:  # noqa: BLE001 - want to report any bad-file reason
        return f"failed to unpickle ({exc!r}) — likely an HTML error page, not the real file"

    if not isinstance(obj, dict) or not obj:
        return f"unpickled but got {type(obj).__name__}, expected a non-empty dict"

    sample_key = next(iter(obj))
    sample_val = obj[sample_key]
    if name in ("spotifytovec", "tracktovec"):
        try:
            length = len(sample_val)
        except TypeError:
            return f"values aren't array-like (got {type(sample_val).__name__})"
        if length != 100:
            return f"expected 100-d vectors, got length {length}"
    else:
        if not isinstance(sample_val, str):
            return f"expected string values, got {type(sample_val).__name__}"

    print(f"  {name}: {len(obj):,} entries, looks correct", file=sys.stderr)
    return None


def _download(file_id: str, dest: Path) -> None:
    gdown.download(id=file_id, output=str(dest), quiet=False)


def fetch_one(name: str, out_dir: Path, force: bool) -> bool:
    dest = out_dir / f"{name}.p"

    if dest.exists() and not force:
        size = dest.stat().st_size
        if size >= MIN_SIZE.get(name, 0):
            err = _sanity_check(name, dest)
            if err is None:
                print(f"{dest}: already present ({size:,} bytes), skipping", file=sys.stderr)
                return True
            print(f"{dest}: present but {err}; re-downloading", file=sys.stderr)

    ids_to_try = [FILES[name]] + ([FALLBACK_IDS[name]] if name in FALLBACK_IDS else [])
    for attempt, file_id in enumerate(ids_to_try):
        print(f"downloading {name}.p (id={file_id}) ...", file=sys.stderr)
        try:
            _download(file_id, dest)
        except Exception as exc:  # noqa: BLE001
            print(f"  download failed: {exc!r}", file=sys.stderr)
            continue

        if not dest.exists():
            print("  no file written", file=sys.stderr)
            continue

        size = dest.stat().st_size
        if size < MIN_SIZE.get(name, 0):
            print(f"  downloaded only {size:,} bytes (expected >= {MIN_SIZE[name]:,})", file=sys.stderr)

        err = _sanity_check(name, dest)
        if err is None:
            return True

        print(f"  {dest.name} failed sanity check: {err}", file=sys.stderr)
        if attempt + 1 < len(ids_to_try):
            print("  trying fallback id ...", file=sys.stderr)

    print(
        f"\ncould not fetch a valid {name}.p automatically.\n"
        f"Manual fallback: open https://drive.google.com/uc?id={FILES[name]} "
        f"in a browser (this handles Drive's per-file quota/virus-scan "
        f"interstitial that scripts can't) and save it as {dest}.\n",
        file=sys.stderr,
    )
    return False


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--out", type=Path, default=Path("./pickles"), help="output directory (default ./pickles)")
    ap.add_argument("--only", default="", help="comma-separated subset of: " + ",".join(FILES))
    ap.add_argument("--force", action="store_true", help="re-download even if a valid file is already present")
    args = ap.parse_args(argv)

    names = args.only.split(",") if args.only else list(FILES)
    for n in names:
        if n not in FILES:
            ap.error(f"unknown file {n!r}, expected one of {list(FILES)}")

    args.out.mkdir(parents=True, exist_ok=True)

    ok = True
    for name in names:
        ok = fetch_one(name, args.out, args.force) and ok

    if ok:
        print(f"\nall pickles present in {args.out}/", file=sys.stderr)
        return 0
    print("\none or more pickles could not be fetched automatically; see above.", file=sys.stderr)
    return 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
