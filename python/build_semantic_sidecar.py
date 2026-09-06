#!/usr/bin/env python3
"""Build a bounded, grounded semantic sidecar from supplied JSONL evidence.

No metadata is inferred and no web API is called. The Sentence Transformers
model must already exist locally; pin --model-name and --model-revision to the
identity used to obtain that local snapshot.
"""

from __future__ import annotations

import argparse
import json
import sqlite3
import struct
import unicodedata
from pathlib import Path

SCHEMA_VERSION = 2
QUERY_ENCODER = "precomputed-query-v1"
FACETS = ("tags", "descriptions", "styles", "moods", "instrumentation", "vocal_evidence", "release_dates")
STOP_WORDS = {"a", "an", "and", "but", "for", "in", "of", "or", "the", "to", "with"}


def arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--catalog", type=Path, required=True, help="catalog.sqlite used to ground track IDs")
    parser.add_argument("--input", type=Path, required=True, help="UTF-8 JSONL evidence")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--model", type=Path, required=True, help="already-downloaded Sentence Transformers model")
    parser.add_argument("--model-name", required=True)
    parser.add_argument("--model-revision", required=True)
    parser.add_argument("--feature-version", required=True)
    parser.add_argument("--limit", type=int, default=5000)
    parser.add_argument("--report", type=Path)
    parser.add_argument("--musicbrainz-cache", type=Path, help="optional existing cache; never queried over the network")
    parser.add_argument("--min-musicbrainz-score", type=int, default=85)
    return parser.parse_args()


def known(value: object) -> dict:
    if value is None or value == "":
        return {"value": "", "missingness": "unknown", "confidence": 0, "provenance": []}
    if isinstance(value, str):
        raise ValueError("feature values require confidence and provenance objects")
    item = dict(value)
    item.setdefault("value", "")
    item.setdefault("confidence", 0)
    item.setdefault("provenance", [])
    item["missingness"] = "known" if item["value"] else "unknown"
    if item["missingness"] == "known" and (not item["provenance"] or not 0 <= float(item["confidence"]) <= 1):
        raise ValueError("known feature requires provenance and confidence in [0,1]")
    return item


def values(record: dict, key: str) -> list[dict]:
    return [known(value) for value in record.get(key, []) if value]


def query_keys(text: str) -> list[str]:
    normalized = unicodedata.normalize("NFKC", text).lower()
    tokens: list[str] = []
    current: list[str] = []
    for character in normalized:
        if character.isalpha() or character.isnumeric():
            current.append(character)
        elif current:
            token = "".join(current)
            if token not in STOP_WORDS:
                tokens.append(token)
            current = []
    if current:
        token = "".join(current)
        if token not in STOP_WORDS:
            tokens.append(token)
    keys: list[str] = []
    if len(tokens) > 1:
        keys.append(" ".join(tokens))
        keys.extend(" ".join(tokens[index:index + 2]) for index in range(len(tokens) - 1))
    keys.extend(tokens)
    return list(dict.fromkeys(keys))


def build_feature(record: dict, catalog_version: str) -> tuple[dict, str, list[str]]:
    feature = {
        "schemaVersion": SCHEMA_VERSION,
        "catalogVersion": catalog_version,
        "trackId": record["track_id"],
        "artistId": known(record.get("artist_id")),
        "recordingId": known(record.get("recording_id")),
        "tags": values(record, "tags"),
        "descriptions": values(record, "descriptions"),
        "styles": values(record, "styles"),
        "moods": values(record, "moods"),
        "instrumentation": values(record, "instrumentation"),
        "vocalEvidence": known(record.get("vocal_evidence")),
        "releaseDates": {
            "originalEdition": known(record.get("original_release")),
            "releaseEdition": known(record.get("release_edition")),
        },
        "previewCoverage": record.get("preview_coverage", {
            "available": False, "startSeconds": 0, "endSeconds": 0,
            "coveredSeconds": 0, "source": "",
        }),
    }
    text_parts: list[str] = []
    for key in ("descriptions", "tags", "styles", "moods", "instrumentation"):
        text_parts.extend(item["value"] for item in feature[key] if item["missingness"] == "known")
    vocal = feature["vocalEvidence"]
    if vocal["missingness"] == "known":
        text_parts.append(vocal["value"])
    if not text_parts:
        raise ValueError(f"track {record['track_id']} has no grounded descriptive text")
    return feature, ". ".join(text_parts), text_parts


def cached_musicbrainz(path: Path | None, minimum: int) -> dict[str, dict]:
    if path is None:
        return {}
    db = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    result: dict[str, dict] = {}
    try:
        for (raw,) in db.execute("SELECT json FROM mb_cache"):
            item = json.loads(raw)
            track_id = item.get("ref", {}).get("id", "")
            if not item.get("matched") or int(item.get("matchScore", 0)) < minimum or not track_id:
                continue
            confidence = int(item["matchScore"]) / 100
            def evidence(value: str, source_id: str) -> dict | None:
                if not value or not source_id:
                    return None
                return {"value": value, "confidence": confidence, "provenance": [{
                    "source": "musicbrainz-cache", "sourceId": source_id,
                    "sourceVersion": "ws2", "modelVersion": "", "confidence": confidence,
                }]}
            artist_ids = item.get("artistIds", [])
            result[track_id] = {
                "artist_id": evidence(artist_ids[0], artist_ids[0]) if len(artist_ids) == 1 else None,
                "recording_id": evidence(item.get("recordingId", ""), item.get("recordingId", "")),
                "release_edition": evidence(item.get("releaseEditionDate", ""), item.get("releaseId", "")),
                # Only the explicit release-group first-release date is used here.
                # The first returned release's date remains an edition date.
                "original_release": evidence(item.get("originalReleaseDate", ""), item.get("recordingId", "")),
            }
    finally:
        db.close()
    return result


