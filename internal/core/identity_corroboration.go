package core

import (
	"encoding/hex"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

const ArtistCoperformanceMethod = "recording-coperformance/v1"

// IdentityCorroboration preserves a provider's independently supported choice
// among the original candidates. It never represents explicit user consent.
type IdentityCorroboration struct {
	SelectedID string                         `json:"selectedId"`
	Method     string                         `json:"method"`
	Supports   []IdentityCorroborationSupport `json:"supports"`
}

type IdentityCorroborationSupport struct {
	AnchorID     string        `json:"anchorId"`
	RecordingIDs []string      `json:"recordingIds"`
	Source       ContextSource `json:"source"`
}

var corroborationUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validCorroborationID(id string) bool {
	return id != "00000000-0000-0000-0000-000000000000" && corroborationUUID.MatchString(id)
}

// CorroboratedArtist validates structural provenance, not musical suitability
// or the source's factual truth. The provider must inspect the complete rival
// set and establish a unique positive collaborator before creating this proof.
func (g *IdentityGrounding) CorroboratedArtist() (IdentityCandidate, bool) {
	if g == nil || (!ValidIdentityGroundingProvider(g.Provider) || !slices.Contains(strings.Split(g.Provider, "+"), "MusicBrainz")) || g.Truncated || g.Confirmed || len(g.Candidates) < 2 || len(g.Candidates) > 64 || g.Corroboration == nil {
		return IdentityCandidate{}, false
	}
	proof := g.Corroboration
	if proof.Method != ArtistCoperformanceMethod || len(proof.Supports) < 1 || len(proof.Supports) > 2 || !validCorroborationID(proof.SelectedID) {
		return IdentityCandidate{}, false
	}
	candidates := make(map[string]bool, len(g.Candidates))
	var selected IdentityCandidate
	for _, candidate := range g.Candidates {
		id := strings.ToLower(candidate.ID)
		if candidate.Kind != ReferenceArtist || !validCorroborationID(candidate.ID) || strings.TrimSpace(candidate.Name) == "" || candidates[id] {
			return IdentityCandidate{}, false
		}
		candidates[id] = true
		if strings.EqualFold(candidate.ID, proof.SelectedID) {
			selected = candidate
		}
	}
	if selected.ID == "" {
		return IdentityCandidate{}, false
	}
	anchors, recordings := map[string]bool{}, map[string]bool{}
	for _, support := range proof.Supports {
		anchor := strings.ToLower(support.AnchorID)
		if !validCorroborationID(support.AnchorID) || candidates[anchor] || anchors[anchor] || len(support.RecordingIDs) == 0 || !validCorroborationSource(support.Source, anchor) {
			return IdentityCandidate{}, false
		}
		anchors[anchor] = true
		local := map[string]bool{}
		for _, recordingID := range support.RecordingIDs {
			id := strings.ToLower(recordingID)
			if !validCorroborationID(recordingID) || local[id] {
				return IdentityCandidate{}, false
			}
			local[id], recordings[id] = true, true
		}
	}
	if len(recordings) < 2 {
		return IdentityCandidate{}, false
	}
	return selected, true
}

func validCorroborationSource(source ContextSource, anchor string) bool {
	if !strings.EqualFold(source.Provider, "MusicBrainz") || source.License != "CC0-1.0" || len(source.Revision) != 64 {
		return false
	}
	if _, err := hex.DecodeString(source.Revision); err != nil {
		return false
	}
	location, err := url.Parse(source.URL)
	return err == nil && location.Scheme == "https" && strings.EqualFold(location.Host, "musicbrainz.org") && location.User == nil && location.Fragment == "" && location.RawPath == "" && location.Path == "/ws/2/artist/"+anchor
}

func cloneIdentityCorroboration(in *IdentityCorroboration) *IdentityCorroboration {
	if in == nil {
		return nil
	}
	out := *in
	out.Supports = append([]IdentityCorroborationSupport(nil), in.Supports...)
	for i := range out.Supports {
		out.Supports[i].RecordingIDs = append([]string(nil), in.Supports[i].RecordingIDs...)
	}
	return &out
}
