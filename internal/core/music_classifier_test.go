package core

import (
	"math"
	"strings"
	"testing"
)

func classifierFixture() MusicClassifierEvidence {
	identity := MusicClassifierIdentity{Model: "discogs-effnet", Revision: "1", WeightsSHA256: strings.Repeat("a", 64), MetadataSHA256: strings.Repeat("b", 64)}
	return MusicClassifierEvidence{
		Version: MusicClassifierEvidenceVersion, Encoder: identity,
		Preprocessing: "sample16k/v1", Runtime: "essentia/test", AudioSHA256: strings.Repeat("c", 64),
		Source: "licensed-dataset", SourceID: "recording-1", License: "CC-BY-4.0",
		Coverage: LibraryCLAPCoverage{CoveredSeconds: 10, Incomplete: true, PartialReason: "sample", Segments: []LibraryAudioInterval{{EndSeconds: 10}}},
		Heads:    []MusicClassifierHead{{Kind: "instrumentation", Model: identity, Classes: []string{"piano", "acousticguitar"}, Scores: []float32{1, .6}}},
	}
}

func TestDiscogsExportsShareEvidenceFamily(t *testing.T) {
	one := classifierFixture()
	one.Encoder.Model = "discogs-effnet-bs64-1"
	two := classifierFixture()
	two.Encoder.Model = "discogs-effnet-bsdynamic-1"
	two.Encoder.WeightsSHA256 = strings.Repeat("e", 64)
	if one.EvidenceFamily() != two.EvidenceFamily() {
		t.Fatal("original encoder exports counted as independent evidence")
	}
}

func TestClassifierEstimatesPreserveCompleteMeaningAndUnknownStrictEvidence(t *testing.T) {
	evidence := classifierFixture()
	for _, tc := range []struct {
		value string
		known bool
	}{{"piano", true}, {"acoustic guitar", true}, {"soft piano", false}, {"spacious reverberation", false}, {"violin", false}} {
		criterion := MusicalCriterion{Kind: "instrumentation", Value: tc.value}
		_, known := evidence.EstimatedScore(criterion)
		if known != tc.known {
			t.Errorf("%s availability=%v, want %v", tc.value, known, tc.known)
		}
		if evidence.StrictState(criterion) != EvidenceUnknown {
			t.Fatalf("uncalibrated %q must remain unknown", tc.value)
		}
	}
	firstFamily, firstFingerprint := evidence.EvidenceFamily(), evidence.Fingerprint()
	head := evidence.Heads[0]
	head.Model.Model = "style-head"
	head.Kind, head.Classes, head.Scores = "style", []string{"Rock---Art Rock"}, []float32{.7}
	evidence.Heads = append(evidence.Heads, head)
	if evidence.EvidenceFamily() != firstFamily || evidence.Fingerprint() == firstFingerprint {
		t.Fatal("heads must change the pinned scoring contract but not create independent evidence")
	}
	for _, value := range []string{"art rock", "rock"} {
		if score, ok := evidence.EstimatedScore(MusicalCriterion{Kind: "style", Value: value}); !ok || score < .69 {
			t.Fatalf("complete Discogs style %q unavailable", value)
		}
	}
}

func TestClassifierValidationRejectsInvalidScoresAndCoverage(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*MusicClassifierEvidence)
	}{
		{"nonfinite", func(e *MusicClassifierEvidence) { e.Heads[0].Scores[0] = float32(math.NaN()) }},
		{"out of range", func(e *MusicClassifierEvidence) { e.Heads[0].Scores[0] = 1.1 }},
		{"class shape", func(e *MusicClassifierEvidence) { e.Heads[0].Scores = nil }},
		{"duplicate classes", func(e *MusicClassifierEvidence) { e.Heads[0].Classes[1] = "piano" }},
		{"unidentified weights", func(e *MusicClassifierEvidence) { e.Encoder.WeightsSHA256 = "" }},
		{"missing license", func(e *MusicClassifierEvidence) { e.License = "" }},
		{"infinite coverage", func(e *MusicClassifierEvidence) { e.Coverage.CoveredSeconds = math.Inf(1) }},
		{"overlapping samples", func(e *MusicClassifierEvidence) {
			e.Coverage.CoveredSeconds = 15
			e.Coverage.Segments = append(e.Coverage.Segments, LibraryAudioInterval{StartSeconds: 5, EndSeconds: 10})
		}},
		{"inconsistent coverage", func(e *MusicClassifierEvidence) { e.Coverage.CoveredSeconds = 12 }},
		{"missing partial reason", func(e *MusicClassifierEvidence) { e.Coverage.PartialReason = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evidence := classifierFixture()
			if err := evidence.Validate(); err != nil {
				t.Fatal(err)
			}
			tc.edit(&evidence)
			if evidence.Validate() == nil {
				t.Fatal("invalid evidence accepted")
			}
			if _, ok := evidence.EstimatedScore(MusicalCriterion{Kind: "instrumentation", Value: "piano"}); ok {
				t.Fatal("invalid evidence used for scoring")
			}
		})
	}
}

func TestClassifierCanonicalVocalLabelsRemainEstimates(t *testing.T) {
	evidence := classifierFixture()
	evidence.Heads[0].Kind = "vocal"
	evidence.Heads[0].Classes = []string{"instrumental", "voice"}
	evidence.Heads[0].Scores = []float32{.2, .8}
	for _, value := range []string{"vocal", "vocals", "voice"} {
		criterion := MusicalCriterion{Kind: "vocal", Value: value}
		if score, known := evidence.EstimatedScore(criterion); !known || score < .79 {
			t.Fatalf("canonical vocal label %q unavailable", value)
		}
		if evidence.StrictState(criterion) != EvidenceUnknown {
			t.Fatal("vocal score became calibrated evidence")
		}
	}
	if _, known := evidence.EstimatedScore(MusicalCriterion{Kind: "vocal", Value: "soft vocals"}); known {
		t.Fatal("qualified vocal phrase was broadened")
	}
}
