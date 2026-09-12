"""Offline threshold scan for reviewed token-role annotations; never activates NLU.

Only the prepared calibration split is read. Exact UTF-8 spans and BIO roles
are scored, not musical suitability, relation understanding or playlist quality.
A suggested threshold is based on observed precision; uncertainty is reported
separately and the resulting candidate always has reviewed:false.
"""
from __future__ import annotations

import argparse
import json
import math
from pathlib import Path
import unicodedata

from prepare_intent_nlu_data import digest, write_json
from train_intent_nlu import load_split
from verify_intent_nlu_parity import byte_boundaries

DEFAULT_THRESHOLDS = [.5, .6, .7, .75, .8, .85, .9, .925, .95, .975, .99, .995, .999]


def validate_head(root: Path) -> tuple[dict, dict]:
    """Validate the model/label/tokenizer contract accepted by native readHead."""
    head_path = root / "nlu-head.json"
    head = json.loads(head_path.read_text(encoding="utf-8-sig"))
    if head.get("version") != 1 or head.get("outputName") != "logits" or type(head.get("maxTokens")) is not int or not 3 <= head["maxTokens"] <= 512:
        raise ValueError("Incompatible native token-head contract")
    labels = head.get("labels", [])
    if not isinstance(labels, list) or not 2 <= len(labels) <= 256 or any(not isinstance(label, str) for label in labels) or len(set(labels)) != len(labels) or "O" not in labels:
        raise ValueError("Invalid native BIO label vocabulary")
    for label in labels:
        if len(label) > 100 or label != "O" and (len(label) < 3 or label[:2] not in {"B-", "I-"}):
            raise ValueError("Invalid native BIO label")
        if label.startswith("I-") and "B-" + label[2:] not in labels:
            raise ValueError("Native BIO I-label lacks a B-label")
    hashes = {"headSHA256": digest(head_path)}
    for key, filename in (("modelSHA256", "model.onnx"), ("tokenizerSHA256", "vocab.txt"), ("configSHA256", "config.json")):
        hashes[key] = digest(root / filename)
        if hashes[key] != head.get(key):
            raise ValueError(f"Export checksum mismatch: {filename}")
    config = json.loads((root / "config.json").read_text(encoding="utf-8-sig"))
    if config.get("model_type") != "distilbert" or config.get("dim") != 768:
        raise ValueError("Native intent model requires cased DistilBERT dim=768")
    return head, hashes


def inside_word(raw: bytes, index: int) -> bool:
    if not 0 < index < len(raw):
        return False
    left, right = raw[:index].decode("utf-8")[-1], raw[index:].decode("utf-8")[0]

    def word(char):
        category = unicodedata.category(char)
        return category[0] in {"L", "N"} or category == "Mn"

    return word(left) and word(right)


def decode_proposals(text: str, tokens: list[dict], logits: list[list[float]], labels: list[str], threshold: float) -> list[dict]:
    """Mirror native DecodeProposals, including orphan/low-confidence I handling."""
    if not .5 <= threshold < 1 or len(tokens) != len(logits) or len(labels) < 2:
        raise ValueError("Invalid proposal shape or threshold")
    raw = text.encode("utf-8")
    output, active = [], None

    def flush():
        nonlocal active
        if active is not None:
            if not inside_word(raw, active["start"]) and not inside_word(raw, active["end"]):
                active["text"] = raw[active["start"]:active["end"]].decode("utf-8")
                output.append(active)
            active = None

    for token, values in zip(tokens, logits):
        if len(values) != len(labels):
            raise ValueError("Invalid class dimension")
        if token["special"]:
            flush()
            continue
        start, end = token["start"], token["end"]
        if type(start) is not int or type(end) is not int or not 0 <= start < end <= len(raw):
            raise ValueError("Invalid source byte span")
        try:
            raw[:start].decode("utf-8")
            raw[:end].decode("utf-8")
        except UnicodeDecodeError as exc:
            raise ValueError("Source span splits a Unicode character") from exc
        if not all(math.isfinite(value) for value in values):
            raise ValueError("Nonfinite logits")
        # max() retains the first index on ties, as the Go decoder does.
        best = max(range(len(values)), key=values.__getitem__)
        score = 1 / sum(math.exp(value - values[best]) for value in values)
        label = labels[best]
        if score < threshold:
            if active is not None and label == "I-" + active["label"]:
                active = None
            flush()
            continue
        if label == "O":
            flush()
            continue
        if len(label) < 3 or label[:2] not in {"B-", "I-"}:
            raise ValueError("Invalid token label")
        if label.startswith("B-"):
            flush()
            active = {"label": label[2:], "start": start, "end": end, "score": score}
        elif active is not None and active["label"] == label[2:] and start >= active["end"]:
            active["end"] = end
            active["score"] = min(active["score"], score)
        else:
            flush()
    flush()
    return output


