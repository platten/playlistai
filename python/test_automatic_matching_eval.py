import copy
import json
from pathlib import Path
import tempfile
import unittest

from automatic_matching_eval import (VERSION, canonical, compare, freeze, metric,
                                     paired_interval, prepare, producer_inputs, read_json, sha256,
                                     validate_corpus, write_new)


def recording(identity, artist=None):
    return dict(id=identity, artistId=artist or identity,
                audioSHA256=sha256(identity.encode()), license="synthetic fixture",
                coverage=[dict(startMs=0, endMs=20000)])


def fixture():
    records = [recording(str(i)) for i in range(30)]
    corpus = dict(version=VERSION, kind="song_describer", split="heldout",
                  sourceURL="https://example.invalid/synthetic-test", sourceLicense="synthetic fixture",
                  sourceSHA256="a" * 64, recordings=records, unavailableSourceItems=0,
                  tasks=[dict(id=r["id"], cluster=r["artistId"], text="synthetic caption", labels={r["id"]: 1})
                         for r in records])
    models = {role: dict(id=role, fingerprint=letter * 64, weightsSHA256=letter * 64,
                         policySHA256="f" * 64, preprocessingVersion="test/v1", license="fixture")
              for role, letter in (("baseline", "b"), ("challenger", "c"))}
    protocol = dict(version=VERSION, split="heldout", corpora={"captions": corpus}, models=models,
                    primaryCorpus="captions", primaryMetric="mrr", k=10, runSeed="18446744073709551615",
                    bootstrapSeed="paired-test", bootstrapSamples=1000, minimumClusters=30,
                    cacheCondition="warm_installed", startingCacheSHA256="d" * 64,
                    assetsSHA256="e" * 64, producerSourceSHA256="f" * 64, selectionPolicySHA256="a" * 64,
                    trainingOverlapAudits={"captions": dict(status="no_detected_overlap", notes="synthetic test only",
                                                            artifactSHA256="a" * 64)},
                    requiredPlatforms=["linux-amd64"],
                    nativeLimits=dict(additionalInstalledBytes=10_000_000_000, peakRSSBytes=4_000_000_000,
                                      p95InferenceMs=1000, maxParityError=.001))
    frozen = dict(protocol=protocol, protocolSHA256=sha256(canonical(protocol)))
    receipts = []
    for role in ("baseline", "challenger"):
        receipt = {key: protocol[key] for key in ("version", "runSeed", "cacheCondition", "startingCacheSHA256",
                                                 "assetsSHA256", "producerSourceSHA256")}
        receipt.update(protocolSHA256=frozen["protocolSHA256"], model=models[role], scores={"captions": {}})
        for task in corpus["tasks"]:
            receipt["scores"]["captions"][task["id"]] = {r["id"]: (1 if role == "challenger" else -1)
                                                         if r["id"] == task["id"] else 0 for r in records}
        receipt["nativeMeasurements"] = {"linux-amd64": dict(receiptSHA256="a" * 64,
            modelFingerprint=models[role]["fingerprint"], nativeExecution=True, downloadVerified=True,
            licenseNoticesPresent=True, additionalInstalledBytes=100, peakRSSBytes=100,
            p95InferenceMs=100, maxParityError=0)}
        receipts.append(receipt)
    return frozen, *receipts


