#!/usr/bin/env python3
"""Prepare resumable Discogs-EffNet evidence from permitted local PCM WAV files.

Python/Essentia is maintainer tooling only; the desktop consumes derived scores.
Setup reuses the repository's pinned, resumable asset downloader. Preparation
never fetches audio, loads remote code, or copies input audio into its outputs.
"""

import argparse
import array
import hashlib
import importlib.metadata
import json
import math
import os
from pathlib import Path
import sys
import tempfile
import wave

from fetch_enhanced_audio import fetch, target_path


LOCK_PATH = Path(__file__).with_name("discogs-effnet-sources.json")
PREPROCESSING = "pcm16-mono16k-effnet128-hop62-repeat-lastbatchsame-mean/v1"
KINDS = ("instrumentation", "vocal", "mood", "style")
MAX_AUDIO_BYTES = 512 * 1024 * 1024


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False)


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def read_lock():
    raw = LOCK_PATH.read_bytes()
    lock = json.loads(raw)
    if lock["version"] != 1 or len(lock["assets"]) != 11:
        raise ValueError("unsupported classifier source lock")
    return lock, hashlib.sha256(raw).hexdigest()


def model_definitions(root, lock):
    """Verify all graphs and metadata before the first model is loaded."""
    for asset in lock["assets"]:
        fetch(root, asset, verify_only=True)
    definitions = {}
    for kind in ("encoder",) + KINDS:
        weights = next(a for a in lock["assets"] if a["kind"] == kind and a["path"].endswith(".pb"))
        metadata = next(a for a in lock["assets"] if a["kind"] == kind and a["path"].endswith(".json"))
        model = json.loads(target_path(root, metadata).read_text(encoding="utf-8"))
        purpose = "embeddings" if kind == "encoder" else "predictions"
        output = next(v for v in model["schema"]["outputs"] if v.get("output_purpose") == purpose)
        identity = {"model": Path(weights["path"]).stem, "revision": weights["revision"],
                    "weightsSha256": weights["sha256"], "metadataSha256": metadata["sha256"]}
        definitions[kind] = {"model": identity, "path": str(target_path(root, weights)),
                             "classes": model["classes"], "input": model["schema"]["inputs"][0]["name"],
                             "output": output["name"]}
    return definitions


def source_file(root, row):
    if not isinstance(row.get("trackId"), str) or not row["trackId"].strip():
        raise ValueError("source row needs an exact catalog trackId")
    catalog_hash = row.get("catalogSha256", "")
    if len(catalog_hash) != 64 or any(c not in "0123456789abcdef" for c in catalog_hash):
        raise ValueError("source row needs a pinned catalogSha256")
    for name in ("source", "sourceId", "license", "derivedLicense"):
        if not isinstance(row.get(name), str) or not row[name].strip() or len(row[name]) > 2048:
            raise ValueError(f"source row needs bounded {name} provenance")
    if row.get("redistributionAllowed") is not True:
        raise ValueError("public preparation requires explicit derived-evidence redistribution permission")
    asset = {"path": row["path"]}
    path = target_path(root, asset)
    if not path.is_file() or path.stat().st_size > MAX_AUDIO_BYTES:
        raise ValueError("audio source missing or exceeds 512 MiB bound")
    expected = row.get("audioSha256", "")
    if len(expected) != 64 or any(c not in "0123456789abcdef" for c in expected) or digest(path) != expected:
        raise ValueError("audio source checksum differs from manifest")
    return path


def read_pcm(path, start_seconds, max_seconds):
    """Read bounded source samples, preserving coverage before model padding."""
    if not math.isfinite(start_seconds) or start_seconds < 0 or not 3 <= max_seconds <= 60:
        raise ValueError("start must be nonnegative; sample bound must be 3–60 seconds")
    with wave.open(str(path), "rb") as source:
        if source.getframerate() != 16000 or source.getnchannels() != 1 or source.getsampwidth() != 2 or source.getcomptype() != "NONE":
            raise ValueError("prepare local sources as mono PCM16 WAV at 16000 Hz")
        total_frames = source.getnframes()
        start = round(start_seconds * 16000)
        if start >= total_frames:
            raise ValueError("sample begins outside source audio")
        source.setpos(start)
        raw = source.readframes(min(round(max_seconds * 16000), total_frames - start))
    samples = array.array("h", raw)
    if sys.byteorder != "little":
        samples.byteswap()
    if len(samples) < 3 * 16000:
        raise ValueError("classifier requires at least three observed seconds")
    coverage = {"coveredSeconds": len(samples) / 16000, "incomplete": True,
                "partialReason": "Only the listed source interval was analyzed; whole-recording coverage is unconfirmed",
                "segments": [{"startSeconds": start / 16000, "endSeconds": (start + len(samples)) / 16000}]}
    return samples, coverage, start == 0 and len(samples) == total_frames


