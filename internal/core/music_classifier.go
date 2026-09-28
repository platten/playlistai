package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

const MusicClassifierEvidenceVersion = 1

// MusicClassifierIdentity pins both weights and the ordered class vocabulary.
// Upstream filenames and reported revisions alone do not establish compatibility.
type MusicClassifierIdentity struct {
	Model          string `json:"model"`
	Revision       string `json:"revision"`
	WeightsSHA256  string `json:"weightsSha256"`
	MetadataSHA256 string `json:"metadataSha256"`
}

type MusicClassifierHead struct {
	Kind    string                  `json:"kind"`
	Model   MusicClassifierIdentity `json:"model"`
	Classes []string                `json:"classes"`
	Scores  []float32               `json:"scores"`
}

// MusicClassifierEvidence retains uncalibrated class scores separately from
// factual FeatureValues. All heads sharing an encoder are ONE evidence family.
// SourceID identifies the permitted audio source; audio itself is never stored.
type MusicClassifierEvidence struct {
	Version       int                     `json:"version"`
	Encoder       MusicClassifierIdentity `json:"encoder"`
	Preprocessing string                  `json:"preprocessing"`
	Runtime       string                  `json:"runtime"`
	AudioSHA256   string                  `json:"audioSha256"`
	Source        string                  `json:"source"`
	SourceID      string                  `json:"sourceId"`
	License       string                  `json:"license"`
	Coverage      LibraryCLAPCoverage     `json:"coverage"`
	Heads         []MusicClassifierHead   `json:"heads"`
}

func (e MusicClassifierEvidence) Validate() error {
	if e.Version != MusicClassifierEvidenceVersion || !validClassifierIdentity(e.Encoder) ||
		!classifierHash(e.AudioSHA256) || !classifierText(e.Preprocessing) || !classifierText(e.Runtime) ||
		!classifierText(e.Source) || !classifierText(e.SourceID) || !classifierText(e.License) {
		return fmt.Errorf("classifier evidence: missing or invalid identity/provenance")
	}
	if len(e.Heads) == 0 || len(e.Heads) > 16 {
		return fmt.Errorf("classifier evidence: expected 1–16 heads")
	}
	seenHeads := make(map[string]bool, len(e.Heads))
	for _, head := range e.Heads {
		if head.Kind != "instrumentation" && head.Kind != "vocal" && head.Kind != "mood" && head.Kind != "style" {
			return fmt.Errorf("classifier evidence: unsupported head kind %q", head.Kind)
		}
		if !validClassifierIdentity(head.Model) || seenHeads[head.Model.Model] || len(head.Classes) == 0 || len(head.Classes) > 1024 || len(head.Classes) != len(head.Scores) {
			return fmt.Errorf("classifier evidence: invalid head identity or shape")
		}
		seenHeads[head.Model.Model] = true
		seenClasses := make(map[string]bool, len(head.Classes))
		for i, label := range head.Classes {
			value := float64(head.Scores[i])
			if !classifierText(label) || seenClasses[label] || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
				return fmt.Errorf("classifier evidence: invalid class or score")
			}
			seenClasses[label] = true
		}
	}
	coverage := e.Coverage
	if len(coverage.Segments) == 0 || len(coverage.Segments) > 128 || math.IsNaN(coverage.CoveredSeconds) || math.IsInf(coverage.CoveredSeconds, 0) || coverage.CoveredSeconds <= 0 {
		return fmt.Errorf("classifier evidence: measured audio coverage required")
	}
	covered, end := 0.0, 0.0
	for _, interval := range coverage.Segments {
		if math.IsNaN(interval.StartSeconds) || math.IsNaN(interval.EndSeconds) || math.IsInf(interval.StartSeconds, 0) || math.IsInf(interval.EndSeconds, 0) || interval.StartSeconds < end || interval.EndSeconds <= interval.StartSeconds {
			return fmt.Errorf("classifier evidence: invalid or overlapping audio interval")
		}
		covered += interval.EndSeconds - interval.StartSeconds
		end = interval.EndSeconds
	}
	if math.Abs(covered-coverage.CoveredSeconds) > 0.001 || (coverage.Incomplete && !classifierText(coverage.PartialReason)) {
		return fmt.Errorf("classifier evidence: inconsistent coverage")
	}
	return nil
}

