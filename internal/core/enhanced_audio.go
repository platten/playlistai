package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

const EnhancedAudioPolicyVersion = "enhanced-hybrid/v1"

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