def mean_scores(predictions, class_count):
    """Do not treat absent labels, nonfinite scores, or logits as probabilities."""
    totals = [0.0] * class_count
    count = 0
    for row in predictions:
        if len(row) != class_count:
            raise ValueError("classifier output differs from pinned ordered vocabulary")
        for index, value in enumerate(row):
            value = float(value)
            if not math.isfinite(value) or not 0 <= value <= 1:
                raise ValueError("classifier returned invalid class score")
            totals[index] += value
        count += 1
    if not count:
        raise ValueError("classifier produced no observed patches")
    return [total / count for total in totals]


class EffNet:
    def __init__(self, definitions):
        self.definitions = definitions
        self.encoder = None
        try:
            self.runtime = "essentia-tensorflow/" + importlib.metadata.version("essentia-tensorflow")
            self.runtime += "/cpu" if os.environ.get("CUDA_VISIBLE_DEVICES", "-1") in ("", "-1") else "/cuda"
        except importlib.metadata.PackageNotFoundError as error:
            raise RuntimeError("Install the optional Essentia TensorFlow preparation environment; see docs/discogs-effnet.md") from error

    def load(self):
        # CPU is the portable default; preparation operators can opt into a
        # configured CUDA environment without changing the desktop runtime.
        os.environ.setdefault("CUDA_VISIBLE_DEVICES", "-1")
        os.environ.setdefault("TF_CPP_MIN_LOG_LEVEL", "2")
        os.environ.setdefault("TF_NUM_INTRAOP_THREADS", "2")
        os.environ.setdefault("TF_NUM_INTEROP_THREADS", "1")
        try:
            import essentia.standard as es
            import numpy as np
        except ImportError as error:
            raise RuntimeError("Install the optional Essentia TensorFlow preparation environment; see docs/discogs-effnet.md") from error
        if not hasattr(es, "TensorflowPredictEffnetDiscogs") or not hasattr(es, "TensorflowPredict2D"):
            raise RuntimeError("This Essentia build lacks TensorFlow model support")
        self.numpy = np
        definitions = self.definitions
        encoder = definitions["encoder"]
        self.encoder = es.TensorflowPredictEffnetDiscogs(
            graphFilename=encoder["path"], input=encoder["input"], output=encoder["output"],
            batchSize=64, patchSize=128, patchHopSize=62, lastPatchMode="repeat", lastBatchMode="same")
        self.heads = {kind: es.TensorflowPredict2D(graphFilename=definitions[kind]["path"],
                                                  input=definitions[kind]["input"], output=definitions[kind]["output"])
                      for kind in KINDS}

    def classify(self, samples):
        if self.encoder is None:
            self.load()
        pcm = self.numpy.asarray(samples, dtype=self.numpy.float32) / 32768.0
        embeddings = None
        try:
            # One encoder pass; heads are correlated observations of this same input.
            embeddings = self.encoder(pcm)
            return [{"kind": kind, "model": self.definitions[kind]["model"],
                     "classes": self.definitions[kind]["classes"],
                     "scores": mean_scores(self.heads[kind](embeddings), len(self.definitions[kind]["classes"]))}
                    for kind in KINDS]
        finally:
            pcm.fill(0)
            if embeddings is not None:
                embeddings.fill(0)


def prepare_row(row, audio_root, max_seconds, lock_hash, model, cache):
    path = source_file(audio_root, row)  # recheck changed audio before consulting cache
    key = hashlib.sha256(canonical({"source": row, "lock": lock_hash, "preprocessing": PREPROCESSING,
                                    "runtime": model.runtime, "maxSeconds": max_seconds}).encode()).hexdigest()
    target = cache / (key + ".json")
    samples, coverage, all_source = read_pcm(path, float(row.get("startSeconds", 0)), max_seconds)
    if all_source and row.get("completeRecording") is True:
        coverage["incomplete"] = False
        coverage.pop("partialReason")
    evidence = {"version": 1, "encoder": model.definitions["encoder"]["model"],
                "preprocessing": PREPROCESSING, "runtime": model.runtime,
                "audioSha256": row["audioSha256"], "source": row["source"], "sourceId": row["sourceId"],
                "license": row["derivedLicense"], "coverage": coverage}
    try:
        if target.exists():
            if target.is_symlink() or target.stat().st_size > 2 * 1024 * 1024:
                raise ValueError("classifier checkpoint is linked or oversized")
            result = json.loads(target.read_text(encoding="utf-8"))
            if result.get("preparationKey") != key or result.get("trackId") != row["trackId"] or result.get("catalogSha256") != row["catalogSha256"]:
                raise ValueError("classifier cache identity mismatch")
            validate_checkpoint(result.get("classifierEvidence"), evidence, model.definitions)
            return result, True
        evidence["heads"] = model.classify(samples)
    finally:
        for index in range(len(samples)):
            samples[index] = 0
    result = {"catalogSha256": row["catalogSha256"], "trackId": row["trackId"], "classifierEvidence": [evidence], "preparationKey": key}
    # Atomic per-record checkpoints survive interruption without truncated JSONL.
    atomic_json(target, result)
    return result, False


