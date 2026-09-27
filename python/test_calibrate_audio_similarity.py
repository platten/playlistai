import unittest
from calibrate_audio_similarity import calibrate


class CalibrationTests(unittest.TestCase):
    def fixture(self):
        return dict(modelFingerprint="synthetic-space", kind="texture", criterion="spacious reverberation",
                    version="synthetic-v1", developmentSet="dev", validationSet="validation",
                    examples=[dict(recordingId=str(i), split=split, score=score, label=label)
                              for i, (split, score, label) in enumerate([
                                  ("development", .8, True), ("development", .1, False),
                                  ("validation", .9, True), ("validation", .2, False),
                                  ("validation", .95, None)])])

    def test_unknown_not_negative(self):
        result = calibrate(self.fixture())
        self.assertEqual(result["minimumScore"], .8)
        self.assertEqual(result["validationPrecision"], 1)
        self.assertEqual(result["negativeExamples"], 1)

    def test_validation_cannot_retune_threshold(self):
        data = self.fixture()
        data["examples"][3]["score"] = .85
        with self.assertRaisesRegex(ValueError, "Frozen threshold"):
            calibrate(data)

    def test_reject_split_leakage(self):
        data = self.fixture()
        data["examples"][2]["recordingId"] = "0"
        with self.assertRaisesRegex(ValueError, "split leakage"):
            calibrate(data)


if __name__ == "__main__":
    unittest.main()
