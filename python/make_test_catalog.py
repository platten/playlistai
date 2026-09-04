#!/usr/bin/env python3
"""Generate a small deterministic synthetic catalog for Go tests.

Writes vectors.i8 + catalog.sqlite (same format as convert_pickles.py) into the
target directory. Vectors are seeded pseudo-random unit vectors; a handful of
tracks share an artist and a few carry accented names so the token-search and
normalization tests have something real to bite on.

  python make_test_catalog.py --out ../internal/catalog/testdata [--count 256]
"""

from __future__ import annotations

import argparse
import math
import random
import sys
from pathlib import Path

from catalogfmt import DIM, Track, write_catalog, write_manifest

# A few fixed rows the Go tests assert on by id.
FIXED: list[tuple[str, str, str, str]] = [
    ("seed0001", "Justice", "Genesis", "https://cdn.example/preview/genesis.mp3"),
    ("seed0002", "Justice", "Stress", ""),
    ("seed0003", "SebastiAn", "Rerun", ""),
    ("seed0004", "Boards of Canada", "Roygbiv", ""),
    ("seed0005", "Sigur Rós", "Svefn-g-englar", ""),  # accents + hyphen
    ("seed0006", "Björk", "Jóga", ""),  # accents
    ("seed0007", "Justice", "D.A.N.C.E. - Radio Edit", ""),  # inner " - "
]

ARTISTS = [
    "Kavinsky", "M83", "Aphex Twin", "The Chemical Brothers", "Daft Punk",
    "Bonobo", "Four Tet", "Burial", "Jon Hopkins", "Rival Consoles",
    "Tycho", "Com Truise", "Floating Points", "Caribou", "Moderat",
]
WORDS = [
    "Nightcall", "Outrun", "Tetra", "Midnight", "City", "Signal", "Drift",
    "Aurora", "Lantern", "Marble", "Static", "Ember", "Vault", "Prism",
    "Halcyon", "Cirrus", "Onyx", "Pulse", "Meridian", "Cascade",
]


def unit_vector(rng: random.Random) -> list[float]:
    v = [rng.gauss(0.0, 1.0) for _ in range(DIM)]
    n = math.sqrt(sum(x * x for x in v)) or 1.0
    return [x / n for x in v]


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--out", type=Path, required=True)
    ap.add_argument("--count", type=int, default=256)
    ap.add_argument("--seed", type=int, default=20260904)
    args = ap.parse_args(argv)

    rng = random.Random(args.seed)

    def tracks():
        for tid, artist, title, preview in FIXED:
            yield Track(tid, artist, title, preview, unit_vector(rng), unit_vector(rng))
        for i in range(len(FIXED), args.count):
            artist = ARTISTS[rng.randrange(len(ARTISTS))]
            title = f"{WORDS[rng.randrange(len(WORDS))]} {WORDS[rng.randrange(len(WORDS))]}"
            yield Track(
                f"trk{i:05d}", artist, title, "", unit_vector(rng), unit_vector(rng)
            )

    manifest = write_catalog(args.out, tracks(), source="synthetic test fixture")
    write_manifest(args.out / "catalog-manifest.json", manifest)
    print(
        f"wrote {args.count} synthetic tracks to {args.out}",
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
