#!/usr/bin/env python3
"""Fit a literal-criterion cosine threshold on independently labelled examples.

Input JSON: modelFingerprint, kind, criterion, developmentSet, validationSet,
version, examples [{recordingId, split: development|validation, score,
label: true|false|null}]. Null means unknown and is never a negative label.
Output is an AudioSimilarityCalibration JSON object for reviewed code injection.
This does not activate Automatic or modify an installed model/catalog.
"""
import argparse
import json
import math
from pathlib import Path


def calibrate(data):
    required = ("modelFingerprint", "kind", "criterion", "developmentSet", "validationSet", "version")
    if any(not isinstance(data.get(key), str) or not data[key].strip() for key in required):
        raise ValueError("Nonempty fingerprint, criterion, version and dataset identities are required")
    if data["developmentSet"] == data["validationSet"]:
        raise ValueError("Development and validation datasets must be independent")
    groups = {"development": [], "validation": []}
    seen = set()
    for example in data["examples"]:
        identity, split = example["recordingId"], example["split"]
        if not isinstance(identity, str) or not identity or identity in seen:
            raise ValueError("Each recording identity must appear once, without split leakage")
        seen.add(identity)
        if split not in groups:
            raise ValueError("Unknown split")
        label, score = example["label"], example["score"]
        if label is not None and type(label) is not bool:
            raise ValueError("Labels must be true, false or null (unknown)")
        if type(score) not in (int, float) or not math.isfinite(score) or not -1 <= score <= 1:
            raise ValueError("Scores must be finite cosines in [-1, 1]")
        if label is not None:
            groups[split].append((score, label))
    for rows in groups.values():
        if not any(label for _, label in rows) or not any(not label for _, label in rows):
            raise ValueError("Both splits need positive examples and labelled negative confounders")

    def metrics(rows, threshold):
        positives = sum(label for _, label in rows)
        selected = [label for score, label in rows if score >= threshold]
        tp = sum(selected)
        return (tp / len(selected) if selected else 0), tp / positives

    # Select using development labels only. Validation is applied once below;
    # a failed validation is not an invitation to tune against held-out labels.
    candidates = []
    for threshold in sorted({score for score, _ in groups["development"]}):
        precision, recall = metrics(groups["development"], threshold)
        if precision >= .90:
            candidates.append((recall, precision, -threshold))
    if not candidates:
        raise ValueError("No development threshold meets 90% precision; criterion remains unknown")
    threshold = -max(candidates)[2]
    precision, recall = metrics(groups["validation"], threshold)
    if precision < .90 or recall <= 0:
        raise ValueError("Frozen threshold failed validation; criterion remains unknown")
    return dict({key: data[key] for key in required}, minimumScore=threshold,
                validationPrecision=precision, validationRecall=recall,
                positiveExamples=sum(label for _, label in groups["validation"]),
                negativeExamples=sum(not label for _, label in groups["validation"]))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("examples", type=Path)
    args = parser.parse_args()
    try:
        print(json.dumps(calibrate(json.loads(args.examples.read_text())), indent=2, allow_nan=False))
    except (ValueError, KeyError, TypeError) as error:
        parser.exit(1, f"Calibration rejected: {error}\n")
