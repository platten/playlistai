package core

import (
	"fmt"
	"math"
	"strings"
)

// Validate checks semantic invariants that JSON decoding and GBNF cannot.
func (m MusicIntent) Validate() error {
	if m.DurationSeconds < 0 || m.DurationSeconds > 24*60*60 {
		return fmt.Errorf("intent: duration must be between zero and 24 hours")
	}
	if m.Start != nil && (m.Start.Influence == InfluenceNegative || (m.Start.Kind != ReferenceArtist && m.Start.Kind != ReferenceAlbum && m.Start.Kind != ReferenceTrack) || strings.TrimSpace(m.Start.Query) == "" && m.Start.TrackID == "") {
		return fmt.Errorf("intent: invalid starting endpoint")
	}
	if m.VerificationPolicy != "" && m.VerificationPolicy != BestAvailable && m.VerificationPolicy != VerifiedOnly {
		return fmt.Errorf("intent: invalid verification policy")
	}
	for _, period := range m.Temporal {
		if (period.Basis != "composition" && period.Basis != "original_release") || period.StartYear < 1 || period.EndYear < period.StartYear || period.EndYear > 9999 {
			return fmt.Errorf("intent: invalid temporal requirement")
		}
		if period.Scope != "" && period.Scope != "playlist" && period.Scope != "journey_start" && period.Scope != "journey_end" {
			return fmt.Errorf("intent: invalid temporal scope")
		}
	}
	if m.Destination != nil && (m.Destination.Influence == InfluenceNegative || (m.Destination.Kind != ReferenceArtist && m.Destination.Kind != ReferenceAlbum && m.Destination.Kind != ReferenceTrack) || strings.TrimSpace(m.Destination.Query) == "" && m.Destination.TrackID == "") {
		return fmt.Errorf("intent: invalid final destination")
	}

	if len(m.AnchorAttempts) > 6 {
		return fmt.Errorf("intent: at most six anchor attempts are allowed")
	}
	for _, hint := range m.GenreExpansions {
		if strings.TrimSpace(hint.Genre) == "" || strings.TrimSpace(hint.Characteristics) == "" || len(hint.RelatedGenres) > 3 {
			return fmt.Errorf("intent: genre expansions need a genre, characteristics, and at most three related genres")
		}
		preserved := false
		for _, criterion := range m.EssentialCriteria {
			if (criterion.Kind == "style" || criterion.Kind == "genre") && strings.EqualFold(criterion.Value, hint.Genre) {
				preserved = true
			}
		}
		if !preserved {
			return fmt.Errorf("intent: expanded genre must remain an essential criterion")
		}
	}
	if _, err := m.Seed.Canonical(); err != nil {
		return fmt.Errorf("intent: %w", err)
	}
	if m.Mode != "" && m.Mode != ModeSimilar && m.Mode != ModeJourney {
		return fmt.Errorf("intent: invalid mode %q", m.Mode)
	}
	if len(m.InferredAnchors) > 3 {
		return fmt.Errorf("intent: at most 3 inferred anchors are allowed")
	}
	for _, group := range [][]IntentReference{m.References, m.RequiredTracks, m.Journey.Waypoints, anchorReferences(m.InferredAnchors)} {
		for _, ref := range group {
			if ref.Kind != ReferenceArtist && ref.Kind != ReferenceTrack && ref.Kind != ReferenceAlbum {
				return fmt.Errorf("intent: invalid reference kind %q", ref.Kind)
			}
			if ref.Influence != InfluencePositive && ref.Influence != InfluenceNegative {
				return fmt.Errorf("intent: invalid influence %q", ref.Influence)
			}
			if strings.TrimSpace(ref.Query) == "" && strings.TrimSpace(ref.TrackID) == "" {
				return fmt.Errorf("intent: reference has no query or track id")
			}
			if ref.Resolution != nil {
				if err := validateResolution(ref.Kind, *ref.Resolution); err != nil {
					return err
				}
			}
		}
	}
	for _, criterion := range m.EssentialCriteria {
		if err := validateStrength(criterion.Strength); err != nil {
			return err
		}
		switch criterion.Kind {
		case "genre", "style", "texture", "mood", "instrumentation", "vocal":
		default:
			return fmt.Errorf("intent: invalid essential criterion kind %q", criterion.Kind)
		}
		if strings.TrimSpace(criterion.Value) == "" {
			return fmt.Errorf("intent: essential criterion has no value")
		}
		switch criterion.Scope {
		case "", "playlist", "journey_start", "journey_end", "journey_via":
		default:
			return fmt.Errorf("intent: invalid essential criterion scope %q", criterion.Scope)
		}
	}
	for _, anchor := range m.InferredAnchors {
		if anchor.Reference.Influence != InfluencePositive {
			return fmt.Errorf("intent: inferred anchors must be positive references")
		}
		if anchor.Suitability.Score < -1 || anchor.Suitability.Score > 1 || math.IsNaN(anchor.Suitability.Score) {
			return fmt.Errorf("intent: invalid inferred anchor suitability")
		}
		switch anchor.Suitability.State {
		case "", EvidenceMatch, EvidenceMismatch, EvidenceUnknown, EvidenceUnsupported:
		default:
			return fmt.Errorf("intent: invalid inferred anchor suitability state %q", anchor.Suitability.State)
		}
	}
	for _, ref := range m.RequiredTracks {
		if ref.Kind != ReferenceTrack || ref.Influence != InfluencePositive {
			return fmt.Errorf("intent: required tracks must be positive track references")
		}
	}
	preferenceGroups := [][]IntentPreference{
		m.Preferences.Genres, m.Preferences.Styles, m.Preferences.Moods,
		m.Preferences.Instrumentation, m.Preferences.TextureDescriptions,
		m.Preferences.VocalPreferences,
	}
	for _, group := range preferenceGroups {
		for _, preference := range group {
			if err := validatePreferenceScope(preference); err != nil {
				return err
			}
			if strings.TrimSpace(preference.Value) == "" ||
				(preference.Influence != InfluencePositive && preference.Influence != InfluenceNegative) {
				return fmt.Errorf("intent: invalid semantic preference")
			}
		}
	}
	if vocal := m.Preferences.VocalPreference; vocal != nil &&
		(strings.TrimSpace(vocal.Value) == "" ||
			(vocal.Influence != InfluencePositive && vocal.Influence != InfluenceNegative)) {
		return fmt.Errorf("intent: invalid vocal preference")
	}
	if m.Preferences.VocalPreference != nil {
		if err := validatePreferenceScope(*m.Preferences.VocalPreference); err != nil {
			return err
		}
	}
	for _, constraint := range m.HardConstraints {
		if strings.TrimSpace(constraint.Kind) == "" || strings.TrimSpace(constraint.Value) == "" {
			return fmt.Errorf("intent: hard constraint must have kind and value")
		}
		if constraint.Supported != HardConstraintSupported(constraint.Kind) {
			return fmt.Errorf("intent: incorrect capability claim for hard constraint %q", constraint.Kind)
		}
	}
	c := m.Controls
	if !c.RecommendationMode.Valid() {
		return fmt.Errorf("intent: unknown recommendation mode %q", c.RecommendationMode)
	}
	for name, value := range map[string]float64{
		"audio weight": c.AudioWeight, "cooccurrence weight": c.CooccurrenceWeight,
		"discovery": c.Discovery, "artist diversity": c.ArtistDiversity,
		"transition smoothness": c.TransitionSmoothness,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return fmt.Errorf("intent: %s must be between 0 and 1", name)
		}
	}
	if c.TotalTrackCount != 0 && (c.TotalTrackCount < MinCount || c.TotalTrackCount > MaxCount) {
		return fmt.Errorf("intent: total track count must be between %d and %d", MinCount, MaxCount)
	}
	lastPosition := -1.0
	for _, point := range m.Journey.EnergyTrajectory {
		if point.Position < 0 || point.Position > 1 || point.Energy < 0 || point.Energy > 1 {
			return fmt.Errorf("intent: energy trajectory values must be between 0 and 1")
		}
		if point.Position < lastPosition {
			return fmt.Errorf("intent: energy trajectory positions must be ordered")
		}
		lastPosition = point.Position
	}
	return nil
}

