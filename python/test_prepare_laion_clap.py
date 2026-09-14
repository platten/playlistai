import hashlib
import tempfile
import unittest
import zipfile
from pathlib import Path

from prepare_laion_clap import extract_runtime, mapped_name, verified


class LAIONCLAPPreparationTests(unittest.TestCase):
    def test_checkpoint_names_map_to_hugging_face_architecture(self):
        self.assertEqual(
            mapped_name("audio_branch.layers.2.blocks.3.attn.qkv.weight"),
            "audio_model.audio_encoder.layers.2.blocks.3.attention.self.qkv.weight",
        )
        self.assertEqual(
            mapped_name("text_projection.0.weight"),
            "text_projection.linear1.weight",
        )
        self.assertEqual(
            mapped_name("audio_transform.sequential.3.weight"),
            "audio_transform.layers.1.linear.weight",
        )

    def test_runtime_extracts_only_the_pinned_member(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            archive = root / "runtime.zip"
            data = b"verified runtime"
            with zipfile.ZipFile(archive, "w") as output:
                output.writestr("runtime/lib/runtime.dll", data)
                output.writestr("../../outside", b"unsafe")
            digest = hashlib.sha256(data).hexdigest()
            target = root / "out" / "runtime.dll"
            extract_runtime(archive, "runtime/lib/runtime.dll", target, len(data), digest)
            self.assertTrue(verified(target, len(data), digest))
            self.assertFalse((root / "outside").exists())
            with self.assertRaises(ValueError):
                extract_runtime(archive, "runtime/lib/runtime.dll", root / "bad.dll", len(data), "0" * 64)


if __name__ == "__main__":
    unittest.main()
