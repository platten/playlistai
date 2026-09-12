import io
import hashlib
import json
from pathlib import Path
import tarfile
import tempfile
import struct
import unittest
import zipfile

from prepare_mert_packs import member_bytes, pe_imports, crt_assets


def fixture_pe(imports=(), machine=0x8664):
    raw = bytearray(2048)
    raw[:2] = b"MZ"
    struct.pack_into("<I", raw, 60, 128)
    raw[128:132] = b"PE\0\0"
    struct.pack_into("<HH", raw, 132, machine, 1)
    struct.pack_into("<H", raw, 148, 240)
    struct.pack_into("<H", raw, 152, 0x20B)
    if imports:
        struct.pack_into("<I", raw, 272, 0x1000)
    struct.pack_into("<IIII", raw, 400, 1536, 0x1000, 1536, 512)
    for i, name in enumerate(imports):
        struct.pack_into("<I", raw, 512 + i * 20 + 12, 0x1200 + i * 128)
        value = name.encode("ascii") + b"\0"
        raw[1024 + i * 128:1024 + i * 128 + len(value)] = value
    return bytes(raw)


class PackSafetyTests(unittest.TestCase):
    def test_pe_import_names_and_crt_dependency_closure(self):
        self.assertEqual(pe_imports(fixture_pe(["MSVCP140.dll", "KERNEL32.dll"])), {"msvcp140.dll", "kernel32.dll"})
        with tempfile.TemporaryDirectory() as tmp:
            source = Path(tmp)
            pins = []
            for name, raw in [("msvcp140.dll", fixture_pe(["VCRUNTIME140.dll"])), ("vcruntime140.dll", fixture_pe())]:
                (source / name).write_bytes(raw)
                pins.append({"name": name, "path": name, "size": len(raw), "sha256": hashlib.sha256(raw).hexdigest()})
            (source / "license.txt").write_bytes(b"test notices")
            notices = [{"path": "license.txt", "size": 12, "sha256": hashlib.sha256(b"test notices").hexdigest()}]
            lock = {"windowsCRT": {"source": "official fixture", "targets": {"windows/amd64": {"files": pins, "notices": notices}}}}
            assets, text, _ = crt_assets(lock, "windows/amd64", source, fixture_pe(["MSVCP140.dll"]))
            self.assertEqual(len(assets), 2)
            self.assertEqual(text, "test notices")
            lock["windowsCRT"]["targets"]["windows/amd64"]["files"] = pins[:1]
            with self.assertRaisesRegex(ValueError, "Missing app-local"):
                crt_assets(lock, "windows/amd64", source, fixture_pe(["MSVCP140.dll"]))
            lock["windowsCRT"]["targets"]["windows/amd64"]["files"] = pins
            (source / "vcruntime140.dll").write_bytes(b"corrupted")
            with self.assertRaisesRegex(ValueError, "integrity"):
                crt_assets(lock, "windows/amd64", source, fixture_pe(["MSVCP140.dll"]))

    def test_windows_assets_cannot_be_omitted(self):
        with self.assertRaisesRegex(ValueError, "crt-source"):
            crt_assets({}, "windows/amd64", None, fixture_pe())
        self.assertEqual(crt_assets({}, "linux/amd64", None, b""), ([], "", []))

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