func validateResolution(kind ReferenceKind, resolution ReferenceResolution) error {
	if resolution.Status != ResolutionResolved && resolution.Status != ResolutionAmbiguous && resolution.Status != ResolutionUnresolved {
		return fmt.Errorf("intent: invalid resolution status %q", resolution.Status)
	}
	if resolution.Status == ResolutionResolved && resolution.Selected == nil {
		return fmt.Errorf("intent: resolved reference has no selected entity")
	}
	if resolution.Selected != nil && resolution.Selected.Kind != kind {
		return fmt.Errorf("intent: resolution kind %q does not match reference kind %q", resolution.Selected.Kind, kind)
	}
	for _, candidate := range append(append([]ResolutionCandidate(nil), resolution.Alternatives...), dereferenceCandidate(resolution.Selected)...) {
		if candidate.Kind != kind || math.IsNaN(candidate.Confidence) || candidate.Confidence < 0 || candidate.Confidence > 1 {
			return fmt.Errorf("intent: invalid resolution candidate")
		}
		var total float64
		for _, representative := range candidate.Representatives {
			if strings.TrimSpace(representative.TrackID) == "" || math.IsNaN(representative.Weight) || representative.Weight <= 0 || representative.Weight > 1 {
				return fmt.Errorf("intent: invalid weighted representative")
			}
			total += representative.Weight
		}
		if len(candidate.Representatives) > 0 && math.Abs(total-1) > 0.001 {
			return fmt.Errorf("intent: representative weights must sum to one")
		}
	}
	return nil
}

func dereferenceCandidate(candidate *ResolutionCandidate) []ResolutionCandidate {
	if candidate == nil {
		return nil
	}
	return []ResolutionCandidate{*candidate}
}
