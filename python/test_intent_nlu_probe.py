"""Offline safeguards for larger unreviewed diagnostics; no model needed."""
import copy
import unittest

from probe_intent_nlu_calibration import case_results, compare_source_quantities, diagnostic_scan, validate_expansion
from test_intent_nlu_data import fixture


LABELS = ["O", "B-artist:similarity", "I-artist:similarity"]


def probe(name="Beta", group="new"):
    row = fixture(name, group)
    row["review"] = {"status": "unreviewed"}
    return row


class ExpansionTests(unittest.TestCase):
    def test_refuses_approval_duplicates_and_training_identity(self):
        old = fixture("Alpha", "old")
        new = probe()
        validate_expansion([new], [old], {old["id"]}, LABELS)
        for change in (lambda row: row.update(review=old["review"]),
                       lambda row: row.update(id=old["id"]),
                       lambda row: row.update(group=old["group"]),
                       lambda row: row["spans"][0].update(canonical="Alpha"),
                       lambda row: row["spans"][0].update(canonical="Alpha", candidateCanonical="Gamma"),
                       lambda row: row["spans"][0].update(canonical="Ａｌｐｈａ"),
                       lambda row: row["spans"][0].update(role="unknown-role")):
            row = copy.deepcopy(new)
            change(row)
            with self.assertRaises(ValueError):
                validate_expansion([row], [old], {old["id"]}, LABELS)
        with self.assertRaisesRegex(ValueError, "Duplicate"):
            validate_expansion([new, copy.deepcopy(new)], [], set(), LABELS)

    def test_refuses_overlapping_spans_and_empty_probe(self):
        row = probe()
        duplicate = copy.deepcopy(row["spans"][0])
        duplicate["id"] = "different"
        row["spans"].append(duplicate)
        with self.assertRaisesRegex(ValueError, "Overlapping"):
            validate_expansion([row], [], set(), LABELS)
        with self.assertRaisesRegex(ValueError, "Nonempty"):
            validate_expansion([], [], set(), LABELS)

    def test_good_diagnostic_cannot_become_calibration_candidate(self):
        cases = [{"id": str(i), "text": "Beta", "tokens": [{"start": 0, "end": 4, "special": False}],
                  "logits": [[0., 20., 0.]], "gold": [{"start": 0, "end": 4, "kind": "artist", "role": "similarity"}]}
                 for i in range(25)]
        report = diagnostic_scan(cases, LABELS)
        self.assertIsNotNone(report["exploratoryThresholdMeetingObservedTarget"])
        self.assertNotIn("suggestedThreshold", report)
        self.assertFalse(report["reviewed"])
        self.assertFalse(report["calibrationEligible"])
        self.assertFalse(report["productionReady"])
        results = case_results(cases, LABELS)
        self.assertTrue(results[0]["proposals"][0]["correct"])
        self.assertEqual(results[0]["missed"], [])
        cases[0]["gold"][0]["role"] = "exclude_output"
        wrong = case_results(cases, LABELS)[0]
        self.assertFalse(wrong["proposals"][0]["correct"])
        self.assertEqual(len(wrong["missed"]), 1)

    def test_source_comparison_requires_same_prompts_and_reports_increment(self):
        span = {"start": 0, "end": 2, "kind": "quantity", "role": "count"}
        result = {"id": "a", "prompt": "12 tracks", "gold": [span],
                  "proposals": [{"start": 0, "end": 2, "label": "quantity:count", "correct": True}]}
        source = {"id": "a", "prompt": result["prompt"], "elapsedMs": .2, "translation": {"atoms": [
            {"kind": "count", "polarity": "positive", "strength": "required", "evidence": [{"start": 0, "end": 9}]}]}}
        counts = compare_source_quantities([result], [source])["roles"]["count"]
        self.assertEqual((counts["sourceCorrect"], counts["distilbertCorrectBeyondSource"]), (1, 0))
        source["translation"]["atoms"][0]["polarity"] = "negative"
        self.assertEqual(compare_source_quantities([result], [source])["roles"]["count"]["distilbertCorrectBeyondSource"], 0)
        source["translation"]["atoms"] = []
        self.assertEqual(compare_source_quantities([result], [source])["roles"]["count"]["distilbertCorrectBeyondSource"], 1)
        source["prompt"] = "12 hours"
        with self.assertRaisesRegex(ValueError, "prompt differs"):
            compare_source_quantities([result], [source])

    def test_broad_source_atom_cannot_claim_two_gold_quantities(self):
        result = {"id": "a", "prompt": "12 or 14 tracks", "gold": [
            {"start": 0, "end": 2, "kind": "quantity", "role": "count"},
            {"start": 6, "end": 8, "kind": "quantity", "role": "count"}], "proposals": []}
        source = {"id": "a", "prompt": result["prompt"], "elapsedMs": .2, "translation": {"atoms": [
            {"kind": "count", "polarity": "positive", "strength": "required", "evidence": [{"start": 0, "end": 15}]}]}}
        counts = compare_source_quantities([result], [source])["roles"]["count"]
        self.assertEqual((counts["gold"], counts["sourceCorrect"], counts["sourceAccepted"]), (2, 0, 1))


if __name__ == "__main__":
    unittest.main()
