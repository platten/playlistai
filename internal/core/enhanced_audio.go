package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

const EnhancedAudioPolicyVersion = "enhanced-hybrid/v2"

// MERTSimilarityQuery records one resolved audio query inside a logical
// reference group. Artist references can contain several weighted recordings;
// unrelated user references remain separate groups.
type MERTSimilarityQuery struct {
	GroupID          string   `json:"groupId"`
	Track            TrackRef `json:"track"`
	Weight           float64  `json:"weight"`
	RepresentationID string   `json:"representationId,omitempty"`
}

// MERTSimilarityHit is a frozen nearest-neighbor result. Representation is
// retained so same-version replay never searches a cache that may have changed.
type MERTSimilarityHit struct {
	GroupID        string              `json:"groupId"`
	QueryTrackID   string              `json:"queryTrackId"`
	TrackID        string              `json:"trackId"`
	Rank           int                 `json:"rank"`
	Score          float64             `json:"score"`
	QueryWeight    float64             `json:"queryWeight"`
	Representation AudioRepresentation `json:"representation"`
}

// MERTSimilaritySearch describes one bounded, immutable search view. Recorded
// distinguishes an executed empty search from legacy evidence with no search.
type MERTSimilaritySearch struct {
	Recorded         bool                        `json:"recorded"`
	CatalogVersion   string                      `json:"catalogVersion"`
	Model            AudioRepresentationIdentity `json:"model"`
	ViewFingerprint  string                      `json:"viewFingerprint,omitempty"`
	SearchableTracks int64                       `json:"searchableTracks"`
	Queries          []MERTSimilarityQuery       `json:"queries"`
	Hits             []MERTSimilarityHit         `json:"hits"`
}

// EnhancedAudioInput is the serializable, derived-only evidence captured before
// ranking. It contains no PCM or model intermediates. Centroids are audio-only;
// callers must keep explicit negative feedback separate from exposure.
type EnhancedAudioInput struct {
	PolicyVersion    string                         `json:"policyVersion"`
	CatalogVersion   string                         `json:"catalogVersion"`
	DSPVersion       string                         `json:"dspVersion"`
	Model            AudioRepresentationIdentity    `json:"model"`
	DSP              map[string]DSPAnalysis         `json:"dsp"`
	Representations  map[string]AudioRepresentation `json:"representations"`
	PositiveCentroid []float32                      `json:"positiveCentroid,omitempty"`
	NegativeCentroid []float32                      `json:"negativeCentroid,omitempty"`
	MERTSearch       *MERTSimilaritySearch          `json:"mertSearch,omitempty"`
}

// EnhancedAudioSnapshot owns a deep copy of its input. Accessors return copies
// so a caller cannot change a running generation or its cache fingerprint.
type EnhancedAudioSnapshot struct {
	data        EnhancedAudioInput
	fingerprint string
}

func NewEnhancedAudioSnapshot(input EnhancedAudioInput) (*EnhancedAudioSnapshot, error) {
	if input.PolicyVersion == "" {
		input.PolicyVersion = EnhancedAudioPolicyVersion
	}
	b, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	s := &EnhancedAudioSnapshot{}
	if err := s.UnmarshalJSON(b); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *EnhancedAudioSnapshot) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("null"), nil
	}
	return json.Marshal(s.data)
}

func (s *EnhancedAudioSnapshot) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return errors.New("enhanced audio snapshot must be an object")
	}
	var input EnhancedAudioInput
	if err := json.Unmarshal(b, &input); err != nil {
		return err
	}
	canonical, err := json.Marshal(input)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(canonical)
	s.data, s.fingerprint = input, hex.EncodeToString(digest[:])
	return nil
}

func (s *EnhancedAudioSnapshot) Fingerprint() string {
	if s == nil {
		return ""
	}
	return s.fingerprint
}
func (s *EnhancedAudioSnapshot) Input() EnhancedAudioInput {
	if s == nil {
		return EnhancedAudioInput{}
	}
	b, _ := json.Marshal(s.data)
	var out EnhancedAudioInput
	_ = json.Unmarshal(b, &out)
	return out
}
