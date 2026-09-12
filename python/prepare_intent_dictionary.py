#!/usr/bin/env python3
"""Validate and archive the small reviewed intent registry; offline tooling only.

No training, provider requests, PCM, or model inference occurs here. Native Go
embeds the checked-in JSON, so desktop users do not need Python or this archive.
"""

from __future__ import annotations

import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
KINDS = {"genre", "mood", "instrumentation", "vocal", "texture", "activity"}


def key(value: str) -> str:
    return " ".join(value.casefold().split())


def validate(document: dict) -> None:
    if document.get("version") != "music-concepts/v1":
        raise ValueError("unsupported registry version; update both Go and preparation contracts")
    concepts = document.get("concepts", [])
    if not concepts or not document.get("provenance"):
        raise ValueError("registry requires concepts and provenance")
    ids, aliases = {}, {}
    for concept in concepts:
        for field in ("id", "kind", "value", "source", "license"):
            if not isinstance(concept.get(field), str) or not concept[field].strip():
                raise ValueError(f"missing {field}: {concept.get('id')}")
        identity = concept["id"]
        if identity in ids or concept["kind"] not in KINDS:
            raise ValueError(f"duplicate identity or unsupported kind: {identity}")
        ids[identity] = concept
        for spelling in [concept["value"], *concept.get("aliases", [])]:
            if not isinstance(spelling, str) or not spelling.strip():
                raise ValueError(f"empty/non-string alias: {identity}")
            alias = (concept["kind"], key(spelling))
            if alias in aliases and aliases[alias] != identity:
                raise ValueError(f"ambiguous exact alias {alias}: {identity}, {aliases[alias]}")
            aliases[alias] = identity
        providers = concept.get("providers", {})
        for query in providers.get("musicBrainz", []):
            if key(query) not in {key(s) for s in [concept["value"], *concept.get("aliases", [])]}:
                raise ValueError(f"MusicBrainz query is not a reviewed exact alias: {identity}: {query}")
        if "genre_electronic" in providers.get("acousticBrainz", {}):
            raise ValueError("conditional electronic subgenres need an applicability gate, not a direct mapping")
    for identity, concept in ids.items():
        for related in [*concept.get("parents", []), *concept.get("related", [])]:
            if related == identity or related not in ids:
                raise ValueError(f"invalid relation: {identity} -> {related}")
    visited, visiting = set(), set()

    def visit(identity: str) -> None:
        if identity in visiting:
            raise ValueError(f"cycle in parent taxonomy: {identity}")
        if identity in visited:
            return
        visiting.add(identity)
        for parent in ids[identity].get("parents", []):
            visit(parent)
        visiting.remove(identity)
        visited.add(identity)

    for identity in ids:
        visit(identity)


def stable_json(value: object) -> bytes:
    return (json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2) + "\n").encode("utf-8")


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def prepare(args: argparse.Namespace) -> dict:
    raw = args.input.read_bytes()
    document = json.loads(raw)
    validate(document)
    canonical = stable_json(document)
    compressed = io.BytesIO()
    with gzip.GzipFile(filename="", mode="wb", fileobj=compressed, mtime=0, compresslevel=9) as archive:
        archive.write(canonical)
    gz = compressed.getvalue()
    manifest = {
        "version": document["version"],
        "conceptCount": len(document["concepts"]),
        "sourceSHA256": sha256(raw),
        "registrySHA256": sha256(canonical),
        "gzipSHA256": sha256(gz),
        "registryBytes": len(canonical),
        "gzipBytes": len(gz),
        "runtime": "Go embed; no Python desktop prerequisite",
        "license": document["provenance"]["license"],
        "sources": [],
    }
    evidence = []
    if args.evidence and not args.evidence_license:
        raise ValueError("--evidence-license is required when archiving source evidence")
    for path in args.evidence:
        if path.stat().st_size > 64 * 1024 * 1024:
            raise ValueError("evidence inputs must be metadata samples <=64 MiB, not audio/model/dump archives")
        data = path.read_bytes()
        digest = sha256(data)
        name = digest[:12] + "-" + path.name
        evidence.append((name, data))
        manifest["sources"].append({"file": "sources/" + name, "sha256": digest, "license": args.evidence_license})
    if args.check:
        return manifest
    args.output.mkdir(parents=True, exist_ok=True)
    # Keep the original alongside the distributable form for later review.
    (args.output / "concepts.source.json").write_bytes(raw)
    (args.output / "concepts.json").write_bytes(canonical)
    (args.output / "concepts.json.gz").write_bytes(gz)
    if evidence:
        (args.output / "sources").mkdir(exist_ok=True)
    for name, data in evidence:
        (args.output / "sources" / name).write_bytes(data)
    (args.output / "manifest.json").write_bytes(stable_json(manifest))
    return manifest


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, default=ROOT / "internal/musicconcepts/concepts.json")
    parser.add_argument("--output", type=Path, default=Path.home() / "Downloads/playlistai-intent-dictionary-v1")
    parser.add_argument("--check", action="store_true", help="validate and report hashes without writing files")
    parser.add_argument("--evidence", type=Path, action="append", default=[], help="retain an existing metadata source sample; repeatable")
    parser.add_argument("--evidence-license", help="license identifier for all supplied source samples")
    args = parser.parse_args()
    try:
        manifest = prepare(args)
    except (OSError, ValueError, KeyError, TypeError) as error:
        parser.error(str(error))
    print(json.dumps(manifest, indent=2, ensure_ascii=False))


if __name__ == "__main__":
    main()
