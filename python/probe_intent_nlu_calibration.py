"""Expand a frozen pilot's diagnostic sample without approving new annotations.

Reads approved training/calibration partitions only for provenance/leakage checks;
never reads evaluation.jsonl, trains, or writes an activation candidate. Newly
authored labels remain unreviewed. Threshold scans are exploratory diagnostics.
"""
from __future__ import annotations

import argparse
from collections import Counter
import json
from pathlib import Path
import platform
import statistics
import unicodedata

from calibrate_intent_nlu import DEFAULT_THRESHOLDS, decode_proposals, infer_cases, load_calibration, scan_thresholds, validate_head
from prepare_intent_nlu_data import IDENTITY_KINDS, digest, read_records, validate_record, write_json
from train_intent_nlu import load_split


def identities(row: dict) -> set[str]:
    return {unicodedata.normalize("NFKC", str(span[key])).casefold()
            for span in row["spans"] if span["kind"] in IDENTITY_KINDS
            for key in ("text", "canonical", "candidateCanonical") if span.get(key)}


def validate_expansion(rows: list[dict], existing: list[dict], reserved_ids: set[str], labels: list[str]) -> None:
    """A fresh authored probe cannot masquerade as reviewed or repeat old data."""
    if not rows:
        raise ValueError("Nonempty diagnostic expansion required")
    ids = set(reserved_ids)
    texts = {row["prompt"].strip().casefold() for row in existing}
    groups = {row["group"] for row in existing}
    known_identities = set().union(*(identities(row) for row in existing))
    for row in rows:
        validate_record(row)
        if row["review"]["status"] != "unreviewed":
            raise ValueError("Diagnostic expansion must retain unreviewed status; use reviewed calibration tooling for approved data")
        if row["id"] in ids or row["prompt"].strip().casefold() in texts:
            raise ValueError("Duplicate or existing prompt/ID in diagnostic expansion")
        if row["group"] in groups or identities(row) & known_identities:
            raise ValueError("Expansion reuses a training/calibration identity or paraphrase group")
        ids.add(row["id"])
        texts.add(row["prompt"].strip().casefold())
        previous_end = 0
        for span in sorted(row["spans"], key=lambda span: span["start"]):
            if span["start"] < previous_end:
                raise ValueError("Overlapping role supervision")
            previous_end = span["end"]
            if "B-" + span["kind"] + ":" + span["role"] not in labels:
                raise ValueError("Expansion role is absent from the fixed head vocabulary")


def diagnostic_scan(cases: list[dict], labels: list[str]) -> dict:
    report = scan_thresholds(cases, labels, DEFAULT_THRESHOLDS, .99, 20)
    # A successful exploratory scan must not become importable calibration.
    report["exploratoryThresholdMeetingObservedTarget"] = report.pop("suggestedThreshold")
    report.update({"calibrationEligible": False, "productionReady": False,
                   "labelReview": "contains agent-authored unreviewed diagnostic labels"})
    return report


def case_results(cases: list[dict], labels: list[str], threshold: float = .5) -> list[dict]:
    output = []
    for case in cases:
        proposals = decode_proposals(case["text"], case["tokens"], case["logits"], labels, threshold)
        gold = {(s["start"], s["end"], s["kind"] + ":" + s["role"]) for s in case["gold"]}
        predicted = {(s["start"], s["end"], s["label"]) for s in proposals}
        for proposal in proposals:
            proposal["correct"] = (proposal["start"], proposal["end"], proposal["label"]) in gold
        output.append({"id": case["id"], "prompt": case["text"], "threshold": threshold,
                       "proposals": proposals, "gold": case["gold"],
                       "missed": [s for s in case["gold"] if (s["start"], s["end"], s["kind"] + ":" + s["role"]) not in predicted]})
    return output


