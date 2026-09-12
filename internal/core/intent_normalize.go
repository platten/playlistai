package core

import "strings"

// Normalized is the compatibility entry point for a canonical request. It does
// not resolve entities or infer musical evidence. The versioned JSON remains
// unchanged while migration, canonicalization and runtime assessment are kept
// as separate operations.
func (m MusicIntent) Normalized() MusicIntent {
	out := normalizeIntent(migrateStoredIntent(m))
	// Runtime capabilities are assessed again for each request, never trusted
	// merely because an older result serialized an enforcement claim.
	out.Capabilities = intentCapabilities()
	out.backfillEngineAdapter()
	return out
}

func normalizeIntent(out MusicIntent) MusicIntent {
	if out.DurationSeconds > 0 && out.DurationToleranceSeconds == 0 {
		// Missing tolerance in historical requests adopts the documented
		// one-minute default; no historical target or explicit count is changed.
		out.DurationToleranceSeconds = DefaultDurationToleranceSeconds
	}
	out.Translation = cloneTranslation(out.Translation)
	if out.Start != nil {
		refs := cleanReferences([]IntentReference{*out.Start}, false)
		if len(refs) > 0 {
			out.Start = &refs[0]
		}
	}
	if out.Destination != nil {
		refs := cleanReferences([]IntentReference{*out.Destination}, false)
		if len(refs) > 0 {
			out.Destination = &refs[0]
		}
	}
	out.References = cleanReferences(out.References, false)
	out.InferredAnchors = cleanInferredAnchors(out.InferredAnchors)
	out.RequiredTracks = cleanReferences(out.RequiredTracks, true)
	out.EssentialCriteria = cleanCriteria(out.EssentialCriteria)
	out.Journey.Waypoints = cleanReferences(out.Journey.Waypoints, false)
	out.Preferences = cleanPreferences(out.Preferences)
	if genre, ok := SinglePlaylistGenre(out); ok {
		found := false
		for _, c := range out.EssentialCriteria {
			found = found || (c.Scope == "playlist" && (c.Kind == "genre" || c.Kind == "style") && CanonicalStyle(c.Value) == CanonicalStyle(genre.Value))
		}
		if !found {
			out.EssentialCriteria = append(out.EssentialCriteria, genre)
		}
	}
	out.HardConstraints = cleanHardConstraints(out.HardConstraints)
	out.Unsupported = cleanUnsupported(out.Unsupported)
	out.Unsupported = addUnsupportedConstraints(out.Unsupported, out.HardConstraints)

	out.Controls.TotalTrackCount = clampInt(orDefaultInt(out.Controls.TotalTrackCount, DefaultCount), MinCount, MaxCount)
	if out.Controls.AudioWeight == 0 && out.Controls.CooccurrenceWeight == 0 {
		out.Controls.AudioWeight, out.Controls.CooccurrenceWeight = 0.5, 0.5
	}
	out.Controls.AudioWeight = clampFloat(out.Controls.AudioWeight, 0, 1)
	out.Controls.CooccurrenceWeight = clampFloat(out.Controls.CooccurrenceWeight, 0, 1)
	out.Controls.Discovery = clampFloat(out.Controls.Discovery, 0, 1)
	out.Controls.ArtistDiversity = clampFloat(out.Controls.ArtistDiversity, 0, 1)
	out.Controls.TransitionSmoothness = clampFloat(out.Controls.TransitionSmoothness, 0, 1)
	if seed, err := out.Seed.Canonical(); err == nil {
		out.Seed = seed
	}

	if out.Mode != ModeSimilar && out.Mode != ModeJourney {
		if len(out.Journey.Waypoints) >= 2 || positiveReferenceCount(out.References) >= 2 {
			out.Mode = ModeJourney
		} else {
			out.Mode = ModeSimilar
		}
	}
	return out
}

func anchorReferences(anchors []InferredAnchor) []IntentReference {
	result := make([]IntentReference, 0, len(anchors))
	for _, anchor := range anchors {
		result = append(result, anchor.Reference)
	}
	return result
}

func cleanInferredAnchors(in []InferredAnchor) []InferredAnchor {
	var out []InferredAnchor
	seen := map[string]struct{}{}
	for _, anchor := range in {
		refs := cleanReferences([]IntentReference{anchor.Reference}, false)
		if len(refs) == 0 {
			continue
		}
		anchor.Reference = refs[0]
		anchor.Role = strings.TrimSpace(anchor.Role)
		anchor.Reason = strings.TrimSpace(anchor.Reason)
		anchor.Suitability.Detail = strings.TrimSpace(anchor.Suitability.Detail)
		if anchor.Suitability.State == "" {
			anchor.Suitability.State = EvidenceUnknown
		}
		key := string(anchor.Reference.Kind) + "\x00" + strings.ToLower(anchor.Reference.Query) + "\x00" + anchor.Reference.TrackID
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, anchor)
	}
	return out
}

