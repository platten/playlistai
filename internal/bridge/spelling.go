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
	for _, anchor := range intent.InferredAnchors {
		refs = append(refs, anchor.Reference)
	}
	out := append([]ResolutionSelection(nil), selections...)
	seen := map[string]bool{}
	for i := range out {
		selection := &out[i]
		key := string(selection.Kind) + "\x00" + strings.ToLower(strings.TrimSpace(selection.Query))
		if seen[key] || (selection.RejectSpelling && selection.TrackID != "") {
			return nil, fmt.Errorf("invalid or repeated reference choice")
		}
		seen[key] = true
		valid := false
		for _, ref := range refs {
			if ref.Kind != selection.Kind || !strings.EqualFold(strings.TrimSpace(ref.Query), strings.TrimSpace(selection.Query)) {
				continue
			}
			ref.TrackID, ref.Resolution, ref.SpellingDecision = "", nil, ""
			result := resolver.ResolveReference(ref)
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