def validate_checkpoint(cached, expected, definitions):
    """A matching lookup key alone cannot authenticate cached observations."""
    if not isinstance(cached, list) or len(cached) != 1 or not isinstance(cached[0], dict):
        raise ValueError("classifier checkpoint evidence shape mismatch")
    actual = dict(cached[0])
    heads = actual.pop("heads", None)
    if actual != expected:
        raise ValueError("classifier checkpoint source/model/coverage mismatch")
    expected_kinds = set(definitions) - {"encoder"}
    if not isinstance(heads, list) or len(heads) != len(expected_kinds):
        raise ValueError("classifier checkpoint heads mismatch")
    for head in heads:
        if not isinstance(head, dict) or head.get("kind") not in expected_kinds:
            raise ValueError("classifier checkpoint head kind mismatch")
        kind = head["kind"]
        expected_kinds.remove(kind)
        contract = {"kind": kind, "model": definitions[kind]["model"], "classes": definitions[kind]["classes"]}
        actual_head = dict(head)
        scores = actual_head.pop("scores", None)
        if actual_head != contract or not isinstance(scores, list) or len(scores) != len(contract["classes"]):
            raise ValueError("classifier checkpoint ordered vocabulary mismatch")
        if any(isinstance(v, bool) or not isinstance(v, (int, float)) or not math.isfinite(v) or not 0 <= v <= 1 for v in scores):
            raise ValueError("classifier checkpoint scores invalid")


def atomic_json(target, value):
    with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", dir=target.parent, delete=False) as stream:
        temporary = Path(stream.name)
        try:
            stream.write(canonical(value) + "\n")
        except BaseException:
            temporary.unlink(missing_ok=True)
            raise
    try:
        temporary.replace(target)
    finally:
        temporary.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    setup = sub.add_parser("setup", help="Fetch and verify the pinned original graphs and metadata")
    setup.add_argument("--models", type=Path, required=True)
    setup.add_argument("--verify-only", action="store_true")
    prepare = sub.add_parser("prepare", help="Analyze licensed local WAVs and emit exact-track classifier JSONL")
    prepare.add_argument("--models", type=Path, required=True)
    prepare.add_argument("--manifest", type=Path, required=True)
    prepare.add_argument("--audio-root", type=Path, required=True)
    prepare.add_argument("--cache", type=Path, required=True)
    prepare.add_argument("--out", type=Path, required=True)
    prepare.add_argument("--max-seconds", type=float, default=60)
    prepare.add_argument("--max-records", type=int, default=1000)
    args = parser.parse_args()
    lock, lock_hash = read_lock()
    if args.command == "setup":
        for asset in lock["assets"]:
            fetch(args.models, asset, args.verify_only)
        print(f"Verified {len(lock['assets'])} original model/metadata/notice assets; noncommercial terms in {args.models / 'LICENSE'}")
        return
    if not 1 <= args.max_records <= 100000 or not 3 <= args.max_seconds <= 60:
        raise ValueError("preparation requires 1–100000 records and 3–60 seconds per source")
    definitions = model_definitions(args.models, lock)
    model = EffNet(definitions)
    args.cache.mkdir(parents=True, exist_ok=True)
    args.out.parent.mkdir(parents=True, exist_ok=True)
    temporary = args.out.with_suffix(args.out.suffix + ".part")
    if temporary.is_symlink() or args.out.is_symlink():
        raise ValueError("output must not be a symbolic link")
    count = hits = 0
    seen = set()
    with args.manifest.open(encoding="utf-8") as source, temporary.open("w", encoding="utf-8") as output:
        for line in source:
            if not line.strip():
                continue
            if count == args.max_records:
                break
            if len(line) > 65536:
                raise ValueError("source manifest row too large")
            row = json.loads(line)
            if row["trackId"] in seen:
                raise ValueError("source manifest repeats catalog trackId")
            seen.add(row["trackId"])
            result, cached = prepare_row(row, args.audio_root, args.max_seconds, lock_hash, model, args.cache)
            output.write(canonical(result) + "\n")
            count += 1
            hits += int(cached)
    os.replace(temporary, args.out)
    print(f"Prepared {count} exact-track rows ({hits} cache hits); outputs remain uncalibrated estimates")


if __name__ == "__main__":
    main()
