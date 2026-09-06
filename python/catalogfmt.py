"""Shared on-disk format for the Playlist AI catalog.

Two files make up a catalog directory:

  vectors.i8      32-byte header + N*SPACES*DIM int8 values, row-major.
                  Row r holds track keys[r]; space 0 is the audio-content
                  embedding (Deej-AI spotifytovec), space 1 the playlist
                  co-occurrence embedding (tracktovec). Each 100-d sub-vector is
                  L2-normalized, then quantized: int8 = round(clamp(v,-1,1)*127).
                  Dequantize with v/127; the Go loader re-normalizes.

  catalog.sqlite  table `tracks(row, id, artist, title, preview, search, ...)` plus a
                  `meta` key/value table. `row` matches vectors.i8 row order.
                  `search` is the normalized "artist title" string used for the
                  token-substring search (see normalize_search); the Go side
                  implements the identical normalization.

`convert_pickles.py` produces a catalog from the Deej-AI pickles;
`make_test_catalog.py` produces a small synthetic one for tests. Both go through
this module so the format can't drift.
"""

from __future__ import annotations

import json
import re
import sqlite3
import struct
import time
import unicodedata
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable, Iterator, Sequence

MAGIC = b"PAIVEC1\0"
FORMAT_VERSION = 1
DIM = 100
SPACES = 2
QUANT_INT8_127 = 1
HEADER_SIZE = 32
QUANT_SCALE = 127.0

_NON_ALNUM_RE = re.compile(r"[^a-z0-9]+")
_WS_RE = re.compile(r"\s+")


def normalize_search(text: str) -> str:
    """Fold `text` to the canonical search form.

    NFKD normalize -> drop Unicode category Mn (nonspacing marks) -> lowercase ->
    every run of non-[a-z0-9] becomes a single space -> trim. Deterministic and
    reproduced byte-for-byte in Go (internal/catalog/search.go). Non-Latin
    scripts (CJK, Cyrillic, ...) are stripped rather than transliterated; this
    diverges from Deej-AI's `unidecode` but keeps the two implementations exactly
    in step.
    """
    decomposed = unicodedata.normalize("NFKD", text)
    no_marks = "".join(c for c in decomposed if unicodedata.category(c) != "Mn")
    lowered = no_marks.lower()
    spaced = _NON_ALNUM_RE.sub(" ", lowered)
    return _WS_RE.sub(" ", spaced).strip()


def normalize_unicode_search(text: str) -> str:
    """Fold search text while preserving letters and numbers in every script."""
    decomposed = unicodedata.normalize("NFKD", text)
    chars: list[str] = []
    pending_space = False
    for char in decomposed:
        if unicodedata.category(char) == "Mn":
            continue
        char = char.lower()
        if char.isalpha() or char.isnumeric():
            if pending_space and chars:
                chars.append(" ")
            chars.append(char)
            pending_space = False
        else:
            pending_space = True
    return "".join(chars)


def split_display(display: str) -> tuple[str, str]:
    """Split "Artist - Title" on the first " - " (Deej-AI's own convention)."""
    idx = display.find(" - ")
    if idx < 0:
        return "", display
    return display[:idx], display[idx + 3 :]


@dataclass
class Track:
    id: str
    artist: str
    title: str
    preview: str
    audio: Sequence[float]  # length DIM
    track: Sequence[float]  # length DIM
    aliases: Sequence[str] = ()  # artist aliases, if supplied by the source


def _l2_normalize(vec: Sequence[float]) -> list[float]:
    norm = sum(x * x for x in vec) ** 0.5
    if norm == 0.0:
        return [0.0] * len(vec)
    return [x / norm for x in vec]


def _quantize(vec: Sequence[float]) -> bytes:
    out = bytearray(len(vec))
    for i, x in enumerate(vec):
        q = round(max(-1.0, min(1.0, x)) * QUANT_SCALE)
        out[i] = struct.pack("b", max(-127, min(127, q)))[0]
    return bytes(out)


def pack_header(count: int) -> bytes:
    head = MAGIC + struct.pack(
        "<IIIII", FORMAT_VERSION, count, DIM, SPACES, QUANT_INT8_127
    )
    return head.ljust(HEADER_SIZE, b"\0")


