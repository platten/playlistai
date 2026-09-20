// Package resolution applies the shared catalog resolver to a complete intent.
package resolution

import (
	"fmt"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type Issue struct {
	Kind                core.ReferenceKind         `json:"kind"`
	Influence           core.Influence             `json:"influence"`
	Query               string                     `json:"query"`
	Status              core.ResolutionStatus      `json:"status"`
	Required            bool                       `json:"required"`
	Inferred            bool                       `json:"inferred"`
	Role                string                     `json:"role"`
	Alternatives        []core.ResolutionCandidate `json:"alternatives"`
	GroundingCandidates []core.IdentityCandidate   `json:"groundingCandidates,omitempty"`
	GroundingTruncated  bool                       `json:"groundingTruncated,omitempty"`
	SpellingSuggestion  *core.ResolutionCandidate  `json:"spellingSuggestion,omitempty"`
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
	for _, endpoint := range []**core.IntentReference{&intent.Start, &intent.Destination} {
		if *endpoint == nil {
			continue
		}
		resolved, next := applyList(resolver, []core.IntentReference{**endpoint}, true, issues)
		issues = next
		*endpoint = &resolved[0]
	}
	for index := range intent.InferredAnchors {
		before := len(issues)
		resolved, next := applyList(resolver, []core.IntentReference{intent.InferredAnchors[index].Reference}, false, issues)
		issues = next
		intent.InferredAnchors[index].Reference = resolved[0]
		if len(issues) > before {
			issues[len(issues)-1].Inferred = true
			issues[len(issues)-1].Role = intent.InferredAnchors[index].Role
		}
	}
	return intent.Normalized(), issues
}

func applyList(resolver ports.ReferenceResolver, references []core.IntentReference, required bool, issues []Issue) ([]core.IntentReference, []Issue) {
	out := make([]core.IntentReference, len(references))
	for i, reference := range references {
		explicitSelection := strings.TrimSpace(reference.TrackID) != ""
		var result core.ReferenceResolution
		groundingResolved := false
		if reference.SpellingDecision != "accepted" && reference.Resolution != nil && reference.Resolution.Selected != nil && IsSpellingCandidate(*reference.Resolution.Selected) {
			reference.TrackID, reference.Resolution = "", nil
		}
		if reference.SpellingDecision == "original" {
			reference.TrackID, reference.Resolution = "", nil
		}
		if !explicitSelection && reference.Grounding != nil && !reference.Grounding.Truncated && len(reference.Grounding.Candidates) == 1 && reference.Grounding.Candidates[0].Kind == core.ReferenceTrack {
			grounded := reference
			grounded.TrackID = "musicbrainz:" + reference.Grounding.Candidates[0].ID
			result = resolver.ResolveReference(grounded)
			groundingResolved = result.Status == core.ResolutionResolved && result.Selected != nil
		}
		if groundingResolved {
			// The catalog explicitly correlated this recording MBID.
		} else if reference.Resolution != nil && reference.Resolution.CatalogVersion == resolver.CatalogVersion() &&
			(reference.Kind == core.ReferenceAlbum || reference.Resolution.Status == core.ResolutionResolved && reference.Resolution.Selected != nil) {
			result = *reference.Resolution
		} else {
			result = resolver.ResolveReference(reference)
		}
		groundingAmbiguous := reference.Grounding != nil && (reference.Grounding.Truncated || len(reference.Grounding.Candidates) > 1)
		if groundingAmbiguous && !explicitSelection {
			// Catalog candidates do not carry MusicBrainz MBIDs, so they cannot
			// prove which homonymous provider identity the user meant. Require a
			// more specific prompt instead of presenting an unrelated choice.
			result.Alternatives = nil
			result.Status = core.ResolutionAmbiguous
			result.Selected = nil
		} else if reference.Grounding != nil && len(reference.Grounding.Candidates) == 1 && !explicitSelection && result.Status == core.ResolutionResolved && result.Selected != nil && !groundingMatchesSelected(reference.Grounding.Candidates[0], *result.Selected) {
			// A unique provider identity still cannot authenticate a differently
			// named catalog entity. Preserve it as an explicit confirmation rather
			// than silently treating a textual catalog hit as the same identity.
			result.Alternatives = []core.ResolutionCandidate{*result.Selected}
			result.Status = core.ResolutionAmbiguous
			result.Selected = nil
		}
		reference.Resolution = &result
		if result.Status == core.ResolutionResolved && result.Selected != nil && len(result.Selected.Representatives) > 0 {
			reference.TrackID = result.Selected.Representatives[0].TrackID
		} else {
			reference.TrackID = ""
			issue := Issue{Kind: reference.Kind, Influence: reference.Influence, Query: reference.Query, Status: result.Status, Required: required, Alternatives: result.Alternatives}
			if reference.Grounding != nil {
				issue.GroundingCandidates = append([]core.IdentityCandidate(nil), reference.Grounding.Candidates...)
				issue.GroundingTruncated = reference.Grounding.Truncated
			}
			if !groundingAmbiguous && reference.SpellingDecision == "" && result.Status == core.ResolutionAmbiguous && len(result.Alternatives) == 1 && IsSpellingCandidate(result.Alternatives[0]) {
				candidate := result.Alternatives[0]
				issue.SpellingSuggestion = &candidate
			}
			issues = append(issues, issue)
		}
		out[i] = reference
	}
	return out, issues
}

func groundingMatchesSelected(identity core.IdentityCandidate, selected core.ResolutionCandidate) bool {
	if core.NormalizeIdentityPart(identity.Name) != core.NormalizeIdentityPart(selected.Artist) {
		return false
	}
	if identity.Kind == core.ReferenceTrack {
		return core.NormalizeIdentityPart(identity.Title) == core.NormalizeIdentityPart(selected.Title)
	}
	return identity.Kind == core.ReferenceArtist
}

// IsSpellingCandidate identifies a proposal that needs a user's identity choice.
func IsSpellingCandidate(candidate core.ResolutionCandidate) bool {
	for _, evidence := range candidate.Evidence {
		if evidence.Match == "spelling" {
			return true
		}
	}
	return false
}

// SpellingConfirmationError runs before online recovery, which must not choose
// another artist while a user's spelling decision is still pending.
func SpellingConfirmationError(issues []Issue) error {
	for _, issue := range issues {
		if issue.Inferred || issue.Status != core.ResolutionAmbiguous {
			continue
		}
		for _, candidate := range issue.Alternatives {
			if IsSpellingCandidate(candidate) {
				return fmt.Errorf("%w: confirm the artist spelling for %q", core.ErrAmbiguousReference, issue.Query)
			}
		}
	}
	return nil
}

func BlockingError(issues []Issue) error {
	for _, issue := range issues {
		label := strings.TrimSpace(issue.Query)
		if label == "" {
			label = string(issue.Kind)
		}
		if issue.Status == core.ResolutionAmbiguous && !issue.Inferred {
			matches := ""
			if len(issue.GroundingCandidates) > 1 || issue.GroundingTruncated {
				matches = groundingNames(issue.GroundingCandidates, issue.GroundingTruncated)
			} else {
				matches = alternativeNames(issue.Alternatives)
			}
			return fmt.Errorf("%w: %q matches %s", core.ErrAmbiguousReference, label, matches)
		}
		if issue.Required && issue.Status == core.ResolutionUnresolved {
			return fmt.Errorf("%w: required track %q did not resolve", core.ErrRequiredTrackConflict, label)
		}
	}
	return nil
}

func groundingNames(candidates []core.IdentityCandidate, truncated bool) string {
	names := make([]string, 0, len(candidates)+1)
	for _, candidate := range candidates {
		name := strings.TrimSpace(candidate.Name)
		if candidate.Title != "" {
			name += " - " + candidate.Title
		}
		if candidate.Disambiguation != "" {
			name += " (" + candidate.Disambiguation + ")"
		}
		if name != "" {
			names = append(names, fmt.Sprintf("%q", name))
		}
	}
	if truncated {
		names = append(names, "additional provider identities")
	}
	if len(names) == 0 {
		return "multiple provider identities"
	}
	return strings.Join(names, ", ")
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