func cleanCriteria(in []MusicalCriterion) []MusicalCriterion {
	var out []MusicalCriterion
	seen := map[string]struct{}{}
	for _, criterion := range in {
		criterion.Kind = strings.ToLower(strings.TrimSpace(criterion.Kind))
		criterion.Value = strings.TrimSpace(criterion.Value)
		criterion.Scope = strings.ToLower(strings.TrimSpace(criterion.Scope))
		if criterion.Scope == "" {
			criterion.Scope = "playlist"
		}
		if criterion.Kind == "" || criterion.Value == "" {
			continue
		}
		criterion.Evidence = append([]SourceEvidence(nil), criterion.Evidence...)
		key := criterion.Scope + "\x00" + criterion.Kind + "\x00" + strings.ToLower(criterion.Value) + "\x00" + criterion.Group + "\x00" + criterion.Strength
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, criterion)
	}
	return out
}

func cleanReferences(in []IntentReference, required bool) []IntentReference {
	var out []IntentReference
	seen := map[string]struct{}{}
	for _, ref := range in {
		ref.Evidence = append([]SourceEvidence(nil), ref.Evidence...)
		ref.Query = strings.TrimSpace(ref.Query)
		ref.TrackID = strings.TrimSpace(ref.TrackID)
		ref.Resolution = cleanResolution(ref.Resolution)
		if ref.Kind == "" {
			if ref.TrackID != "" || required {
				ref.Kind = ReferenceTrack
			} else {
				ref.Kind = ReferenceArtist
			}
		}
		if ref.Influence == "" {
			ref.Influence = InfluencePositive
		}
		if ref.Query == "" && ref.TrackID == "" {
			continue
		}
		key := string(ref.Kind) + "\x00" + strings.ToLower(ref.Query) + "\x00" + ref.TrackID + "\x00" + string(ref.Influence)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func cleanResolution(in *ReferenceResolution) *ReferenceResolution {
	if in == nil {
		return nil
	}
	out := *in
	out.CatalogVersion = strings.TrimSpace(out.CatalogVersion)
	out.Alternatives = append([]ResolutionCandidate(nil), out.Alternatives...)
	if out.Selected != nil {
		selected := *out.Selected
		selected.Representatives = append([]WeightedTrack(nil), selected.Representatives...)
		out.Selected = &selected
	}
	return &out
}

func cleanPreferences(p SemanticPreferences) SemanticPreferences {
	p.VocalPreferences = cleanPreferenceList(p.VocalPreferences)
	p.Genres = cleanPreferenceList(p.Genres)
	p.Styles = cleanPreferenceList(p.Styles)
	p.Moods = cleanPreferenceList(p.Moods)
	p.Instrumentation = cleanPreferenceList(p.Instrumentation)
	p.TextureDescriptions = cleanPreferenceList(p.TextureDescriptions)
	if p.VocalPreference != nil {
		preference := *p.VocalPreference
		preference.Evidence = append([]SourceEvidence(nil), preference.Evidence...)
		p.VocalPreference = &preference // normalization must not edit the caller/cache
		p.VocalPreference.Value = strings.TrimSpace(p.VocalPreference.Value)
		if p.VocalPreference.Influence == "" {
			p.VocalPreference.Influence = InfluencePositive
		}
		if p.VocalPreference.Value == "" {
			p.VocalPreference = nil
		}
	}
	return p
}

func cleanPreferenceList(in []IntentPreference) []IntentPreference {
	var out []IntentPreference
	for _, preference := range in {
		preference.Evidence = append([]SourceEvidence(nil), preference.Evidence...)
		preference.Value = strings.TrimSpace(preference.Value)
		if preference.Value == "" {
			continue
		}
		if preference.Influence == "" {
			preference.Influence = InfluencePositive
		}
		out = append(out, preference)
	}
	return out
}

func cleanHardConstraints(in []HardConstraint) []HardConstraint {
	var out []HardConstraint
	for _, constraint := range in {
		constraint.Kind = strings.TrimSpace(constraint.Kind)
		constraint.Value = strings.TrimSpace(constraint.Value)
		if constraint.Kind != "" && constraint.Value != "" {
			constraint.Supported = HardConstraintSupported(constraint.Kind)
			constraint.RuntimeEnforced = constraint.Supported
			out = append(out, constraint)
		}
	}
	return out
}

func cleanUnsupported(in []UnsupportedRequirement) []UnsupportedRequirement {
	var out []UnsupportedRequirement
	seen := map[string]struct{}{}
	for _, requirement := range in {
		requirement.Text = strings.TrimSpace(requirement.Text)
		requirement.Reason = strings.TrimSpace(requirement.Reason)
		if requirement.Text != "" {
			key := strings.ToLower(requirement.Text)
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, requirement)
		}
	}
	return out
}

func positiveReferenceCount(refs []IntentReference) int {
	n := 0
	for _, ref := range refs {
		if ref.Influence == InfluencePositive {
			n++
		}
	}
	return n
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func seedSetEmpty(s IntentSeeds) bool { return len(s.Queries) == 0 && len(s.TrackIDs) == 0 }
func orDefaultInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