def main() -> None:
    args = arguments()
    if args.limit < 1:
        raise ValueError("--limit must be positive")
    from sentence_transformers import SentenceTransformer

    catalog = sqlite3.connect(f"file:{args.catalog}?mode=ro", uri=True)
    catalog_ids = {row[0] for row in catalog.execute("SELECT id FROM tracks")}
    catalog_meta = dict(catalog.execute("SELECT key, value FROM meta"))
    catalog_version = ":".join(catalog_meta.get(k, "") for k in ("format_version", "track_count", "created"))
    records: list[tuple[dict, str, list[str]]] = []
    rejected: list[dict] = []
    enrichment = cached_musicbrainz(args.musicbrainz_cache, args.min_musicbrainz_score)
    with args.input.open(encoding="utf-8") as source:
        for line_number, line in enumerate(source, 1):
            if len(records) >= args.limit:
                break
            if not line.strip():
                continue
            try:
                raw = json.loads(line)
                if raw.get("track_id") not in catalog_ids:
                    raise ValueError("track_id is not in catalog")
                for key, value in enrichment.get(raw["track_id"], {}).items():
                    if value and not raw.get(key):
                        raw[key] = value
                records.append(build_feature(raw, catalog_version))
            except (KeyError, TypeError, ValueError, json.JSONDecodeError) as error:
                rejected.append({"line": line_number, "reason": str(error)})
    records.sort(key=lambda item: item[0]["trackId"])
    if not records:
        raise ValueError("no grounded records were accepted")
    model = SentenceTransformer(str(args.model), local_files_only=True)
    embeddings = model.encode_document(
        [text for _, text, _ in records], normalize_embeddings=True,
        convert_to_numpy=True, show_progress_bar=True,
    )
    dimension = int(embeddings.shape[1])
    query_terms = sorted({key for _, _, phrases in records for phrase in phrases for key in query_keys(phrase)})
    if not query_terms:
        raise ValueError("grounded records produced no query vocabulary")
    query_embeddings = model.encode_query(
        query_terms, normalize_embeddings=True,
        convert_to_numpy=True, show_progress_bar=True,
    )

    if args.output.exists():
        args.output.unlink()
    db = sqlite3.connect(args.output)
    try:
        db.executescript("""
            PRAGMA journal_mode=OFF;
            PRAGMA synchronous=OFF;
            CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
            CREATE TABLE features (track_id TEXT PRIMARY KEY, feature_json TEXT NOT NULL);
            CREATE TABLE semantic_vectors (track_id TEXT PRIMARY KEY, embedding BLOB NOT NULL);
            CREATE TABLE query_vectors (term TEXT PRIMARY KEY, embedding BLOB NOT NULL);
        """)
        meta = {
            "schema_version": str(SCHEMA_VERSION), "catalog_version": catalog_version,
            "feature_version": args.feature_version, "text_model": args.model_name,
            "model_revision": args.model_revision, "embedding_dim": str(dimension),
            "track_count": str(len(records)), "supported_facets": json.dumps(list(FACETS)),
            "query_encoder": QUERY_ENCODER, "query_term_count": str(len(query_terms)),
        }
        db.executemany("INSERT INTO meta VALUES (?, ?)", sorted(meta.items()))
        db.executemany("INSERT INTO features VALUES (?, ?)", [
            (feature["trackId"], json.dumps(feature, separators=(",", ":"), ensure_ascii=False))
            for feature, _, _ in records
        ])
        db.executemany("INSERT INTO semantic_vectors VALUES (?, ?)", [
            (feature["trackId"], sqlite3.Binary(struct.pack(f"<{dimension}f", *embedding)))
            for (feature, _, _), embedding in zip(records, embeddings, strict=True)
        ])
        db.executemany("INSERT INTO query_vectors VALUES (?, ?)", [
            (term, sqlite3.Binary(struct.pack(f"<{dimension}f", *embedding)))
            for term, embedding in zip(query_terms, query_embeddings, strict=True)
        ])
        db.commit()
        db.execute("VACUUM")
    finally:
        db.close()
        catalog.close()

    coverage = {facet: 0 for facet in FACETS}
    for feature, _, _ in records:
        for facet in FACETS:
            if facet == "vocal_evidence":
                present = feature["vocalEvidence"]["missingness"] == "known"
            elif facet == "release_dates":
                present = any(v["missingness"] == "known" for v in feature["releaseDates"].values())
            else:
                present = bool(feature[facet])
            coverage[facet] += int(present)
    report = {
        "schemaVersion": SCHEMA_VERSION, "catalogVersion": catalog_version,
        "featureVersion": args.feature_version, "model": args.model_name,
        "modelRevision": args.model_revision, "embeddingDimension": dimension,
        "queryEncoder": QUERY_ENCODER, "queryTerms": len(query_terms),
        "catalogTracks": len(catalog_ids), "pilotTracks": len(records),
        "coverage": coverage, "rejected": rejected,
        "indexBytes": args.output.stat().st_size,
    }
    output = json.dumps(report, indent=2, ensure_ascii=False) + "\n"
    if args.report:
        args.report.write_text(output, encoding="utf-8")
    print(output, end="")


if __name__ == "__main__":
    main()