def write_catalog(out_dir: Path, tracks: Iterable[Track], *, source: str) -> dict:
    """Write vectors.i8 + catalog.sqlite into `out_dir`. Returns a manifest dict."""
    out_dir.mkdir(parents=True, exist_ok=True)
    vec_path = out_dir / "vectors.i8"
    db_path = out_dir / "catalog.sqlite"
    if db_path.exists():
        db_path.unlink()

    db = sqlite3.connect(db_path)
    try:
        db.executescript(
            """
            PRAGMA journal_mode = OFF;
            PRAGMA synchronous = OFF;
            CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
            CREATE TABLE tracks (
                row     INTEGER PRIMARY KEY,
                id      TEXT NOT NULL UNIQUE,
                artist  TEXT NOT NULL,
                title   TEXT NOT NULL,
                preview TEXT NOT NULL DEFAULT '',
                search  TEXT NOT NULL,
                artist_search TEXT NOT NULL,
                title_search TEXT NOT NULL,
                unicode_search TEXT NOT NULL
            );
            CREATE TABLE artist_aliases (
                artist TEXT NOT NULL,
                alias TEXT NOT NULL,
                alias_search TEXT NOT NULL,
                alias_unicode TEXT NOT NULL,
                PRIMARY KEY (artist, alias)
            );
            """
        )

        count = 0
        materialized: list[Track] = list(tracks)
        count = len(materialized)

        with open(vec_path, "wb") as vf:
            vf.write(pack_header(count))
            rows = []
            for row, t in enumerate(materialized):
                if len(t.audio) != DIM or len(t.track) != DIM:
                    raise ValueError(f"track {t.id}: sub-vector not {DIM}-d")
                vf.write(_quantize(_l2_normalize(t.audio)))
                vf.write(_quantize(_l2_normalize(t.track)))
                rows.append(
                    (
                        row,
                        t.id,
                        t.artist,
                        t.title,
                        t.preview,
                        normalize_search(f"{t.artist} {t.title}"),
                        normalize_search(t.artist),
                        normalize_search(t.title),
                        normalize_unicode_search(f"{t.artist} {t.title}"),
                    )
                )
            db.executemany(
                "INSERT INTO tracks (row, id, artist, title, preview, search, "
                "artist_search, title_search, unicode_search) "
                "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
                rows,
            )
            aliases = {
                (t.artist, alias.strip())
                for t in materialized
                for alias in t.aliases
                if alias.strip()
            }
            db.executemany(
                "INSERT INTO artist_aliases "
                "(artist, alias, alias_search, alias_unicode) VALUES (?, ?, ?, ?)",
                [
                    (artist, alias, normalize_search(alias), normalize_unicode_search(alias))
                    for artist, alias in sorted(aliases)
                ],
            )
            db.executescript(
                """
                CREATE INDEX tracks_artist_search_idx ON tracks(artist_search);
                CREATE INDEX tracks_title_search_idx ON tracks(title_search);
                CREATE INDEX tracks_unicode_search_idx ON tracks(unicode_search);
                CREATE INDEX artist_alias_search_idx ON artist_aliases(alias_search);
                CREATE INDEX artist_alias_unicode_idx ON artist_aliases(alias_unicode);
                """
            )

        created = int(time.time())
        db.executemany(
            "INSERT INTO meta (key, value) VALUES (?, ?)",
            [
                ("format_version", str(FORMAT_VERSION)),
                ("dim", str(DIM)),
                ("spaces", str(SPACES)),
                ("quant", "int8-global-127"),
                ("track_count", str(count)),
                ("source", source),
                ("created", str(created)),
                ("search_schema_version", "2"),
            ],
        )
        db.commit()
        db.execute("VACUUM")
        db.commit()
    finally:
        db.close()

    return {
        "name": "playlist-ai-catalog",
        "format_version": FORMAT_VERSION,
        "dim": DIM,
        "spaces": SPACES,
        "quant": "int8-global-127",
        "track_count": count,
        "source": source,
        "created": created,
        "files": [
            _file_entry(vec_path),
            _file_entry(db_path),
        ],
    }


def _file_entry(path: Path) -> dict:
    import hashlib

    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return {"name": path.name, "size": path.stat().st_size, "sha256": h.hexdigest()}


def write_manifest(path: Path, manifest: dict) -> None:
    path.write_text(json.dumps(manifest, indent=2) + "\n")


def iter_common_keys(*dicts: dict) -> Iterator[str]:
    """Sorted intersection of the given dicts' keys."""
    common = set(dicts[0])
    for d in dicts[1:]:
        common &= set(d)
    yield from sorted(common)
