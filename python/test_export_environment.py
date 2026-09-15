import importlib.metadata
import tempfile
import unittest
from pathlib import Path

from export_environment import check_environment, pinned_versions


class ExportEnvironmentTests(unittest.TestCase):
    def test_preflight_uses_installation_pins_and_accepts_cpu_build_tag(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "requirements.txt"
            path.write_text("# exact tested environment\ntorch==2.13.0\ntransformers==5.10.1\n", encoding="utf-8")
            installed = {"torch": "2.13.0+cpu", "transformers": "5.10.1"}
            self.assertEqual(check_environment(path, installed.__getitem__), pinned_versions(path))
            installed["torch"] = "2.9.1"
            with self.assertRaisesRegex(ValueError, "environment mismatch"):
                check_environment(path, installed.__getitem__)

    def test_missing_dependency_and_ambiguous_pins_fail(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "requirements.txt"
            for content in ("torch>=2.13.0", "torch==2.13.0\ntorch==2.9.1", "# no pins"):
                path.write_text(content, encoding="utf-8")
                with self.assertRaises(ValueError):
                    pinned_versions(path)
            path.write_text("torch==2.13.0", encoding="utf-8")
            def missing(_):
                raise importlib.metadata.PackageNotFoundError("torch")
            with self.assertRaisesRegex(ValueError, "not installed"):
                check_environment(path, missing)

    def test_checked_in_clap_environments_use_same_secure_core(self):
        root = Path(__file__).parent
        pack = pinned_versions(root / "requirements-laion-clap-pack.txt")
        validation = pinned_versions(root / "requirements-clap-validation.txt")
        self.assertEqual(pack, validation)
        mert = pinned_versions(root / "requirements-mert.txt")
        self.assertEqual({name: mert[name] for name in pack}, pack)
        self.assertGreaterEqual(tuple(map(int, pack["transformers"].split("."))), (5, 10, 0))


if __name__ == "__main__":
    unittest.main()
