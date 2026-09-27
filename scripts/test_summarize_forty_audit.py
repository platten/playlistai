"""Offline regression for occurrence accounting and stale audit rejection."""

import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


spec = importlib.util.spec_from_file_location("forty_audit", Path(__file__).with_name("summarize-forty-audit.py"))
audit_summary = importlib.util.module_from_spec(spec)
spec.loader.exec_module(audit_summary)


class FortyAuditTest(unittest.TestCase):
    def test_accounting_and_identity_validation(self):
        prompts = [{"prompt": f"Public synthetic prompt {n}", "count": 10} for n in range(40)]
        track = dict(id="recording:α", artist="Artist", title="Title")
        run = dict(prompt=prompts[0]["prompt"], result={"playlist": {"tracks": [track]}},
                   parser={"backend": "rules", "fallbackUsed": True}, milliseconds=1000,
                   generationLimitSeconds=300, interpretationFindings=["lost criterion"])
        failed = dict(prompt=prompts[1]["prompt"], error="startup failed", timedOut=True, milliseconds=910000,
                      result={"playlist": {"tracks": None}})
        report = dict(runs=[run, failed], completed=False)
        audit = dict(caseNumber=1, prompt=prompts[0]["prompt"], requested=10,
                     returnedCount=1, missingSlots=9,
                     runIdentity=dict(variant="baseline", rawCaseSHA256=audit_summary.run_hash(run)),
                     tracks=[dict(**track, position=1, verdict="unknown", sources=[],
                                  searchesAttempted=["Artist Title identity"] )])
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "baseline-01.json"
            path.write_text(json.dumps(audit), encoding="utf-8")
            result = audit_summary.summarize(prompts, report, directory, "baseline")
            self.assertEqual(result["requestedSlots"], 400)
            self.assertEqual(result["returnedOccurrences"], 1)
            self.assertEqual(result["auditedOccurrences"], 1)
            self.assertEqual(result["missingSlotsInAttemptedCases"], 19)
            self.assertEqual(result["unrunSlots"], 380)
            self.assertEqual(len(result["occurrences"]), 400)
            self.assertEqual(result["operationFailures"], 1)
            self.assertEqual(result["parserFallbackCases"], 1)
            self.assertEqual(result["parserUnknownCases"], 1)
            self.assertEqual(result["verdictFractionsPerRequested"]["unknown"], 1 / 400)
            self.assertEqual(result["cases"][0]["interpretationFindings"], ["lost criterion"])
            self.assertEqual(result["cases"][0]["rawCaseSHA256"], audit_summary.run_hash(run))
            self.assertEqual(result["cases"][1]["rawCaseSHA256"], audit_summary.run_hash(failed))
            self.assertIsNone(result["cases"][2]["rawCaseSHA256"])
            self.assertEqual(result["cases"][0]["reportedMilliseconds"], 1000)
            self.assertEqual(result["cases"][1]["reportedMilliseconds"], 910000)
            self.assertNotIn("generationMilliseconds", result["cases"][1])
            audit_summary.write_outputs(result, Path(directory) / "summary")
            self.assertTrue((Path(directory) / "summary.csv").exists())
            self.assertIn("380", (Path(directory) / "summary.md").read_text())

            mutations = [
                lambda a: a.update(prompt="wrong"),
                lambda a: a["runIdentity"].update(variant="treatment"),
                lambda a: a["runIdentity"].update(rawCaseSHA256="stale"),
                lambda a: a["runIdentity"].update(familyId="other-family"),
                lambda a: a["runIdentity"].update(inputMode="replay"),
                lambda a: a["runIdentity"].update(executable={"sha256": "other-binary"}),
                lambda a: a["tracks"][0].update(id="different-recording"),
                lambda a: a["tracks"][0].update(position=2),
                lambda a: a.update(tracks=[]),
                lambda a: a["tracks"][0].update(searchesAttempted=[]),
            ]
            for mutate in mutations:
                changed = copy.deepcopy(audit)
                mutate(changed)
                path.write_text(json.dumps(changed), encoding="utf-8")
                with self.subTest(mutation=changed), self.assertRaises(ValueError):
                    audit_summary.summarize(prompts, report, directory, "baseline")

            path.unlink()
            with self.assertRaises(ValueError):
                audit_summary.summarize(prompts, {**report, "listeningRuns": [{"variant": "treatment"}]}, directory, "baseline")
            result = audit_summary.summarize(prompts, report, directory, "baseline")
            self.assertEqual(result["unauditedOccurrences"], 1)
            self.assertEqual(result["verdictCounts"]["unknown"], 0)
            self.assertEqual(result["verdictFractionsPerReturned"]["good"], 0)
            empty = audit_summary.summarize(prompts, {"runs": []}, directory, "baseline")
            self.assertIsNone(empty["verdictFractionsPerReturned"]["good"])
            self.assertEqual(empty["unrunSlots"], 400)


if __name__ == "__main__":
    unittest.main()
