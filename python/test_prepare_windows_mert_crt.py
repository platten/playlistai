"""Offline extraction safety tests: no downloads or native programs required."""
import hashlib
import json
from pathlib import Path
import struct
import tempfile
import unittest
from unittest.mock import patch

from prepare_windows_mert_crt import acquire, cabinets_from, outside_repository, prepare_notices, within


def pin(path, raw):
    return {"path": path, "size": len(raw), "sha256": hashlib.sha256(raw).hexdigest(),
            "url": "https://download.visualstudio.microsoft.com/download/test.exe"}


class CRTPreparationTests(unittest.TestCase):
    def test_cabinet_scan_rejects_truncation_and_unexpected_containers(self):
        cabinet = bytearray(36)
        cabinet[:4] = b"MSCF"
        struct.pack_into("<I", cabinet, 8, 36)
        cabinet[24:26] = bytes([3, 1])
        self.assertEqual(cabinets_from(b"MZ" + cabinet + cabinet), [cabinet, cabinet])
        for raw in [b"MSCF", cabinet, cabinet + cabinet[:-1], cabinet * 3]:
            with self.assertRaises(ValueError):
                cabinets_from(raw)

    def test_existing_source_is_verified_and_never_overwritten_or_executed(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            original = b"original untrusted executable bytes"
            (root / "test.exe").write_bytes(original)
            with patch("urllib.request.urlopen", side_effect=AssertionError("network forbidden")):
                self.assertEqual(acquire(root, pin("test.exe", original)), original)
                with self.assertRaises(ValueError):
                    acquire(root, pin("test.exe", b"different"))
            self.assertEqual((root / "test.exe").read_bytes(), original)

    def test_download_integrity_failure_does_not_publish_bad_archive(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            with patch("urllib.request.urlopen") as request:
                request.return_value.__enter__.return_value.read.return_value = b"corrupt"
                with self.assertRaises(ValueError):
                    acquire(root, pin("test.exe", b"expected"))
            self.assertFalse((root / "test.exe").exists())
            bad = pin("test.exe", b"expected")
            bad["url"] = "https://untrusted.example/test.exe"
            with self.assertRaises(ValueError):
                acquire(root, bad)

    def test_output_paths_and_repository_are_protected(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            for path in ["../escape", str(root.parent / "escape"), "."]:
                with self.assertRaises(ValueError):
                    within(root, path)
            self.assertEqual(within(root, "amd64/runtime.dll"), root / "amd64/runtime.dll")
        with self.assertRaises(ValueError):
            outside_repository(Path(__file__).parent / "downloaded")

    def test_checked_in_notice_matches_both_platform_pins(self):
        lock = json.loads(Path(__file__).with_name("mert-runtime-sources.json").read_text())
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            prepare_notices(root, lock["windowsCRT"]["targets"])
            prepare_notices(root, lock["windowsCRT"]["targets"])
            notice = root / "MICROSOFT-CRT-NOTICES.txt"
            self.assertIn("DISTRIBUTABLE CODE", notice.read_text(encoding="utf-8"))
            notice.write_bytes(b"changed")
            with self.assertRaises(ValueError):
                prepare_notices(root, lock["windowsCRT"]["targets"])


if __name__ == "__main__":
    unittest.main()
