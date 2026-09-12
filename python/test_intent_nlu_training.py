"""Small random-encoder smoke test of offline train/export; no musical claims."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from prepare_intent_nlu_data import digest, prepare, write_json
from test_intent_nlu_data import fixture


class TrainingSmokeTests(unittest.TestCase):
    def test_tiny_synthetic_checkpoint_exports_without_activation(self):
        import torch
        from transformers import BertTokenizerFast, DistilBertConfig, DistilBertForMaskedLM

        torch.manual_seed(42)
        scripts = Path(__file__).parent
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "distilbert"
            source.mkdir()
            vocabulary = ["[PAD]", "[UNK]", "[CLS]", "[SEP]", "[MASK]", "Alpha", "Beta", "Gamma", "🎵", "Hurt", "by", "Nine", "Inch", "Nails", "Only", "the", "opening", "section", "instrumental", ";", "vocals", "later", "are", "fine", "."]
            (source / "vocab.txt").write_text("\n".join(vocabulary) + "\n", encoding="utf-8")
            tokenizer = BertTokenizerFast(vocab_file=str(source / "vocab.txt"), do_lower_case=False)
            tokenizer.save_pretrained(source)
            config = DistilBertConfig(vocab_size=len(vocabulary), n_layers=1, n_heads=2, dim=16, hidden_dim=32, dropout=0, attention_dropout=0)
            DistilBertForMaskedLM(config).save_pretrained(source, safe_serialization=True)
            write_json(root / "sources.json", [{"model": "distilbert", "name": path.name, "size": path.stat().st_size, "sha256": digest(path)} for path in source.iterdir()])
            data_source = root / "fixture.json"
            write_json(data_source, [fixture("Alpha", "a"), fixture("Beta", "b"), fixture("Gamma", "c")])
            prepare(data_source, root / "data", "fixed")
            # Evaluation contents are deliberately invalid: training must not read them.
            (root / "data/evaluation.jsonl").write_text("NOT TRAINING INPUT")
            train = subprocess.run([sys.executable, str(scripts / "train_intent_nlu.py"), "--data", str(root / "data"), "--source", str(source), "--output", str(root / "trained"), "--epochs", "1", "--threads", "1"], capture_output=True, text=True, timeout=90)
            self.assertEqual(train.returncode, 0, train.stdout + train.stderr)
            export = subprocess.run([sys.executable, str(scripts / "export_intent_nlu.py"), "--training", str(root / "trained"), "--output", str(root / "exported"), "--threads", "1"], capture_output=True, text=True, timeout=90)
            self.assertEqual(export.returncode, 0, export.stdout + export.stderr)
            candidate = json.loads((root / "exported/calibration-candidate.json").read_text())
            self.assertFalse(candidate["reviewed"])
            self.assertIsNone(candidate["threshold"])
            self.assertFalse((root / "exported/calibration.json").exists())
            head = json.loads((root / "exported/nlu-head.json").read_text())
            self.assertEqual(head["modelSHA256"], digest(root / "exported/model.onnx"))
            self.assertEqual(head["tokenizerSHA256"], digest(root / "exported/vocab.txt"))
            self.assertEqual(head["configSHA256"], digest(root / "exported/config.json"))
            self.assertEqual(candidate["tokenizerSHA256"], head["tokenizerSHA256"])
            self.assertEqual(head["outputName"], "logits")
            self.assertTrue(all(row["argmaxExact"] for row in json.loads((root / "exported/export-parity.json").read_text())["cases"]))


if __name__ == "__main__":
    unittest.main()
