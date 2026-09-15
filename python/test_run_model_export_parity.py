import contextlib
import io
import json
from pathlib import Path
import tempfile
import types
import unittest
from unittest.mock import patch

import run_model_export_parity as parity


class ModelExportParityTests(unittest.TestCase):
    def test_source_selection_is_pinned_and_excludes_unrelated_catalog_and_runtimes(self):
        mert = parity.selected_assets("mert")
        self.assertEqual(len(mert), 7)
        self.assertTrue(all(a["path"].startswith("sources/MERT-v1-95M/") or a == parity.MERT_LICENSE for a in mert))
        self.assertLess(sum(a["size"] for a in mert), 400_000_000)
        clap = parity.selected_assets("clap")
        self.assertEqual(len(clap), 1 + len(parity.clap.ASSETS))
        self.assertEqual(clap[0]["sha256"], parity.clap.CHECKPOINT["sha256"])
        for asset in mert + clap:
            self.assertTrue(asset["url"].startswith("https://"))
            self.assertRegex(asset["sha256"], r"^[0-9a-f]{64}$")
            self.assertGreater(asset["size"], 0)
            self.assertNotIn("catalog", asset["path"])
            self.assertNotIn("runtime", asset["path"])
        with self.assertRaises(ValueError):
            parity.selected_assets("../private")

    def test_work_and_reports_must_be_fresh_separate_and_outside_repo(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            repo = base / "repo"
            repo.mkdir()
            for work, reports in ((repo, base / "reports"), (repo / "child", base / "reports"),
                                  (base, base / "reports"), (base / "work", base / "work/reports"),
                                  (base / "work/child", base / "work")):
                with self.assertRaises(ValueError):
                    parity.prepare_directories(work, reports, repo)
            work = base / "work"
            work.mkdir()
            sentinel = work / "keep"
            sentinel.write_text("existing data", encoding="utf-8")
            with self.assertRaises(ValueError):
                parity.prepare_directories(work, base / "reports", repo)
            self.assertEqual(sentinel.read_text(), "existing data")
            created = parity.prepare_directories(base / "new-work", base / "reports", repo)
            self.assertTrue(all(path.is_dir() for path in created))

    def test_artifact_collection_only_copies_bounded_json_allowlist(self):
        with tempfile.TemporaryDirectory() as directory:
            export, reports = Path(directory) / "export", Path(directory) / "reports"
            export.mkdir()
            reports.mkdir()
            for name in ("parity.json", "conversion.json", "health.json", "private.json", "audio.onnx", "checkpoint.pt"):
                (export / name).write_text('{"synthetic": true}', encoding="utf-8")
            parity.collect_reports("clap", export, reports)
            self.assertEqual({p.name for p in reports.iterdir()}, {"clap-parity.json", "clap-conversion.json"})
            (export / "parity.json").write_bytes(b" " * ((1 << 20) + 1))
            with self.assertRaisesRegex(ValueError, "size budget"):
                parity.collect_reports("clap", export, reports)

    def test_artifact_collection_refuses_symlink(self):
        with tempfile.TemporaryDirectory() as directory:
            export, reports = Path(directory) / "export", Path(directory) / "reports"
            export.mkdir()
            reports.mkdir()
            outside = Path(directory) / "outside.json"
            outside.write_text("{}", encoding="utf-8")
            try:
                (export / "parity.json").symlink_to(outside)
            except OSError as error:
                self.skipTest(f"Symlinks unavailable: {error}")
            with self.assertRaisesRegex(ValueError, "symlink"):
                parity.collect_reports("clap", export, reports)
            self.assertEqual(list(reports.iterdir()), [])

    def test_export_dispatch_reuses_real_entry_points_without_packaging(self):
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            torch = types.SimpleNamespace(set_num_threads=lambda _: None, manual_seed=lambda _: None)
            with patch.object(parity.subprocess, "run") as run, patch.object(parity.clap, "export_and_validate") as export, patch.dict("sys.modules", {"torch": torch}):
                parity.export_model("clap", work, 2)
                self.assertIn("./cmd/audioparity", run.call_args.args[0])
                self.assertEqual(export.call_args.args[-1], work / "export")
                parity.export_model("mert", work, 2)
                command = run.call_args.args[0]
                self.assertTrue(command[1].endswith("export_mert.py"))
                self.assertNotIn("--runtime-library", command)
                self.assertIn("--license-text", command)

    def test_driver_records_success_and_failure_without_uploading_graphs(self):
        for fail in (False, True):
            with self.subTest(fail=fail), tempfile.TemporaryDirectory() as directory:
                work, reports = Path(directory) / "work", Path(directory) / "reports"
                def fake_export(model, output, threads):
                    export = output / "export"
                    export.mkdir()
                    (export / "parity-report.json").write_text('{"fixtures": 8}', encoding="utf-8")
                    (export / "model.onnx").write_text("private graph", encoding="utf-8")
                    if fail:
                        raise ValueError("synthetic export failure")
                with patch.object(parity, "check_environment", return_value={"torch": "pinned"}), patch.object(parity, "fetch") as fetch, patch.object(parity, "export_model", side_effect=fake_export), contextlib.redirect_stdout(io.StringIO()):
                    args = ["--model", "mert", "--work-dir", str(work), "--report-dir", str(reports)]
                    if fail:
                        with self.assertRaisesRegex(ValueError, "synthetic export failure"):
                            parity.main(args)
                    else:
                        parity.main(args)
                    self.assertEqual(fetch.call_count, len(parity.selected_assets("mert")))
                run = json.loads((reports / "run.json").read_text())
                self.assertEqual(run["status"], "failed" if fail else "passed")
                self.assertFalse(run["modelPublished"])
                self.assertEqual({p.name for p in reports.iterdir()}, {"run.json", "sources.json", "mert-parity.json"})

    def test_describe_has_no_download_environment_or_export_side_effects(self):
        output = io.StringIO()
        with patch.object(parity, "fetch") as fetch, patch.object(parity, "check_environment") as check, patch.object(parity, "export_model") as export, contextlib.redirect_stdout(output):
            parity.main(["--model", "clap", "--describe"])
        self.assertEqual(json.loads(output.getvalue())["model"], "clap")
        fetch.assert_not_called()
        check.assert_not_called()
        export.assert_not_called()

    def test_workflow_is_manual_with_read_permissions_and_report_only_artifacts(self):
        workflow = (parity.ROOT / ".github/workflows/model-export-parity.yml").read_text(encoding="utf-8")
        self.assertIn("  workflow_dispatch:", workflow)
        for trigger in ("  push:", "  pull_request:", "  pull_request_target:", "  schedule:"):
            self.assertNotIn(trigger, workflow)
        self.assertIn("  contents: read", workflow)
        self.assertIn("persist-credentials: false", workflow)
        self.assertIn("path: ${{ runner.temp }}/model-parity-reports/*.json", workflow)
        self.assertNotIn("secrets.", workflow)


if __name__ == "__main__":
    unittest.main()