def compare_source_quantities(results: list[dict], source: list[dict]) -> dict:
    """Compare explicit count/duration role spans only, not whole-parser quality."""
    sources = {row["id"]: row for row in source}
    if len(sources) != len(source) or set(sources) != {row["id"] for row in results}:
        raise ValueError("Source report IDs do not match expansion")
    totals = {role: {"gold": 0, "sourceCorrect": 0, "sourceAccepted": 0, "distilbertCorrect": 0,
                     "distilbertAccepted": 0, "distilbertCorrectBeyondSource": 0} for role in ("count", "duration")}
    for result in results:
        saved = sources[result["id"]]
        if saved["prompt"] != result["prompt"]:
            raise ValueError("Source report prompt differs from diagnostic source")
        atoms = saved["translation"].get("atoms") or []
        for role, counts in totals.items():
            gold = [s for s in result["gold"] if s["kind"] == "quantity" and s["role"] == role]
            # BIO roles do not encode polarity/strength; neither side can earn
            # an advantage by scoring a different polarity/strength subset.
            selected = [a for a in atoms if a["kind"] == role]
            def covers(atom, span):
                return any(e["start"] <= span["start"] and e["end"] >= span["end"] for e in atom.get("evidence", []))
            # Source evidence may include the count noun, so exact boundaries
            # differ. Match only unambiguous one-to-one gold spans; a broad
            # atom covering two alternative quantities proves neither choice.
            matched = set()
            for atom in selected:
                covered = [i for i, span in enumerate(gold) if covers(atom, span)]
                if len(covered) == 1:
                    matched.add(covered[0])
            source_correct = [gold[i] for i in matched]
            proposed = [p for p in result["proposals"] if p["label"] == "quantity:" + role]
            correct = [p for p in proposed if p["correct"]]
            counts["gold"] += len(gold)
            counts["sourceCorrect"] += len(source_correct)
            counts["sourceAccepted"] += len(selected)
            counts["distilbertCorrect"] += len(correct)
            counts["distilbertAccepted"] += len(proposed)
            counts["distilbertCorrectBeyondSource"] += sum(not any(s["start"] == p["start"] and s["end"] == p["end"] for s in source_correct) for p in correct)
    return {"scope": "count/duration source role coverage; not numeric-value, polarity, strength or whole-intent accuracy",
            "roles": totals, "sourceMedianMs": statistics.median(row["elapsedMs"] for row in source)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for arg in ("data", "training", "export", "input", "output"):
        parser.add_argument("--" + arg, type=Path, required=True)
    parser.add_argument("--threads", type=int, default=2)
    parser.add_argument("--source-report", type=Path, help="Optional actual Go source-probe output for the expansion")
    args = parser.parse_args()
    if args.output.exists() or args.threads < 1:
        parser.error("Use a fresh output directory and positive thread count")
    exported = getattr(args, "export")
    head, hashes = validate_head(exported)
    original, provenance = load_calibration(args.data, args.training, exported)
    manifest = json.loads((args.data / "data-manifest.json").read_text(encoding="utf-8-sig"))
    train = load_split(args.data, "train", manifest)
    expansion = read_records(args.input)
    validate_expansion(expansion, train + original, set(manifest["assignment"]), head["labels"])
    # Fixed cased tokenizer alignment also rejects annotations bisecting tokens.
    from transformers import BertTokenizerFast
    from train_intent_nlu import align_labels
    tokenizer = BertTokenizerFast(vocab_file=str(exported / "vocab.txt"), do_lower_case=False, strip_accents=False, tokenize_chinese_chars=True)
    label_ids = {label: index for index, label in enumerate(head["labels"])}
    for row in expansion:
        encoded = tokenizer(row["prompt"], return_offsets_mapping=True, truncation=False)
        align_labels(row, encoded["offset_mapping"], label_ids)
    cases, execution = infer_cases(original + expansion, exported, head, args.threads)
    prior, extra = cases[:len(original)], cases[len(original):]
    report = {"version": 1, "purpose": "fixed-pilot expanded calibration diagnostic",
              "hashes": hashes, "provenance": {**provenance, "expansionSHA256": digest(args.input)},
              "approvedExamples": len(original), "unreviewedExamples": len(expansion),
              "evaluationRead": False, "trained": False, "calibrationEligible": False,
              "productionReady": False, "fixedComparisonThreshold": .5,
              "originalApproved": scan_thresholds(prior, head["labels"], DEFAULT_THRESHOLDS, .99, 20),
              "expansion": diagnostic_scan(extra, head["labels"]),
              "combined": diagnostic_scan(cases, head["labels"]),
              "byFamily": {group: diagnostic_scan([case for case in extra if case["id"] in {row["id"] for row in expansion if row["group"] == group}], head["labels"])
                           for group in sorted({row["group"] for row in expansion})},
              "trainingRoleSpans": dict(Counter(span["kind"] + ":" + span["role"] for row in train for span in row["spans"])),
              "execution": {**execution, "platform": platform.platform(), "python": platform.python_version(),
                            "medianTokenizeAndInferMs": statistics.median(execution["tokenizeAndInferMs"]),
                            "p95TokenizeAndInferMs": sorted(execution["tokenizeAndInferMs"])[max(0, int(.95 * len(cases)) - 1)],
                            "modelBytes": (exported / "model.onnx").stat().st_size},
              "limitations": ["New diagnostic annotations are not approved by the project user",
                              "Authored families and correlated spans are not a representative independent sample",
                              "Increasing calibration size measures a fixed model; it does not train new capabilities",
                              "Role spans do not measure whole intent, relations, playlist length or listening quality"]}
    args.output.mkdir(parents=True)
    if args.source_report:
        source = json.loads(args.source_report.read_text(encoding="utf-8-sig"))
        report["sourceQuantityComparison"] = compare_source_quantities(case_results(extra, head["labels"]), source)
        report["provenance"]["sourceReportSHA256"] = digest(args.source_report)
    write_json(args.output / "diagnostic-report.json", report)
    write_json(args.output / "case-results.json", case_results(cases, head["labels"]))
    print(json.dumps({"approved": len(original), "additionalUnreviewed": len(expansion),
                      "combinedAtFixedThreshold": report["combined"]["scan"][0],
                      "calibrationEligible": False}, indent=2))


if __name__ == "__main__":
    main()