def wilson_lower(correct: int, total: int, z: float = 1.959963984540054) -> float | None:
    """Lower endpoint of a 95% two-sided Wilson interval; undefined for n=0."""
    if total == 0:
        return None
    proportion = correct / total
    z2 = z * z
    return max(0., (proportion + z2 / (2 * total) - z * math.sqrt(proportion * (1 - proportion) / total + z2 / (4 * total * total))) / (1 + z2 / total))


def scan_thresholds(cases: list[dict], labels: list[str], thresholds: list[float], target_precision: float, minimum_accepted: int) -> dict:
    if not cases or not 0 < target_precision <= 1 or minimum_accepted < 1:
        raise ValueError("Nonempty calibration cases and valid observed target required")
    rows = []
    for threshold in sorted(set(thresholds)):
        correct = accepted = gold_count = covered = complete = 0
        per_label = {}
        for case in cases:
            gold = {(span["start"], span["end"], span["kind"] + ":" + span["role"]) for span in case["gold"]}
            proposals = decode_proposals(case["text"], case["tokens"], case["logits"], labels, threshold)
            predicted = {(span["start"], span["end"], span["label"]) for span in proposals}
            true = gold & predicted
            correct += len(true)
            accepted += len(predicted)
            gold_count += len(gold)
            covered += bool(predicted)
            complete += predicted == gold
            for start, end, label in gold | predicted:
                counts = per_label.setdefault(label, {"accepted": 0, "correct": 0, "gold": 0})
                key = (start, end, label)
                counts["accepted"] += key in predicted
                counts["correct"] += key in true
                counts["gold"] += key in gold
        precision = correct / accepted if accepted else None
        row = {"threshold": threshold, "acceptedSpans": accepted, "correctSpans": correct, "incorrectSpans": accepted - correct, "goldSpans": gold_count, "missedSpans": gold_count - correct, "observedPrecision": precision, "recall": correct / gold_count if gold_count else None, "acceptedErrorRate": (accepted - correct) / accepted if accepted else None, "precisionWilson95Lower": wilson_lower(correct, accepted), "examplesWithAcceptedSpans": covered, "examplesWithoutAcceptedSpans": len(cases) - covered, "exampleCoverage": covered / len(cases), "exactRoleSpanExamples": complete, "perLabel": per_label, "meetsObservedTarget": accepted >= minimum_accepted and precision is not None and precision >= target_precision}
        rows.append(row)
    eligible = [row for row in rows if row["meetsObservedTarget"]]
    selected = max(eligible, key=lambda row: (row["correctSpans"], row["exampleCoverage"], -row["threshold"])) if eligible else None
    return {"version": 1, "decoder": "native-BIO-whole-word-min-confidence/v1", "validationExamples": len(cases), "observedPrecisionTarget": target_precision, "minimumAcceptedSpans": minimum_accepted, "suggestedThreshold": selected["threshold"] if selected else None, "scan": rows, "reviewed": False, "evaluationRead": False, "limitations": ["Calibration split is used for threshold selection and is not an independent quality estimate", "Observed precision target is not a confidence-bound guarantee", "Wilson interval treats accepted spans as independent; spans within prompts may be correlated", "Token-role span correctness does not measure relations, scope, strength or musical quality", "Full-request and independent evaluation remain required before activation"]}


def load_calibration(data: Path, training: Path, exported: Path) -> tuple[list[dict], dict]:
    manifest_path = data / "data-manifest.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8-sig"))
    if manifest.get("version") != 1 or manifest.get("reviewRequired") is not True:
        raise ValueError("Reviewed preparation manifest required")
    training_path = training / "training-manifest.json"
    training_record = json.loads(training_path.read_text(encoding="utf-8-sig"))
    if training_record.get("dataManifestSHA256") != digest(manifest_path):
        raise ValueError("Calibration data is not bound to the training manifest")
    parity_path = exported / "export-parity.json"
    parity = json.loads(parity_path.read_text(encoding="utf-8-sig"))
    if parity.get("trainingManifestSHA256") != digest(training_path):
        raise ValueError("Export is not bound to the supplied training manifest")
    if parity.get("modelSHA256") != digest(exported / "model.onnx"):
        raise ValueError("Export model checksum differs from export parity report")
    rows = load_split(data, "calibration", manifest)
    if len({row["id"] for row in rows}) != len(rows):
        raise ValueError("Duplicate calibration example IDs")
    return rows, {"dataManifestSHA256": digest(manifest_path), "calibrationSHA256": digest(data / "calibration.jsonl"), "trainingManifestSHA256": digest(training_path), "exportParitySHA256": digest(parity_path), "calibrationIDs": [row["id"] for row in rows]}


