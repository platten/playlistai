"""Validate reviewed prompt annotations and create reproducible offline NLU splits.

Only prompt-meaning labels are supervised. This tool never trains music/audio
models and never marks an annotation as reviewed. The first review corpus is a
development corpus; it must not be advertised as an independent held-out study.
"""
from __future__ import annotations

import argparse
from datetime import datetime
import hashlib
import json
from pathlib import Path

SCHEMA = "intent-nlu-review/v1"
IDENTITY_KINDS = {"artist", "album", "track", "composer", "performer"}


def digest(path: Path) -> str:
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def write_json(path: Path, value) -> None:
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def read_records(path: Path) -> list[dict]:
    text = path.read_text(encoding="utf-8-sig")
    try:
        value = json.loads(text)
    except json.JSONDecodeError:
        value = None
    if isinstance(value, dict):
        if value.get("schema") != SCHEMA:
            raise ValueError("Unsupported annotation schema")
        return value["records"]
    if isinstance(value, list):
        return value
    return [json.loads(line) for line in text.splitlines() if line.strip()]


def validate_record(record: dict, require_reviewed: bool = False) -> None:
    for name in ("id", "prompt", "group", "interpretation"):
        if not isinstance(record.get(name), str) or not record[name].strip():
            raise ValueError(f"Missing {name}")
    raw = record["prompt"].encode("utf-8")
    if len(raw) > 16384:
        raise ValueError(f"{record['id']}: prompt is too long")
    spans = record.get("spans")
    if not isinstance(spans, list) or not spans:
        raise ValueError(f"{record['id']}: annotated spans required")
    identifiers = set()
    for span in spans:
        if span.get("id") in identifiers or not span.get("id"):
            raise ValueError(f"{record['id']}: duplicate/missing span ID")
        identifiers.add(span["id"])
        start, end = span.get("start"), span.get("end")
        if type(start) is not int or type(end) is not int or not 0 <= start < end <= len(raw):
            raise ValueError(f"{record['id']}: invalid UTF-8 byte offsets")
        try:
            actual = raw[start:end].decode("utf-8")
        except UnicodeDecodeError as exc:
            raise ValueError(f"{record['id']}: split Unicode character") from exc
        if actual != span.get("text"):
            raise ValueError(f"{record['id']}: span does not match original text")
        for name in ("kind", "role", "polarity", "strength", "scope"):
            if not isinstance(span.get(name), str) or not span[name]:
                raise ValueError(f"{record['id']}: span missing {name}")
        if span["polarity"] not in {"positive", "negative", "neutral"}:
            raise ValueError(f"{record['id']}: invalid polarity")
        if span["strength"] not in {"required", "preferred", "allowed", "none"}:
            raise ValueError(f"{record['id']}: invalid strength")
    for relation in record.get("relations", []):
        if relation.get("from") not in identifiers or relation.get("to") not in identifiers or not relation.get("kind"):
            raise ValueError(f"{record['id']}: dangling relation")
    review = record.get("review", {})
    if review.get("status") not in {"unreviewed", "approved", "rejected"}:
        raise ValueError(f"{record['id']}: invalid review status")
    if require_reviewed or review.get("status") == "approved":
        if review.get("status") != "approved" or not review.get("reviewer", "").strip():
            raise ValueError(f"{record['id']}: actual reviewed approval required before preparation/training")
        try:
            when = datetime.fromisoformat(review.get("reviewedAt", "").replace("Z", "+00:00"))
            if when.tzinfo is None:
                raise ValueError("timezone required")
        except (ValueError, TypeError) as exc:
            raise ValueError(f"{record['id']}: reviewedAt must be an ISO timestamp with timezone") from exc


def verify_model_source(source: Path) -> dict:
    """Check immutable original assets before deserializing local model weights."""
    lock_path = source.parent / "sources.json"
    assets = json.loads(lock_path.read_text(encoding="utf-8-sig"))
    selected = [item for item in assets if item.get("model") == source.name]
    if not selected:
        raise ValueError("Source archive must have its verified sources.json model entries")
    for item in selected:
        path = (source / item["name"]).resolve()
        if not path.is_relative_to(source.resolve()):
            raise ValueError("Unsafe source manifest path")
        if path.stat().st_size != item["size"] or digest(path) != item["sha256"]:
            raise ValueError(f"Source archive checksum mismatch: {item['name']}")
    return {"sourceManifestSHA256": digest(lock_path), "verifiedArtifacts": len(selected), "artifacts": [{"name": item["name"], "sha256": item["sha256"]} for item in selected]}


