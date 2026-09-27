package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const EnhancedSearchPolicyVersion = "enhanced-search/v2"
const AutomaticSearchPolicyVersion = "automatic-search/v1"

// SearchProgress describes comparison work, independently of playlist count.
type SearchProgress struct {
	Stage                string `json:"stage"`
	ElapsedMilliseconds  int64  `json:"elapsedMilliseconds"`
	CandidatesConsidered int    `json:"candidatesConsidered"`
	CandidatesEligible   int    `json:"candidatesEligible"`
	CandidatesSupported  int    `json:"candidatesSupported"`
	StoppingReason       string `json:"stoppingReason,omitempty"`
}

// SearchSnapshot retains the actual pool and derived evidence used by a run.
// Result has a nil Search field, preventing recursive snapshots. No raw audio
// or provider credentials belong here. A seed alone is not a provider snapshot.
type SearchSnapshot struct {
	GenerationLimitMilliseconds int64        `json:"generationLimitMilliseconds,omitempty"`
	ID                          string       `json:"id"`
	PolicyVersion               string       `json:"policyVersion"`
	QueryPolicyVersion          string       `json:"queryPolicyVersion"`
	EvidencePolicyVersion       string       `json:"evidencePolicyVersion"`
	Profile                     TasteProfile `json:"profile"`
	Candidates                  []Candidate  `json:"candidates"`
	EligibleCandidates          []Candidate  `json:"eligibleCandidates"`
	Considered                  int          `json:"considered"`
	Eligible                    int          `json:"eligible"`
	StopReason                  string       `json:"stopReason"`
	Result                      *Playlist    `json:"result,omitempty"`
}

// FreezeSearch copies the complete result after all evidence collectors have
// finished. Callers cannot mutate saved replay inputs through shared slices.
func FreezeSearch(search SearchSnapshot, result Playlist) (*SearchSnapshot, error) {
	result.Search = nil
	search.ID, search.Result = "", &result
	blob, err := json.Marshal(search)
	if err != nil {
		return nil, err
	}
	var frozen SearchSnapshot
	if err := json.Unmarshal(blob, &frozen); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(blob)
	frozen.ID = hex.EncodeToString(sum[:])
	return &frozen, nil
}

func (s *SearchSnapshot) Validate() error {
	if s == nil || s.Result == nil || s.Result.Search != nil || (s.PolicyVersion != EnhancedSearchPolicyVersion && s.PolicyVersion != "enhanced-search/v1" && s.PolicyVersion != AutomaticSearchPolicyVersion) {
		return fmt.Errorf("saved search snapshot is missing or incompatible; regenerate explicitly")
	}
	copy := *s
	copy.ID = ""
	blob, err := json.Marshal(copy)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(blob)
	if s.ID != hex.EncodeToString(sum[:]) {
		return fmt.Errorf("saved search snapshot fingerprint does not match; regenerate explicitly")
	}
	return nil
}
