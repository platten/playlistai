"""Calibration math and source-contract tests with synthetic arrays/files only."""
import json
import math
from pathlib import Path
import tempfile
import unittest

from calibrate_intent_nlu import candidate_metadata, decode_proposals, load_calibration, scan_thresholds, validate_head, wilson_lower
from prepare_intent_nlu_data import digest, prepare, write_json
from test_intent_nlu_data import fixture


LABELS = ["O", "B-artist:similarity", "I-artist:similarity"]


def token(start, end, special=False):
    return {"start": start, "end": end, "special": special}


class DecoderCalibrationTests(unittest.TestCase):
    def test_minimum_confidence_and_utf8_exact_spans(self):
        text = "🎵 Löffler"
        tokens = [token(0, 0, True), token(5, 8), token(8, 13), token(0, 0, True)]
        values = [[0., 0., 0.], [0., 6., 0.], [0., 0., 4.], [0., 0., 0.]]
        result = decode_proposals(text, tokens, values, LABELS, .9)
        self.assertEqual([(row["start"], row["end"], row["text"]) for row in result], [(5, 13, "Löffler")])
        self.assertAlmostEqual(result[0]["score"], 1 / (1 + 2 * math.exp(-4)))

    def test_uncertain_continuation_discards_whole_multiword_mention(self):
        tokens = [token(0, 5), token(6, 10)]
        self.assertEqual(decode_proposals("Alpha Beta", tokens, [[0., 6., 0.], [0., 0., 1.]], LABELS, .9), [])

    def test_orphan_and_partial_word_are_not_promoted(self):
        self.assertEqual(decode_proposals("Alpha", [token(0, 5)], [[0., 0., 6.]], LABELS, .9), [])
        self.assertEqual(decode_proposals("AlphaBeta", [token(0, 5), token(5, 9)], [[0., 6., 0.], [6., 0., 0.]], LABELS, .9), [])

    def test_nonfinite_and_split_unicode_rejected(self):
        with self.assertRaisesRegex(ValueError, "Nonfinite"):
            decode_proposals("A", [token(0, 1)], [[float("nan"), 0., 0.]], LABELS, .9)
        with self.assertRaisesRegex(ValueError, "Unicode"):
            decode_proposals("ö", [token(0, 1)], [[0., 6., 0.]], LABELS, .9)
        with self.assertRaisesRegex(ValueError, "threshold"):
            decode_proposals("A", [token(0, 1)], [[0., 6., 0.]], LABELS, 1.)

    def test_threshold_suggestion_keeps_uncertainty_and_never_approves(self):
        cases = [{"text": "Alpha Beta", "tokens": [token(0, 5), token(6, 10)], "logits": [[0., 8., 0.], [0., 2., 0.]], "gold": [{"start": 0, "end": 5, "kind": "artist", "role": "similarity"}]}]
        report = scan_thresholds(cases, LABELS, [.5, .9, .95], .99, 1)
        self.assertEqual(report["suggestedThreshold"], .9)
        low, high = report["scan"][:2]
        self.assertEqual((low["correctSpans"], low["incorrectSpans"], low["observedPrecision"]), (1, 1, .5))
        self.assertEqual(high["observedPrecision"], 1.)
        self.assertLess(high["precisionWilson95Lower"], .21)
        self.assertEqual(high["recall"], 1.)
        self.assertEqual(high["exampleCoverage"], 1.)
        candidate = candidate_metadata({"modelSHA256": "fake"}, report)
        self.assertFalse(candidate["reviewed"])
        self.assertEqual(candidate["threshold"], .9)
        self.assertIsNone(scan_thresholds(cases, LABELS, [.9], .99, 20)["suggestedThreshold"])

    def test_abstention_has_no_precision_and_wrong_role_is_an_error(self):
        cases = [{"text": "Alpha", "tokens": [token(0, 5)], "logits": [[0., 0., 0.]], "gold": [{"start": 0, "end": 5, "kind": "artist", "role": "exclude_output"}]}]
        empty = scan_thresholds(cases, LABELS, [.9], .99, 1)["scan"][0]
        self.assertIsNone(empty["observedPrecision"])
        self.assertIsNone(empty["precisionWilson95Lower"])
        self.assertEqual(empty["recall"], 0.)
        self.assertEqual(empty["exampleCoverage"], 0.)
        cases[0]["logits"] = [[0., 8., 0.]]
        wrong = scan_thresholds(cases, LABELS, [.9], .99, 1)["scan"][0]
        self.assertEqual((wrong["correctSpans"], wrong["incorrectSpans"], wrong["missedSpans"]), (0, 1, 1))
        self.assertIsNone(wilson_lower(0, 0))


class CalibrationProvenanceTests(unittest.TestCase):
    def test_head_checks_bind_model_vocabulary_and_config(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "model.onnx").write_bytes(b"synthetic model bytes; not executed")
            (root / "vocab.txt").write_text("[PAD]\n[UNK]\n[CLS]\n[SEP]\n[MASK]\n", encoding="utf-8")
            write_json(root / "config.json", {"model_type": "distilbert", "dim": 768})
            head = {"version": 1, "outputName": "logits", "maxTokens": 512, "labels": LABELS, "modelSHA256": digest(root / "model.onnx"), "tokenizerSHA256": digest(root / "vocab.txt"), "configSHA256": digest(root / "config.json")}
            write_json(root / "nlu-head.json", head)
            checked, hashes = validate_head(root)
            self.assertEqual(checked, head)
            self.assertEqual(hashes["headSHA256"], digest(root / "nlu-head.json"))
            (root / "vocab.txt").write_text("tampered")
            with self.assertRaisesRegex(ValueError, "vocab.txt"):
                validate_head(root)

    def test_only_reviewed_bound_calibration_is_read_never_evaluation(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "records.json"
            write_json(source, [fixture("Alpha", "a"), fixture("Beta", "b"), fixture("Gamma", "c")])
            prepared, trained, exported = root / "data", root / "trained", root / "exported"
            prepare(source, prepared, "fixed")
            trained.mkdir()
            exported.mkdir()
            (prepared / "evaluation.jsonl").write_text("NOT CALIBRATION DATA")
            (exported / "model.onnx").write_bytes(b"synthetic unexecuted model")

            def bind():
                write_json(trained / "training-manifest.json", {"dataManifestSHA256": digest(prepared / "data-manifest.json")})
                write_json(exported / "export-parity.json", {"trainingManifestSHA256": digest(trained / "training-manifest.json"), "modelSHA256": digest(exported / "model.onnx")})

            bind()
            rows, provenance = load_calibration(prepared, trained, exported)
            self.assertEqual(len(rows), 1)
            self.assertEqual(provenance["calibrationSHA256"], digest(prepared / "calibration.jsonl"))
            record = rows[0]
            record["review"]["status"] = "unreviewed"
            (prepared / "calibration.jsonl").write_text(json.dumps(record) + "\n", encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "checksum"):
                load_calibration(prepared, trained, exported)
            manifest = json.loads((prepared / "data-manifest.json").read_text())
            manifest["files"]["calibration.jsonl"]["sha256"] = digest(prepared / "calibration.jsonl")
            write_json(prepared / "data-manifest.json", manifest)
            bind()
            with self.assertRaisesRegex(ValueError, "reviewed approval"):
                load_calibration(prepared, trained, exported)


if __name__ == "__main__":
    unittest.main()