def grouped_splits(records: list[dict], seed: str) -> tuple[dict, dict]:
    """Keep paraphrase groups and all named identities together, transitively."""
    parents = list(range(len(records)))

    def find(index):
        while parents[index] != index:
            parents[index] = parents[parents[index]]
            index = parents[index]
        return index

    owners = {}
    for index, record in enumerate(records):
        keys = ["group:" + record["group"]]
        for span in record["spans"]:
            if span["kind"] in IDENTITY_KINDS:
                # Canonical review values may unify aliases; raw spelling remains unchanged.
                keys.append("identity:" + span.get("canonical", span["text"]).casefold())
        for key in keys:
            if key in owners:
                parents[find(index)] = find(owners[key])
            else:
                owners[key] = index
    components = {}
    for index, record in enumerate(records):
        components.setdefault(find(index), []).append(record)
    ordered = sorted(components.values(), key=lambda group: hashlib.sha256((seed + "\n" + "\n".join(sorted(row["id"] for row in group))).encode()).hexdigest())
    if len(ordered) < 3:
        raise ValueError("Need at least three independent identity/paraphrase components; do not split connected examples to force a test set")
    splits = {"train": [], "calibration": [], "evaluation": []}
    assignment = {}
    # Ensure all three exist, then approximately 70/15/15 by records. Components
    # remain indivisible, so small review sets may differ substantially from targets.
    for index, component in enumerate(ordered):
        if index < 3:
            target = ("evaluation", "calibration", "train")[index]
        else:
            weights = {"train": .70, "calibration": .15, "evaluation": .15}
            target = min(splits, key=lambda key: len(splits[key]) / weights[key])
        splits[target].extend(component)
        for record in component:
            assignment[record["id"]] = target
    return {key: sorted(value, key=lambda row: row["id"]) for key, value in splits.items()}, assignment


def prepare(source: Path, output: Path, seed: str) -> dict:
    records = read_records(source)
    if len({row["id"] for row in records}) != len(records):
        raise ValueError("Duplicate record IDs")
    for record in records:
        validate_record(record, require_reviewed=True)
    splits, assignment = grouped_splits(records, seed)
    labels = ["O"] + sorted({prefix + span["kind"] + ":" + span["role"] for row in records for span in row["spans"] for prefix in ("B-", "I-")})
    output.mkdir(parents=True, exist_ok=False)
    files = {}
    for name, rows in splits.items():
        path = output / (name + ".jsonl")
        path.write_text("".join(json.dumps(row, ensure_ascii=False, sort_keys=True) + "\n" for row in rows), encoding="utf-8")
        files[path.name] = {"sha256": digest(path), "records": len(rows)}
    write_json(output / "labels.json", labels)
    files["labels.json"] = {"sha256": digest(output / "labels.json")}
    manifest = {"version": 1, "schema": SCHEMA, "sourceSHA256": digest(source), "reviewRequired": True, "seed": seed, "splitPolicy": "transitive-paraphrase-and-canonical-identity/v1", "assignment": assignment, "files": files, "independentBenchmark": False, "warning": "A reviewed development batch is not an independent evaluation benchmark; evaluation labels must remain hidden during tuning."}
    write_json(output / "data-manifest.json", manifest)
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--seed", default="intent-nlu-v1")
    parser.add_argument("--validate-only", action="store_true", help="Validate structure without changing review status or permitting training")
    args = parser.parse_args()
    if args.validate_only:
        records = read_records(args.input)
        for record in records:
            validate_record(record)
        if len({row["id"] for row in records}) != len(records):
            raise ValueError("Duplicate record IDs")
        print(json.dumps({"records": len(records), "approved": sum(row["review"]["status"] == "approved" for row in records), "trainingReady": all(row["review"]["status"] == "approved" for row in records)}, indent=2))
    else:
        if args.output is None:
            parser.error("--output is required unless --validate-only")
        print(json.dumps(prepare(args.input, args.output, args.seed), indent=2))


if __name__ == "__main__":
    main()
