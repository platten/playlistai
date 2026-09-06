// Package resolution applies the shared catalog resolver to a complete intent.
package resolution

import (
	"fmt"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type Issue struct {
	Kind         core.ReferenceKind         `json:"kind"`
	Query        string                     `json:"query"`
	Status       core.ResolutionStatus      `json:"status"`
	Required     bool                       `json:"required"`
	Alternatives []core.ResolutionCandidate `json:"alternatives"`
}

// Apply annotates every typed reference with the resolver result and selects
// the primary real track ID for compatibility with the current engine adapter.
// Issues remain structured so UI and service callers can decide how to handle
// ambiguity; Apply itself never guesses.
func Apply(resolver ports.ReferenceResolver, intent core.MusicIntent) (core.MusicIntent, []Issue) {
	intent = intent.Normalized()
	var issues []Issue
	intent.References, issues = applyList(resolver, intent.References, false, issues)
	intent.Journey.Waypoints, issues = applyList(resolver, intent.Journey.Waypoints, false, issues)
	intent.RequiredTracks, issues = applyList(resolver, intent.RequiredTracks, true, issues)
	return intent.Normalized(), issues
}

func applyList(resolver ports.ReferenceResolver, references []core.IntentReference, required bool, issues []Issue) ([]core.IntentReference, []Issue) {
	out := make([]core.IntentReference, len(references))
	for i, reference := range references {
		var result core.ReferenceResolution
		if reference.Resolution != nil && reference.Resolution.CatalogVersion == resolver.CatalogVersion() &&
			reference.Resolution.Status == core.ResolutionResolved && reference.Resolution.Selected != nil {
			result = *reference.Resolution
		} else {
			result = resolver.ResolveReference(reference)
		}
		reference.Resolution = &result
		if result.Status == core.ResolutionResolved && result.Selected != nil && len(result.Selected.Representatives) > 0 {
			reference.TrackID = result.Selected.Representatives[0].TrackID
		} else {
			reference.TrackID = ""
			issues = append(issues, Issue{Kind: reference.Kind, Query: reference.Query, Status: result.Status, Required: required, Alternatives: result.Alternatives})
		}
		out[i] = reference
	}
	return out, issues
}

func BlockingError(issues []Issue) error {
	for _, issue := range issues {
		label := strings.TrimSpace(issue.Query)
		if label == "" {
			label = string(issue.Kind)
		}
		if issue.Status == core.ResolutionAmbiguous {
			return fmt.Errorf("%w: %q matches %s", core.ErrAmbiguousReference, label, alternativeNames(issue.Alternatives))
		}
		if issue.Required && issue.Status == core.ResolutionUnresolved {
			return fmt.Errorf("%w: required track %q did not resolve", core.ErrRequiredTrackConflict, label)
		}
	}
	return nil
}

func alternativeNames(alternatives []core.ResolutionCandidate) string {
	names := make([]string, 0, len(alternatives))
	for _, alternative := range alternatives {
		name := alternative.Artist
		if alternative.Title != "" {
			name += " - " + alternative.Title
		}
		names = append(names, fmt.Sprintf("%q", name))
	}
	return strings.Join(names, ", ")
}
