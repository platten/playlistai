import hashlib
import io
import json
import tarfile
import tempfile
import unittest
from pathlib import Path

import zstandard

from prepare_model_distribution import prepare, safe_path


class DistributionTests(unittest.TestCase):
    def test_deterministic_split_and_roundtrip(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            source = root / "weights"
            source.write_bytes(b"music" * 1000 + bytes(range(256)))
            entries = {"encoder/model.onnx": source}
            first = prepare("test", entries, root / "one", part_size=101)
            second = prepare("test", entries, root / "two", part_size=101)
            self.assertEqual(first, second)
            self.assertGreater(len(first["parts"]), 1)
            chunks = []
            for part in first["parts"]:
                data = (root / "one" / part["path"]).read_bytes()
                self.assertLessEqual(len(data), 101)
                self.assertEqual(len(data), part["size"])
                self.assertEqual(hashlib.sha256(data).hexdigest(), part["sha256"])
                chunks.append(data)
            with zstandard.ZstdDecompressor().stream_reader(io.BytesIO(b"".join(chunks))) as raw:
                with tarfile.open(fileobj=raw, mode="r|") as archive:
                    members = []
                    for member in archive:
                        self.assertTrue(member.isfile())
                        members.append(member.name)
                        self.assertEqual(archive.extractfile(member).read(), source.read_bytes())
                    self.assertEqual(members, ["encoder/model.onnx"])
            self.assertEqual(json.loads((root / "one" / "manifest.json").read_text()), first)

    def test_unsafe_paths_and_limits(self):
        for path in ("", "/absolute", "../escape", "a/../b", "a\\b", "C:/file", "a//b", "./a", "con.txt", "dir/LPT1", "trailing.", "trailing "):
            with self.subTest(path=path), self.assertRaises(ValueError):
                safe_path(path)
        with tempfile.TemporaryDirectory() as temp:
            source = Path(temp) / "source"
            source.write_text("model")
            for limit in (0, -1, 200_000_000):
                with self.assertRaises(ValueError):
                    prepare("test", {"model": source}, Path(temp) / "out", limit)
            with self.assertRaises(ValueError):
                prepare("../escape", {"model": source}, Path(temp) / "out")
            for entries in ({"model": source, "MODEL": source}, {"a": source, "a/b": source}):
                with self.assertRaises(ValueError):
                    prepare("test", entries, Path(temp) / "out")

    def test_existing_output_is_not_overwritten(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            source = root / "source"
            source.write_text("model")
            prepare("test", {"model": source}, root / "out")
            before = (root / "out" / "manifest.json").read_bytes()
            with self.assertRaises(ValueError):
                prepare("test", {"model": source}, root / "out")
            self.assertEqual((root / "out" / "manifest.json").read_bytes(), before)


if __name__ == "__main__":
    unittest.main()
