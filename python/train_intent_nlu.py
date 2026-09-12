"""Offline English DistilBERT cased token-role pilot, using reviewed prompts only.

This first pilot learns BIO mention/operator roles, not complete relations or
playlist suitability. It never promotes a model or creates reviewed calibration.
"""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import random

from prepare_intent_nlu_data import digest, validate_record, verify_model_source, write_json


def load_split(root: Path, name: str, manifest: dict) -> list[dict]:
    path = root / (name + ".jsonl")
    if digest(path) != manifest["files"][path.name]["sha256"]:
        raise ValueError(f"Prepared split checksum mismatch: {name}")
    rows = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
    for row in rows:
        validate_record(row, require_reviewed=True)
        if manifest["assignment"].get(row["id"]) != name:
            raise ValueError("Split assignment mismatch")
    if not rows:
        raise ValueError(f"Empty {name} split")
    return rows


def align_labels(row: dict, offsets: list, labels: dict[str, int]) -> list[int]:
    """Map HF character offsets to source-byte spans without normalizing names."""
    boundaries = [0]
    for char in row["prompt"]:
        boundaries.append(boundaries[-1] + len(char.encode("utf-8")))
    output, previous = [], None
    covered = set()
    for start, end in offsets:
        if start == end:
            output.append(-100)
            previous = None
            continue
        left, right = boundaries[start], boundaries[end]
        matches = [span for span in row["spans"] if left < span["end"] and right > span["start"]]
        if len(matches) > 1:
            raise ValueError(f"{row['id']}: overlapping supervision cannot be represented by one token-role head")
        if not matches:
            output.append(labels["O"])
            previous = None
            continue
        span = matches[0]
        if not span["start"] <= left < right <= span["end"]:
            raise ValueError(f"{row['id']}: annotation bisects a model token")
        prefix = "I-" if previous == span["id"] else "B-"
        output.append(labels[prefix + span["kind"] + ":" + span["role"]])
        previous = span["id"]
        covered.add(previous)
    if covered != {span["id"] for span in row["spans"]}:
        raise ValueError(f"{row['id']}: missing annotations after tokenization (truncation is forbidden)")
    return output


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data", type=Path, required=True)
    parser.add_argument("--source", type=Path, required=True, help="Verified local distilbert archive; no Hub downloads")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--epochs", type=int, default=5)
    parser.add_argument("--batch-size", type=int, default=4)
    parser.add_argument("--learning-rate", type=float, default=3e-5)
    parser.add_argument("--seed", type=int, default=42)
    parser.add_argument("--threads", type=int, default=4)
    args = parser.parse_args()
    if args.epochs < 1 or args.batch_size < 1 or args.threads < 1 or not 0 < args.learning_rate < 1:
        parser.error("Invalid bounded training parameters")
    manifest = json.loads((args.data / "data-manifest.json").read_text())
    if manifest.get("version") != 1 or manifest.get("reviewRequired") is not True:
        raise ValueError("Reviewed preparation manifest required")
    train = load_split(args.data, "train", manifest)
    calibration = load_split(args.data, "calibration", manifest)
    # Deliberately never read evaluation.jsonl.
    label_path = args.data / "labels.json"
    if digest(label_path) != manifest["files"][label_path.name]["sha256"]:
        raise ValueError("Label vocabulary checksum mismatch")
    labels = json.loads(label_path.read_text())
    label_ids = {label: index for index, label in enumerate(labels)}
    source_verification = verify_model_source(args.source)
    import torch
    from transformers import AutoConfig, AutoModelForTokenClassification, AutoTokenizer

    torch.set_num_threads(args.threads)
    torch.manual_seed(args.seed)
    random.seed(args.seed)
    torch.use_deterministic_algorithms(True)
    config = AutoConfig.from_pretrained(args.source, local_files_only=True)
    if config.model_type != "distilbert":
        raise ValueError("The pilot requires the archived DistilBERT encoder")
    tokenizer = AutoTokenizer.from_pretrained(args.source, local_files_only=True, use_fast=True)
    if not tokenizer.is_fast or getattr(tokenizer, "do_lower_case", False):
        raise ValueError("Cased fast tokenizer required")
    config.num_labels = len(labels)
    config.id2label = dict(enumerate(labels))
    config.label2id = label_ids
    model = AutoModelForTokenClassification.from_pretrained(args.source, config=config, local_files_only=True, ignore_mismatched_sizes=True).cpu()

    def encode(row):
        encoded = tokenizer(row["prompt"], return_offsets_mapping=True, truncation=False)
        if len(encoded["input_ids"]) > 512:
            raise ValueError(f"{row['id']}: exceeds 512 tokens; split/review instead of truncating")
        target = align_labels(row, encoded.pop("offset_mapping"), label_ids)
        return {"input_ids": encoded["input_ids"], "attention_mask": encoded["attention_mask"], "labels": target}

    training = [encode(row) for row in train]
    validation = [encode(row) for row in calibration]

    def batch(rows):
        length = max(len(row["input_ids"]) for row in rows)
        return {key: torch.tensor([row[key] + [pad] * (length - len(row[key])) for row in rows], dtype=torch.long) for key, pad in (("input_ids", tokenizer.pad_token_id), ("attention_mask", 0), ("labels", -100))}

    args.output.mkdir(parents=True, exist_ok=False)
    optimizer = torch.optim.AdamW(model.parameters(), lr=args.learning_rate)
    history, best = [], float("inf")
    for epoch in range(args.epochs):
        model.train()
        order = list(range(len(training)))
        random.Random(args.seed + epoch).shuffle(order)
        losses = []
        for offset in range(0, len(order), args.batch_size):
            values = batch([training[index] for index in order[offset:offset + args.batch_size]])
            optimizer.zero_grad(set_to_none=True)
            loss = model(**values).loss
            loss.backward()
            torch.nn.utils.clip_grad_norm_(model.parameters(), 1)
            optimizer.step()
            losses.append(float(loss.detach()))
        model.eval()
        validation_losses = []
        with torch.inference_mode():
            for offset in range(0, len(validation), args.batch_size):
                validation_losses.append(float(model(**batch(validation[offset:offset + args.batch_size])).loss))
        observed = {"epoch": epoch + 1, "trainLoss": sum(losses) / len(losses), "calibrationLoss": sum(validation_losses) / len(validation_losses)}
        history.append(observed)
        if observed["calibrationLoss"] < best:
            best = observed["calibrationLoss"]
            model.save_pretrained(args.output / "checkpoint", safe_serialization=True)
            tokenizer.save_pretrained(args.output / "checkpoint")
        print(json.dumps(observed), flush=True)
    checkpoint_files = {path.name: digest(path) for path in sorted((args.output / "checkpoint").iterdir()) if path.is_file()}
    write_json(args.output / "training-manifest.json", {"version": 1, "task": "BIO-token-role-pilot/v1", "dataManifestSHA256": digest(args.data / "data-manifest.json"), "source": str(args.source.resolve()), "sourceVerification": source_verification, "checkpointFiles": checkpoint_files, "seed": args.seed, "epochs": args.epochs, "trainExamples": len(train), "calibrationExamples": len(calibration), "evaluationRead": False, "history": history, "productionReady": False, "limitations": ["No learned relation, scope or strength head", "Validation loss does not establish semantic calibration", "No independent playlist-quality evaluation"]})


if __name__ == "__main__":
    main()