func validClassifierIdentity(m MusicClassifierIdentity) bool {
	return classifierText(m.Model) && classifierText(m.Revision) && classifierHash(m.WeightsSHA256) && classifierHash(m.MetadataSHA256)
}

func classifierText(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= 2048
}

func classifierHash(s string) bool {
	decoded, err := hex.DecodeString(s)
	return err == nil && len(decoded) == sha256.Size && s == strings.ToLower(s)
}

// EvidenceFamily excludes the heads: additional heads never add independent
// corroboration for a prediction produced by the same shared encoder.
func (e MusicClassifierEvidence) EvidenceFamily() string {
	if strings.HasPrefix(e.Encoder.Model, "discogs-effnet-bs") {
		// The original TensorFlow and ONNX exports are the same trained
		// encoder family, even though their file hashes differ.
		return "music-classifier/discogs-effnet-v1"
	}
	return "music-classifier/" + e.Encoder.Model + "/" + e.Encoder.WeightsSHA256
}

// Fingerprint pins scoring inputs independently of a particular audio sample.
func (e MusicClassifierEvidence) Fingerprint() string {
	type headContract struct {
		Kind    string
		Model   MusicClassifierIdentity
		Classes []string
	}
	models := make([]headContract, len(e.Heads))
	for i, head := range e.Heads {
		models[i] = headContract{head.Kind, head.Model, head.Classes}
	}
	payload, _ := json.Marshal(struct {
		Version       int
		Encoder       MusicClassifierIdentity
		Preprocessing string
		Runtime       string
		Heads         []headContract
	}{e.Version, e.Encoder, e.Preprocessing, e.Runtime, models})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// StrictState deliberately stays unknown. A high uncalibrated score, including
// 1.0, cannot satisfy a must-have or disprove an exclusion. Calibration requires
// a separately reviewed, independently evaluated policy before this can change.
func (e MusicClassifierEvidence) StrictState(MusicalCriterion) EvidenceState {
	return EvidenceUnknown
}

// EstimatedScore matches an entire supported facet, never a word extracted
// from a qualified phrase. "soft piano" therefore remains unavailable here;
// the complete phrase belongs to text/audio similarity. Missing labels are not
// negative labels. Callers rank this channel independently of cosine channels.
func (e MusicClassifierEvidence) EstimatedScore(criterion MusicalCriterion) (float64, bool) {
	if e.Validate() != nil {
		return 0, false
	}
	kind, want := criterion.Kind, NormalizeIdentityPart(criterion.Value)
	if kind == "genre" {
		kind = "style"
	}
	if kind == "vocal" && (want == "vocal" || want == "vocals") {
		want = "voice"
	}
	if kind == "mood" && (want == "relax" || want == "relaxing") {
		want = "relaxed"
	}
	best, available := 0.0, false
	for _, head := range e.Heads {
		if head.Kind != kind {
			continue
		}
		for i, label := range head.Classes {
			label = NormalizeIdentityPart(label)
			if label == want || classifierLabelMatches(kind, want, label) {
				best, available = math.Max(best, float64(head.Scores[i])), true
			}
		}
	}
	return best, available
}

func classifierLabelMatches(kind, want, label string) bool {
	if kind == "instrumentation" {
		// MTG's ordered vocabulary concatenates compound instrument names.
		return strings.ReplaceAll(want, " ", "") == label
	}
	if kind == "style" {
		// Discogs prefixes each style with its parent (e.g. Rock---Art Rock).
		// Match only the complete style or complete parent, never substrings.
		for _, part := range strings.Split(label, "---") {
			if strings.TrimSpace(part) == want {
				return true
			}
		}
	}
	return false
}