def candidate_metadata(hashes: dict, report: dict) -> dict:
    return {"version": 1, **hashes, "reviewed": False, "threshold": report["suggestedThreshold"], "validationExamples": report["validationExamples"], "note": "Threshold suggestion from reviewed calibration labels only; no human approval or activation. Independent semantic evaluation and review are still required."}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data", type=Path, required=True)
    parser.add_argument("--training", type=Path, required=True)
    parser.add_argument("--export", dest="exported", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--thresholds", default=",".join(map(str, DEFAULT_THRESHOLDS)))
    parser.add_argument("--target-precision", type=float, default=.99, help="Observed target only, not a claim that the lower confidence bound reaches this value")
    parser.add_argument("--minimum-accepted", type=int, default=20)
    parser.add_argument("--threads", type=int, default=2)
    args = parser.parse_args()
    thresholds = [float(value) for value in args.thresholds.split(",")]
    if not thresholds or any(not math.isfinite(value) or not .5 <= value < 1 for value in thresholds) or args.threads < 1:
        parser.error("Thresholds must be finite values in [0.5,1); threads must be positive")
    if not 0 < args.target_precision <= 1 or args.minimum_accepted < 1:
        parser.error("Invalid observed precision target or minimum accepted count")
    if args.output.exists():
        parser.error("Use a fresh output directory")
    head, hashes = validate_head(args.exported)
    rows, provenance = load_calibration(args.data, args.training, args.exported)
    import numpy as np
    import onnxruntime as ort
    from transformers import BertTokenizerFast

    # Native uses a fixed cased BERT normalizer and verified vocab.txt. Do not
    # silently use an unhashed tokenizer.json with potentially different rules.
    tokenizer = BertTokenizerFast(vocab_file=str(args.exported / "vocab.txt"), do_lower_case=False, strip_accents=False, tokenize_chinese_chars=True)
    options = ort.SessionOptions()
    options.intra_op_num_threads = args.threads
    options.inter_op_num_threads = 1
    session = ort.InferenceSession(str(args.exported / "model.onnx"), sess_options=options, providers=["CPUExecutionProvider"])
    if {item.name for item in session.get_inputs()} != {"input_ids", "attention_mask"}:
        raise ValueError("Unexpected ONNX input contract")
    cases = []
    for row in rows:
        encoded = tokenizer(row["prompt"], return_offsets_mapping=True, return_special_tokens_mask=True, truncation=False)
        if len(encoded["input_ids"]) > head["maxTokens"]:
            raise ValueError(f"{row['id']}: model input limit; do not truncate calibration prompts")
        boundaries = byte_boundaries(row["prompt"])
        tokens = [{"start": boundaries[start], "end": boundaries[end], "special": bool(special)} for (start, end), special in zip(encoded["offset_mapping"], encoded["special_tokens_mask"])]
        values = {name: np.asarray([encoded[name]], dtype=np.int64) for name in ("input_ids", "attention_mask")}
        logits = session.run([head["outputName"]], values)[0]
        if logits.shape != (1, len(tokens), len(head["labels"])) or not np.isfinite(logits).all():
            raise ValueError("Unexpected/nonfinite ONNX logits")
        cases.append({"id": row["id"], "text": row["prompt"], "gold": row["spans"], "tokens": tokens, "logits": logits[0].tolist()})
    report = scan_thresholds(cases, head["labels"], thresholds, args.target_precision, args.minimum_accepted)
    report.update({"hashes": hashes, "provenance": provenance, "runtime": ort.__version__, "nativeExecution": False, "calibrationLabelReview": "approved input records", "calibrationApproval": "pending"})
    args.output.mkdir(parents=True, exist_ok=False)
    write_json(args.output / "calibration-report.json", report)
    candidate = candidate_metadata(hashes, report)
    candidate["reportSHA256"] = digest(args.output / "calibration-report.json")
    write_json(args.output / "calibration-candidate.json", candidate)
    print(json.dumps({"validationExamples": len(rows), "suggestedThreshold": report["suggestedThreshold"], "reviewed": False, "evaluationRead": False, "productionReady": False}, indent=2))


if __name__ == "__main__":
    main()
