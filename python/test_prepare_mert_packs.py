import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
import zipfile

from prepare_mert_packs import member_bytes


class PackSafetyTests(unittest.TestCase):
    def test_runtime_lock_matches_application_registry(self):
        root = Path(__file__).resolve().parents[1]
        code = (root / "internal/audio/recommended.go").read_text()
        lock = json.loads(Path(__file__).with_name("mert-runtime-sources.json").read_text())
        self.assertEqual(lock["version"], "1.26.0")
        for target, pin in lock["targets"].items():
            self.assertIn('"' + target + '"', code)
            for value in pin.values():
                self.assertIn(str(value), code)

    def test_zip_exact_member_only(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "runtime.zip"
            with zipfile.ZipFile(path, "w") as archive:
                archive.writestr("../../runtime/lib/lib.dll", b"bad")
                archive.writestr("runtime/lib/lib.dll", b"good")
            self.assertEqual(member_bytes(path, "runtime/lib/lib.dll", 10), b"good")
            with self.assertRaises(ValueError):
                member_bytes(path, "runtime/lib/lib.dll", 3)

    def test_tar_symlink_not_accepted_as_runtime(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "runtime.tgz"
            with tarfile.open(path, "w:gz") as archive:
                member = tarfile.TarInfo("runtime/lib/lib.so")
                member.type = tarfile.SYMTYPE
                member.linkname = "../../outside"
                archive.addfile(member)
            with self.assertRaises(ValueError):
                member_bytes(path, "runtime/lib/lib.so", 100)

    def test_tar_regular_member_with_dot_prefix(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "runtime.tgz"
            with tarfile.open(path, "w:gz") as archive:
                member = tarfile.TarInfo("./runtime/lib/lib.so")
                member.size = 4
                archive.addfile(member, io.BytesIO(b"good"))
            self.assertEqual(member_bytes(path, "runtime/lib/lib.so", 4), b"good")


if __name__ == "__main__":
    unittest.main()
