"""Bounded numerical contract regressions; never load a checkpoint."""
import unittest
import numpy as np
from export_mert import normalize, resample


class MERTPreprocessingTests(unittest.TestCase):
    def test_normalizes_only_observed_samples_and_masks_padding(self):
        pcm = np.tile(np.array([0.1, 0.3], dtype=np.float32), 200)
        values, mask = normalize(pcm)
        self.assertEqual(values.shape, (1, 120000))
        self.assertEqual(mask.dtype, np.int64)
        self.assertEqual(int(mask.sum()), 400)
        self.assertAlmostEqual(float(values[0, :400].mean()), 0, places=6)
        self.assertTrue(np.all(values[0, 400:] == 0))
        self.assertTrue(np.all(mask[0, 400:] == 0))

    def test_constant_and_silence_are_finite_zero(self):
        for level in (0, 0.25):
            values, _ = normalize(np.full(400, level))
            self.assertTrue(np.all(values == 0))

    def test_invalid_inputs_rejected(self):
        for pcm in (np.zeros(399), np.zeros(120001), np.full(400, np.nan), np.full(400, np.inf)):
            with self.assertRaises(ValueError):
                normalize(pcm)

    def test_resample_preserves_dc_at_edges(self):
        for rate in (8000, 24000, 44100, 48000, 96000):
            actual = resample(np.full((rate // 20, 2), 0.25, dtype=np.float32), rate)
            self.assertEqual(len(actual), 1200)
            np.testing.assert_allclose(actual, 0.25, atol=1e-7)

    def test_downsampling_rejects_out_of_band_tone(self):
        t = np.arange(4800) / 48000
        low = resample(np.sin(2 * np.pi * 1000 * t), 48000)[100:-100]
        high = resample(np.sin(2 * np.pi * 18000 * t), 48000)[100:-100]
        self.assertLess(float(np.linalg.norm(high) / np.linalg.norm(low)), 0.001)

    def test_arithmetic_mono_cancels_antiphase(self):
        signal = np.linspace(-0.5, 0.5, 800, dtype=np.float32)
        np.testing.assert_equal(resample(np.stack([signal, -signal], axis=1), 48000), 0)


if __name__ == "__main__":
    unittest.main()