class MatchingEvaluationTests(unittest.TestCase):
    def test_paired_improvement_and_no_automatic_installation(self):
        result = compare(*fixture())
        self.assertEqual(result["decision"], "eligible_for_review")
        self.assertFalse(result["changesInstalledScorer"])
        self.assertGreater(result["corpora"]["captions"]["pairedDelta95CI"][0], 0)

    def test_no_gain_or_missing_native_keeps_current_scorer(self):
        frozen, baseline, challenger = fixture()
        challenger["scores"] = copy.deepcopy(baseline["scores"])
        challenger["nativeMeasurements"] = {}
        result = compare(frozen, baseline, challenger)
        self.assertEqual(result["decision"], "retain_current_scorer")
        self.assertEqual(result["corpora"]["captions"]["pairedDelta95CI"], [0, 0])
        self.assertTrue(any("native" in blocker for blocker in result["blockers"]))

    def test_receipt_mismatch_and_failed_candidate_cannot_disappear(self):
        for field in ("runSeed", "startingCacheSHA256", "assetsSHA256", "protocolSHA256", "producerSourceSHA256"):
            frozen, baseline, challenger = fixture()
            challenger[field] = "changed"
            with self.subTest(field=field), self.assertRaises(ValueError):
                compare(frozen, baseline, challenger)
        frozen, baseline, challenger = fixture()
        del challenger["scores"]["captions"]["0"]["1"]
        with self.assertRaisesRegex(ValueError, "every frozen candidate"):
            compare(frozen, baseline, challenger)

    def test_unknown_labels_are_not_negatives(self):
        corpus = {"kind": "mtg"}
        task = {"labels": {"good": 1, "bad": 0, "unknown": None}}
        self.assertEqual(metric(corpus, task, {"unknown": 100, "good": 1, "bad": 0}, 10), 1)
        task["labels"]["good"] = None
        self.assertIsNone(metric(corpus, task, {"unknown": 100, "good": 1, "bad": 0}, 10))

    def test_producer_does_not_receive_labels_or_caption_target(self):
        inputs = producer_inputs(fixture()[0])
        self.assertEqual(inputs["corpora"]["captions"]["tasks"][0], {"id": "0", "text": "synthetic caption"})
        self.assertNotIn('"labels"', json.dumps(inputs))

    def test_artist_clusters_and_bootstrap_are_deterministic(self):
        deltas = {"artist1": [.5] * 100, "artist2": [-.5]}
        self.assertEqual(paired_interval(deltas, 1000, "seed"), paired_interval(deltas, 1000, "seed"))
        self.assertIsNone(paired_interval({"artist1": [.8] * 100}, 1000, "seed"))
        frozen, baseline, challenger = fixture()
        frozen["protocol"]["corpora"]["captions"]["tasks"][0]["cluster"] = "invented"
        with self.assertRaisesRegex(ValueError, "artist"):
            compare(frozen, baseline, challenger)

    def test_caption_import_preserves_literal_text_and_reports_missing_audio(self):
        raw = b"caption_id,track_id,caption,is_valid_subset,artist_id\n1,a,soft piano and spacious reverberation,True,artist\n2,b,other caption,True,other\n3,a,unvalidated caption,,artist\n"
        corpus = prepare("song_describer", raw, [recording("a", "artist")], "https://example.invalid", "fixture")
        self.assertEqual(corpus["tasks"][0]["text"], "soft piano and spacious reverberation")
        self.assertEqual(corpus["unavailableSourceItems"], 1)
        self.assertEqual(len(corpus["tasks"]), 1)
        validate_corpus(corpus)

    def test_mtg_explicit_negatives_and_unknown(self):
        records = [recording(str(i)) for i in range(3)]
        tracks = [dict(id=r["id"], artistId=r["artistId"], split="heldout", audioSHA256=r["audioSHA256"],
                       lowAudioSHA256="f" * 64, labels={"mood_relaxed": label} if label else {})
                  for r, label in zip(records, ("relaxed", "not_relaxed", None))]
        corpus = prepare("mtg", canonical(dict(version="automatic-mtg-corpus/v1", tracks=tracks)),
                         records, "https://example.invalid", "fixture")
        labels = next(t["labels"] for t in corpus["tasks"] if t["id"] == "mood_relaxed:relaxed")
        self.assertEqual(labels, {"0": 1, "1": 0, "2": None})

    def test_ccmsim_interval_and_dependency_checks(self):
        raw = b"audio_pair,overall_music_similarity\na-b,0.7\nb-c,0.2\n"
        records = [dict(recording(i), sourcePath=i + ".mp3") for i in "abc"]
        mapping = b"survey_audio_id,audio_file_path,survey_start_second,survey_end_second\na,a.mp3,0,20\nb,b.mp3,0,20\nc,c.mp3,0,20\n"
        corpus = prepare("ccmsim", raw, records, "https://example.invalid", "fixture", mapping=mapping)
        self.assertEqual(len({task["cluster"] for task in corpus["tasks"]}), 1)
        validate_corpus(corpus)
        records[0]["coverage"][0]["endMs"] = 10000
        with self.assertRaisesRegex(ValueError, "exact rated interval"):
            prepare("ccmsim", raw, records, "https://example.invalid", "fixture", mapping=mapping)

    def test_freeze_and_no_overwrite_and_duplicate_json_keys(self):
        frozen, _, _ = fixture()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            specification = copy.deepcopy(frozen["protocol"])
            write_new(root / "corpus.json", specification["corpora"]["captions"])
            specification["corpora"] = {"captions": "corpus.json"}
            self.assertEqual(freeze(specification, root), frozen)
            with self.assertRaises(FileExistsError):
                write_new(root / "corpus.json", {})
            (root / "duplicates.json").write_text('{"a": 1, "a": 2}')
            with self.assertRaisesRegex(ValueError, "duplicate JSON"):
                read_json(root / "duplicates.json")

    def test_unreviewed_overlap_or_development_cannot_promote(self):
        frozen, baseline, challenger = fixture()
        frozen["protocol"]["trainingOverlapAudits"]["captions"]["status"] = "unknown"
        frozen["protocolSHA256"] = sha256(canonical(frozen["protocol"]))
        baseline["protocolSHA256"] = challenger["protocolSHA256"] = frozen["protocolSHA256"]
        self.assertEqual(compare(frozen, baseline, challenger)["decision"], "retain_current_scorer")


if __name__ == "__main__":
    unittest.main()
