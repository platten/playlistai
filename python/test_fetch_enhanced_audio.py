"""Offline transport regressions; no external assets or preview audio required."""

import hashlib
import io
from pathlib import Path
import tempfile
import unittest

from fetch_enhanced_audio import fetch, target_path, verify


class Response(io.BytesIO):
    def __init__(self, body, status=200, headers=None):
        super().__init__(body)
        self.status = status
        self.headers = headers or {}

    def geturl(self):
        return "https://example.test/model"


class FetchTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.body = b"verified fixture bytes"
        self.asset = {"path": "sources/model.bin", "url": "https://example.test/model",
                      "size": len(self.body), "sha256": hashlib.sha256(self.body).hexdigest()}

    def test_download_and_offline_reuse(self):
        target = fetch(self.root, self.asset, opener=lambda *a, **k: Response(self.body))
        self.assertTrue(verify(target, self.asset))
        self.assertFalse(target.with_suffix(".bin.part").exists())
        def no_network(*a, **k):
            self.fail("verified file caused network access")
        self.assertEqual(fetch(self.root, self.asset, True, no_network), target)

    def test_resume_and_server_ignoring_range(self):
        for status in (200, 206):
            with self.subTest(status=status):
                target = target_path(self.root, self.asset)
                target.parent.mkdir(parents=True, exist_ok=True)
                partial = target.with_suffix(".bin.part")
                partial.write_bytes(self.body[:5])
                def opener(request, timeout):
                    self.assertEqual(request.headers["Range"], "bytes=5-")
                    if status == 200:
                        return Response(self.body)
                    return Response(self.body[5:], 206, {"Content-Range": f"bytes 5-{len(self.body)-1}/{len(self.body)}"})
                fetch(self.root, self.asset, opener=opener)
                self.assertEqual(target.read_bytes(), self.body)
                target.unlink()

    def test_complete_partial_requires_no_network(self):
        target = target_path(self.root, self.asset)
        target.parent.mkdir(parents=True)
        target.with_suffix(".bin.part").write_bytes(self.body)
        def no_network(*a, **k):
            self.fail("complete partial caused network access")
        fetch(self.root, self.asset, opener=no_network)
        self.assertEqual(target.read_bytes(), self.body)

    def test_refuses_bad_range_size_hash_or_encoding(self):
        for label, response in (
            ("range", Response(self.body, 206, {"Content-Range": "bytes 1-9/10"})),
            ("size", Response(self.body + b"extra")),
            ("hash", Response(b"x" * len(self.body))),
            ("truncated", Response(self.body[:3])),
            ("encoding", Response(self.body, headers={"Content-Encoding": "gzip"})),
        ):
            with self.subTest(label=label), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                with self.assertRaises(ValueError):
                    fetch(root, self.asset, opener=lambda *a, **k: response)
                self.assertFalse(target_path(root, self.asset).exists())

    def test_existing_mismatch_is_preserved(self):
        target = target_path(self.root, self.asset)
        target.parent.mkdir(parents=True)
        target.write_bytes(b"unrelated existing data")
        with self.assertRaisesRegex(ValueError, "preserved"):
            fetch(self.root, self.asset)
        self.assertEqual(target.read_bytes(), b"unrelated existing data")

    def test_offline_missing_fails_without_download(self):
        with self.assertRaisesRegex(ValueError, "missing asset"):
            fetch(self.root, self.asset, verify_only=True)

    def test_paths_cannot_escape(self):
        for path in ("../x", "/x", "C:/x", "sources/../../x", "sources/..\\x"):
            with self.subTest(path=path), self.assertRaises(ValueError):
                target_path(self.root, dict(self.asset, path=path))


if __name__ == "__main__":
    unittest.main()
