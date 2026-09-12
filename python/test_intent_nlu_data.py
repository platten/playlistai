"""Offline data/Unicode/provenance regressions; no real review approval."""
import copy
import json
from pathlib import Path
import tempfile
import unittest

from prepare_intent_nlu_data import grouped_splits, prepare, read_records, validate_record, write_json
from train_intent_nlu import align_labels, load_split
from verify_intent_nlu_parity import byte_boundaries, compare


def fixture(identity="Alpha", group="g1"):
    text = "🎵 " + identity
    return {"id": group, "group": group, "prompt": text, "interpretation": "Synthetic unit fixture only", "spans": [{"id": "s1", "text": identity, "start": 5, "end": len(text.encode()), "kind": "artist", "role": "similarity", "polarity": "positive", "strength": "preferred", "scope": "playlist"}], "relations": [], "review": {"status": "approved", "reviewer": "synthetic-test-fixture", "reviewedAt": "2026-01-01T00:00:00Z"}}


class DataTests(unittest.TestCase):
    def test_real_review_batch_is_valid_but_training_blocked(self):
        path = Path(__file__).parents[1] / "internal/evaluation/testdata/intent-nlu-review-v1.json"
        rows = read_records(path)
        self.assertEqual(len(rows), 40)
        for row in rows:
            validate_record(row)
            self.assertEqual(row["review"]["status"], "unreviewed")
            with self.assertRaisesRegex(ValueError, "actual reviewed approval"):
                validate_record(row, require_reviewed=True)
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary) / "prepared"
            with self.assertRaises(ValueError):
                prepare(path, destination, "test")
            self.assertFalse(destination.exists())

    def test_utf8_boundaries_and_span_text_are_checked(self):
        row = fixture("Löffler")
        validate_record(row, True)
        row["spans"][0]["start"] = 2
        with self.assertRaisesRegex(ValueError, "Unicode"):
            validate_record(row)
        self.assertEqual(byte_boundaries("🎵ö"), [0, 4, 6])

    def test_missing_review_metadata_and_dangling_relations_fail(self):
        row = fixture()
        row["review"]["reviewedAt"] = "2026-01-01"
        with self.assertRaisesRegex(ValueError, "timestamp"):
            validate_record(row)
        row = fixture()
        row["relations"] = [{"from": "missing", "to": "s1", "kind": "modifies"}]
        with self.assertRaisesRegex(ValueError, "dangling"):
            validate_record(row)

    def test_identity_and_paraphrase_groups_stay_together(self):
        records = [fixture("Alpha", "a"), fixture("Alpha", "b"), fixture("Beta", "c"), fixture("Gamma", "d"), fixture("Delta", "e")]
        records[-1]["group"] = "b"
        splits, assignment = grouped_splits(records, "fixed")
        self.assertEqual(assignment["a"], assignment["b"])
        self.assertEqual(assignment["a"], assignment["e"])
        self.assertEqual(assignment, grouped_splits(list(reversed(records)), "fixed")[1])
        self.assertTrue(all(splits.values()))

    def test_jsonl_preparation_and_corruption_guard(self):
        records = [fixture("Alpha", "a"), fixture("Beta", "b"), fixture("Gamma", "c")]
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            path = root / "records.jsonl"
            path.write_text("\n".join(json.dumps(row) for row in records), encoding="utf-8")
            self.assertEqual(read_records(path), records)
            manifest = prepare(path, root / "out", "fixed")
            self.assertEqual(len(load_split(root / "out", "train", manifest)), 1)
            with (root / "out/train.jsonl").open("a") as stream:
                stream.write(" ")
            with self.assertRaisesRegex(ValueError, "checksum"):
                load_split(root / "out", "train", manifest)

    def test_token_supervision_preserves_source_and_rejects_overlap(self):
        row = fixture("Löffler")
        labels = {"O": 0, "B-artist:similarity": 1, "I-artist:similarity": 2}
        self.assertEqual(align_labels(row, [(0, 0), (0, 1), (2, 4), (4, 9), (0, 0)], labels), [-100, 0, 1, 2, -100])
        row["spans"].append({**row["spans"][0], "id": "s2"})
        with self.assertRaisesRegex(ValueError, "overlapping"):
            align_labels(row, [(2, 9)], labels)

    def test_native_parity_cannot_hide_missing_or_shifted_outputs(self):
        expected = {"version": 1, "kind": "minilm", "cases": [{"text": "ö", "ids": [1], "attentionMask": [1], "typeIds": [0], "tokens": [{"id": 1, "start": 0, "end": 2, "special": False}], "embedding": [1.] + [0.] * 383}]}
        actual = copy.deepcopy(expected)
        self.assertTrue(compare(expected, actual)["passed"])
        actual["cases"][0]["tokens"][0]["end"] = 1
        self.assertFalse(compare(expected, actual)["passed"])
        actual = copy.deepcopy(expected)
        actual["cases"][0]["embedding"] = [float("nan"), 0.]
        self.assertFalse(compare(expected, actual)["passed"])
        actual["cases"] = []
        with self.assertRaises(ValueError):
            compare(expected, actual)


if __name__ == "__main__":
    unittest.main()
