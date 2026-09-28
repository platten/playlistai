import array
import copy
import hashlib
import json
import math
import os
from pathlib import Path
import tempfile
import unittest
import wave

from prepare_discogs_effnet import EffNet, mean_scores, model_definitions, prepare_row, read_lock, source_file


class FakeEffNet:
    runtime = "essentia/test"
    calls = 0
    identity = {"model": "discogs-effnet", "revision": "1", "weightsSha256": "a" * 64, "metadataSha256": "b" * 64}
    definitions = {"encoder": {"model": identity}, "instrumentation": {"model": identity, "classes": ["piano", "guitar"]}}

    def classify(self, samples):
        self.calls += 1
        self.borrowed_samples = samples
        return [{"kind": "instrumentation", "model": self.definitions["encoder"]["model"],
                 "classes": ["piano", "guitar"], "scores": [.8, .1]}]


class DiscogsPreparationTest(unittest.TestCase):
    @unittest.skipUnless(os.environ.get("PLAYLISTAI_DISCOGS_MODELS"), "optional verified model smoke; no assets downloaded by tests")
    def test_local_official_models_share_encoder_and_produce_repeatable_scores(self):
        lock, _ = read_lock()
        model = EffNet(model_definitions(Path(os.environ["PLAYLISTAI_DISCOGS_MODELS"]), lock))
        samples = array.array("h", (int(8000 * math.sin(2 * math.pi * 440 * i / 16000)) for i in range(3 * 16000)))
        first = model.classify(samples)
        encoder = model.encoder
        second = model.classify(samples)
        self.assertIs(model.encoder, encoder)
        self.assertEqual([len(h["scores"]) for h in first], [40, 2, 2, 400])
        for left, right in zip(first, second):
            self.assertEqual(left["model"], right["model"])
            for a, b in zip(left["scores"], right["scores"]):
                self.assertAlmostEqual(a, b, places=6)

    def test_source_lock_has_all_four_heads_and_original_graphs(self):
        lock, fingerprint = read_lock()
        self.assertEqual(len(fingerprint), 64)
        self.assertLess(sum(a["size"] for a in lock["assets"]), 25 * 1024 * 1024)
        kinds = {a["kind"] for a in lock["assets"] if a["path"].endswith(".pb")}
        self.assertEqual(kinds, {"encoder", "instrumentation", "mood", "vocal", "style"})
        for asset in lock["assets"]:
            self.assertTrue(asset["url"].startswith("https://essentia.upf.edu/models/"))
            self.assertEqual(len(asset["sha256"]), 64)

    def test_score_shape_and_unknown_values(self):
        self.assertEqual(mean_scores([[0, 1], [1, 0]], 2), [.5, .5])
        for invalid in ([], [[1]], [[1, float("nan")]], [[1, 1.01]], [[1, -1]]):
            with self.assertRaises(ValueError):
                mean_scores(invalid, 2)

    def test_resume_coverage_provenance_and_audio_cleanup(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            path = root / "source.wav"
            with wave.open(str(path), "wb") as output:
                output.setparams((1, 2, 16000, 0, "NONE", "not compressed"))
                output.writeframes(array.array("h", [1000] * (5 * 16000)).tobytes())
            row = {"trackId": "catalog-track-1", "path": "source.wav", "source": "synthetic-test",
                   "catalogSha256": "c" * 64,
                   "sourceId": "sine-1", "license": "CC0-1.0", "derivedLicense": "CC0-1.0",
                   "redistributionAllowed": True, "audioSha256": hashlib.sha256(path.read_bytes()).hexdigest(),
                   "startSeconds": 1}
            cache = root / "cache"
            cache.mkdir()
            model = FakeEffNet()
            result, cached = prepare_row(row, root, 3, "lock", model, cache)
            self.assertFalse(cached)
            evidence = result["classifierEvidence"][0]
            self.assertEqual(evidence["coverage"]["segments"], [{"startSeconds": 1, "endSeconds": 4}])
            self.assertEqual(evidence["coverage"]["coveredSeconds"], 3)
            self.assertTrue(evidence["coverage"]["incomplete"])
            self.assertEqual(evidence["sourceId"], "sine-1")
            self.assertFalse(any(model.borrowed_samples))
            second, cached = prepare_row(row, root, 3, "lock", model, cache)
            self.assertTrue(cached)
            self.assertEqual(second, result)
            self.assertEqual(model.calls, 1)
            checkpoint = cache / (result["preparationKey"] + ".json")
            for field, changed in [("sourceId", "different-recording"), ("audioSha256", "f" * 64),
                                   ("runtime", "other-runtime"), ("preprocessing", "other/v1"),
                                   ("license", "invented-license"), ("encoder", {}),
                                   ("coverage", dict(evidence["coverage"], coveredSeconds=30))]:
                with self.subTest(cached_field=field):
                    invalid = copy.deepcopy(result)
                    invalid["classifierEvidence"][0][field] = changed
                    checkpoint.write_text(json.dumps(invalid))
                    with self.assertRaisesRegex(ValueError, "checkpoint"):
                        prepare_row(row, root, 3, "lock", model, cache)
            for field, changed in [("scores", [float("nan"), .5]), ("classes", ["guitar", "piano"]),
                                   ("model", {}), ("scores", [1]), ("kind", "mood")]:
                with self.subTest(cached_head=field):
                    invalid = copy.deepcopy(result)
                    invalid["classifierEvidence"][0]["heads"][0][field] = changed
                    checkpoint.write_text(json.dumps(invalid))
                    with self.assertRaisesRegex(ValueError, "checkpoint"):
                        prepare_row(row, root, 3, "lock", model, cache)
            checkpoint.write_text(json.dumps(result))
            prepare_row(row, root, 3, "changed-lock", model, cache)
            self.assertEqual(model.calls, 2)
            row["completeRecording"] = True
            row["startSeconds"] = 0
            full, _ = prepare_row(row, root, 10, "lock", model, cache)
            self.assertFalse(full["classifierEvidence"][0]["coverage"]["incomplete"])
            path.write_bytes(path.read_bytes() + b"changed")
            with self.assertRaisesRegex(ValueError, "checksum"):
                prepare_row(row, root, 3, "lock", model, cache)

    def test_source_permission_and_path_validation(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            row = {"trackId": "id", "source": "test", "sourceId": "test-id", "license": "CC0",
                   "catalogSha256": "c" * 64,
                   "derivedLicense": "CC0", "path": "../escape.wav", "redistributionAllowed": False}
            with self.assertRaisesRegex(ValueError, "permission"):
                source_file(root, row)
            row["redistributionAllowed"] = True
            with self.assertRaisesRegex(ValueError, "contained"):
                source_file(root, row)


if __name__ == "__main__":
    unittest.main()
