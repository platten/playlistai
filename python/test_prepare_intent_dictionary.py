import copy
import json
import unittest

from prepare_intent_dictionary import ROOT, validate


class IntentDictionaryTests(unittest.TestCase):
    def setUp(self):
        self.document = json.loads((ROOT / "internal/musicconcepts/concepts.json").read_text(encoding="utf-8"))

    def test_current_registry_and_absent_explanations_validate(self):
        validate(self.document)
        without_explanations = copy.deepcopy(self.document)
        for concept in without_explanations["concepts"]:
            concept.pop("explanation", None)
        validate(without_explanations)

    def test_explanations_are_optional_single_paragraph_strings(self):
        for explanation in (None, [], {}, 42, " padded ", "first\nsecond", "first\rsecond"):
            with self.subTest(explanation=explanation):
                self.document["concepts"][0]["explanation"] = explanation
                with self.assertRaisesRegex(ValueError, "explanation"):
                    validate(self.document)

    def test_prior_version_requires_explicit_contract_update(self):
        self.document["version"] = "music-concepts/v5"
        with self.assertRaisesRegex(ValueError, "version"):
            validate(self.document)


if __name__ == "__main__":
    unittest.main()
