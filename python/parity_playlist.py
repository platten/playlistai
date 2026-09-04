#!/usr/bin/env python3
"""Golden playlist fixtures from a faithful reimplementation of upstream.

`make_playlist`, `most_similar`, `most_similar_by_vec` and `join_the_dots` below
mirror teticio/deej-ai.online-app `backend/deejai.py` (async wrappers dropped,
`noise` fixed to 0 for determinism, numpy replaced with plain loops so this
needs only the standard library). They run against `internal/catalog/testdata` —
the int8 vectors are dequantized and L2-normalized so Python and the Go port
operate on the same numbers — and emit
`internal/reco/deejai/testdata/golden/*.json` for the Go parity test.

  cd python && python3 parity_playlist.py
"""

from __future__ import annotations

import json
import math
import sqlite3
import struct
from pathlib import Path

HERE = Path(__file__).resolve().parent
CATALOG_DIR = HERE.parent / "internal" / "catalog" / "testdata"
GOLDEN_DIR = HERE.parent / "internal" / "reco" / "deejai" / "testdata" / "golden"

HEADER_SIZE = 32
DIM = 100
SPACES = 2


def _l2(v: list[float]) -> list[float]:
    n = math.sqrt(sum(x * x for x in v)) or 1.0
    return [x / n for x in v]


def load_catalog(catalog_dir: Path):
    raw = (catalog_dir / "vectors.i8").read_bytes()
    assert raw[:8] == b"PAIVEC1\0", raw[:8]
    _ver, count, dim, spaces, _quant = struct.unpack("<IIIII", raw[8:28])
    assert (dim, spaces) == (DIM, SPACES), (dim, spaces)

    body = raw[HEADER_SIZE:]
    stride = spaces * dim
    mp3tovecs: list[list[list[float]]] = []
    for r in range(count):
        base = r * stride
        audio = [float(struct.unpack("b", body[base + k : base + k + 1])[0]) / 127.0 for k in range(dim)]
        track = [
            float(struct.unpack("b", body[base + dim + k : base + dim + k + 1])[0]) / 127.0
            for k in range(dim)
        ]
        mp3tovecs.append([_l2(audio), _l2(track)])

    db = sqlite3.connect(catalog_dir / "catalog.sqlite")
    rows = db.execute("SELECT row, id, artist, title FROM tracks ORDER BY row").fetchall()
    db.close()
    track_ids = [r[1] for r in rows]
    track_indices = {r[1]: r[0] for r in rows}
    tracks = {r[1]: f"{r[2]} - {r[3]}" for r in rows}
    return mp3tovecs, track_ids, track_indices, tracks


def _sum_vecs(vecs: list[list[float]]) -> list[float]:
    if not vecs:
        return [0.0] * DIM
    out = list(vecs[0])
    for v in vecs[1:]:
        for k in range(DIM):
            out[k] += v[k]
    return out


