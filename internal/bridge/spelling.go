package bridge

import (
	"fmt"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/resolution"
)

// Validate against fresh catalog alternatives, never an arbitrary recording ID
// supplied by a client. Source queries and evidence remain unchanged throughout.
func validateResolutionSelections(resolver ports.ReferenceResolver, intent core.MusicIntent, selections []ResolutionSelection) ([]ResolutionSelection, error) {
	refs := append([]core.IntentReference(nil), intent.References...)
	refs = append(refs, intent.RequiredTracks...)
	refs = append(refs, intent.Journey.Waypoints...)
	for _, ref := range []*core.IntentReference{intent.Start, intent.Destination} {
		if ref != nil {
			refs = append(refs, *ref)
		}
	}
	explicitRefCount := len(refs)
	for _, anchor := range intent.InferredAnchors {
		refs = append(refs, anchor.Reference)
	}
	out := append([]ResolutionSelection(nil), selections...)
	seen := map[string]bool{}
	for i := range out {
		selection := &out[i]
		key := string(selection.Kind) + "\x00" + strings.ToLower(strings.TrimSpace(selection.Query))
		if seen[key] || (selection.RejectSpelling && (selection.TrackID != "" || selection.IdentityID != "")) || (selection.IdentityID != "" && selection.TrackID != "") ||
			(selection.KeepAsDescription && (selection.Kind != core.ReferenceArtist || selection.RejectSpelling || selection.TrackID != "" || selection.IdentityID != "")) {
			return nil, fmt.Errorf("invalid or repeated reference choice")
		}
		seen[key] = true
		valid := false
		for index, ref := range refs {
			if ref.Kind != selection.Kind || !strings.EqualFold(strings.TrimSpace(ref.Query), strings.TrimSpace(selection.Query)) {
				continue
			}
			if selection.KeepAsDescription {
				if index < explicitRefCount {
					ref.TrackID, ref.Resolution, ref.SpellingDecision = "", nil, ""
					valid = valid || ref.Grounding != nil && (ref.Grounding.Truncated || len(ref.Grounding.Candidates) > 1) || resolver.ResolveReference(ref).Status == core.ResolutionAmbiguous
				}
				continue
			}
			if ref.Grounding != nil && (ref.Grounding.Truncated || len(ref.Grounding.Candidates) > 1) {
				if selection.IdentityID != "" {
					for _, candidate := range ref.Grounding.Candidates {
						if candidate.ID == selection.IdentityID && candidate.Kind == selection.Kind {
							valid = true
						}
					}
					continue
				}
				return nil, fmt.Errorf("reference choice for %q cannot select among unresolved provider identities", selection.Query)
			}
			ref.TrackID, ref.Resolution, ref.SpellingDecision = "", nil, ""
			result := resolver.ResolveReference(ref)
			if ref.Grounding != nil && !ref.Grounding.Truncated && len(ref.Grounding.Candidates) == 1 && result.Status == core.ResolutionResolved && result.Selected != nil {
				for _, track := range result.Selected.Representatives {
					if selection.TrackID != "" && track.TrackID == selection.TrackID {
						valid = true
					}
				}
			}
			if result.Status != core.ResolutionAmbiguous {
				continue
			}
			for _, candidate := range result.Alternatives {
				spelling := resolution.IsSpellingCandidate(candidate)
				if selection.RejectSpelling {
					valid = valid || spelling && ref.Kind == core.ReferenceArtist
					continue
				}
				for _, track := range candidate.Representatives {
					if selection.TrackID != "" && track.TrackID == selection.TrackID {
						valid, selection.spellingAccepted = true, spelling
					}
				}
			}
		}
		if !valid {
			return nil, fmt.Errorf("reference choice for %q is no longer an offered catalog alternative", selection.Query)
		}
	}
	return out, nil
}