class Deejai:
    def __init__(self, catalog_dir: Path):
        self.mp3tovecs, self.track_ids, self.track_indices, self.tracks = load_catalog(catalog_dir)

    def _scores(self, qi: list[list[float]]) -> list[float]:
        out = []
        for row in self.mp3tovecs:
            s = 0.0
            for k in range(DIM):
                s += row[0][k] * qi[0][k] + row[1][k] * qi[1][k]
            out.append(s)
        return out

    def most_similar(self, weights, positive=(), negative=()):
        qi = [
            [
                weights[j] * x
                for x in _sum_vecs(
                    [self.mp3tovecs[i][j] for i in positive]
                    + [[-c for c in self.mp3tovecs[i][j]] for i in negative]
                )
            ]
            for j in range(2)
        ]
        scores = self._scores(qi)
        result = sorted(range(len(scores)), key=lambda i: scores[i])  # ascending, stable
        for i in negative:
            result.remove(i)
        result.reverse()
        for i in positive:
            result.remove(i)
        return result

    def most_similar_by_vec(self, weights, positives):
        qi = [[weights[j] * x for x in _sum_vecs(positives[j])] for j in range(2)]
        scores = self._scores(qi)
        return sorted(range(len(scores)), key=lambda i: -scores[i])  # descending, stable

    def _artist(self, track_id: str) -> str:
        disp = self.tracks[track_id]
        return disp[: disp.find(" - ")]

    def make_playlist(self, weights, seeds, size=10, lookback=3):
        playlist = list(seeds)
        playlist_tracks = [self.tracks[_] for _ in playlist]
        playlist_indices = [self.track_indices[_] for _ in playlist]
        for _ in range(len(playlist), size):
            candidates = self.most_similar(weights, positive=playlist_indices[-lookback:])
            track_id = None
            candidate = candidates[-1] if candidates else None
            for candidate in candidates:
                track_id = self.track_ids[candidate]
                if (
                    track_id not in playlist
                    and self.tracks[track_id] not in playlist_tracks
                    and self._artist(track_id) != self._artist(playlist[-1])
                ):
                    break
            playlist.append(track_id)
            playlist_tracks.append(self.tracks[track_id])
            playlist_indices.append(candidate)
        return playlist

    def join_the_dots(self, weights, ids, size=5):
        ids = list(ids)
        playlist: list[str] = []
        playlist_tracks = [self.tracks[_] for _ in ids]
        end = start = ids[0]
        start_vec = self.mp3tovecs[self.track_indices[start]]
        for end in ids[1:]:
            end_vec = self.mp3tovecs[self.track_indices[end]]
            playlist.append(start)
            for i in range(size):
                positives = [
                    [
                        [
                            (size - i) / (size + 1) * start_vec[k][c]
                            + (i + 1) / (size + 1) * end_vec[k][c]
                            for c in range(DIM)
                        ]
                    ]
                    for k in range(2)
                ]
                candidates = self.most_similar_by_vec(weights, positives)
                track_id = self.track_ids[candidates[-1]]
                for candidate in candidates:
                    track_id = self.track_ids[candidate]
                    if (
                        track_id not in playlist + ids
                        and self.tracks[track_id] not in playlist_tracks
                        and self._artist(track_id) != self._artist(playlist[-1])
                    ):
                        break
                playlist.append(track_id)
            start = end
            start_vec = end_vec
        playlist.append(end)
        return playlist

    def build(self, case: dict) -> list[str]:
        w = [case["creativity"], 1 - case["creativity"]]
        if case["mode"] == "journey":
            return self.join_the_dots(w, case["seeds"], size=case["count"])
        return self.make_playlist(w, case["seeds"], size=case["count"], lookback=case["lookback"])


CASES = [
    {"name": "similar_seed1_c00", "mode": "similar", "seeds": ["seed0001"], "creativity": 0.0, "lookback": 3, "count": 15},
    {"name": "similar_seed1_c05", "mode": "similar", "seeds": ["seed0001"], "creativity": 0.5, "lookback": 3, "count": 15},
    {"name": "similar_seed1_c10", "mode": "similar", "seeds": ["seed0001"], "creativity": 1.0, "lookback": 3, "count": 15},
    {"name": "similar_seed4_lb1", "mode": "similar", "seeds": ["seed0004"], "creativity": 0.5, "lookback": 1, "count": 12},
    {"name": "similar_seed6_c03", "mode": "similar", "seeds": ["seed0006"], "creativity": 0.3, "lookback": 3, "count": 20},
    {"name": "journey_1_4", "mode": "journey", "seeds": ["seed0001", "seed0004"], "creativity": 0.5, "lookback": 3, "count": 8},
    {"name": "journey_1_3_5", "mode": "journey", "seeds": ["seed0001", "seed0003", "seed0005"], "creativity": 0.7, "lookback": 3, "count": 5},
]


def main() -> int:
    dj = Deejai(CATALOG_DIR)
    GOLDEN_DIR.mkdir(parents=True, exist_ok=True)
    for case in CASES:
        track_ids = dj.build(case)
        out = {
            "params": {k: case[k] for k in ("mode", "seeds", "creativity", "lookback", "count")},
            "track_ids": track_ids,
        }
        (GOLDEN_DIR / f"{case['name']}.json").write_text(json.dumps(out, indent=2) + "\n")
        print(f"{case['name']:20} {len(track_ids)} tracks -> {track_ids[:4]}...")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
