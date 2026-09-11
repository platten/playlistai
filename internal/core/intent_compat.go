package core

import "math"

// Historical requests retain explicit migration/default rules. The legacy
// engine fields are derived adapters, never a second source of user intent.
func migrateStoredIntent(m MusicIntent) MusicIntent {
	out := m
	inputVersion := out.Version
	if out.VerificationPolicy == "" {
		out.VerificationPolicy = VerifiedOnly
		if inputVersion >= 8 {
			out.VerificationPolicy = BestAvailable
		}
	}
	// Only v1/v2 need semantic migration. V3 already has the typed intent;
	// v4 adds resolution metadata and v5 makes RNG seeds lossless strings.
	if out.Version < 3 || legacyAdapterOnly(out) {
		out = migrateLegacy(out)
	}
	// V3-v5 represented model-inferred starting points as ordinary references.
	// Move only references whose stored evidence explicitly says they were
	// inferred. References without evidence retain their historical explicit
	// seed behavior.
	if inputVersion > 0 && inputVersion < 6 {
		out.References, out.InferredAnchors = migrateInferredReferences(out.References, out.InferredAnchors)
	}
	out.Version = CurrentIntentVersion
	return out
}

func legacyAdapterOnly(m MusicIntent) bool {
	typed := len(m.References) > 0 || len(m.InferredAnchors) > 0 || len(m.RequiredTracks) > 0 ||
		len(m.EssentialCriteria) > 0 || len(m.Journey.Waypoints) > 0 || len(m.HardConstraints) > 0 ||
		len(m.Preferences.Genres) > 0 || len(m.Preferences.Styles) > 0 || len(m.Preferences.Moods) > 0 ||
		len(m.Preferences.Instrumentation) > 0 || m.Preferences.VocalPreference != nil ||
		len(m.Preferences.TextureDescriptions) > 0
	if typed {
		return false
	}
	return !seedSetEmpty(m.Seeds) || !seedSetEmpty(m.Required)
}

func migrateLegacy(m MusicIntent) MusicIntent {
	if m.Version < 2 && seedSetEmpty(m.Required) {
		m.Required = m.Seeds
	}
	for _, q := range m.Seeds.Queries {
		m.References = append(m.References, legacyReference(ReferenceArtist, q, ""))
	}
	for _, id := range m.Seeds.TrackIDs {
		m.References = append(m.References, legacyReference(ReferenceTrack, "", id))
	}
	for _, q := range m.Required.Queries {
		m.RequiredTracks = append(m.RequiredTracks, legacyReference(ReferenceTrack, q, ""))
	}
	for _, id := range m.Required.TrackIDs {
		m.RequiredTracks = append(m.RequiredTracks, legacyReference(ReferenceTrack, "", id))
	}
	for _, artist := range m.Constraints.ArtistsExclude {
		m.HardConstraints = append(m.HardConstraints, HardConstraint{Kind: "exclude_artist", Value: artist, Supported: true})
	}
	if m.Constraints.ExcludeSeedArtists {
		m.HardConstraints = append(m.HardConstraints, HardConstraint{Kind: "exclude_reference_artists", Value: "true", Supported: true})
	}
	if m.Constraints.NoRepeatArtistBackToBack {
		m.HardConstraints = append(m.HardConstraints, HardConstraint{Kind: "no_back_to_back_artist", Value: "true", Supported: true})
	}
	m.Controls = IntentControls{
		RecommendationMode:   m.Controls.RecommendationMode,
		TotalTrackCount:      m.Count,
		AudioWeight:          m.Creativity,
		CooccurrenceWeight:   1 - m.Creativity,
		Discovery:            m.Noise,
		TransitionSmoothness: float64(clampInt(orDefaultInt(m.Lookback, DefaultLookback), MinLookback, MaxLookback)-1) / 9,
	}
	return m
}

func legacyReference(kind ReferenceKind, query, id string) IntentReference {
	return IntentReference{Kind: kind, Query: query, TrackID: id, Influence: InfluencePositive}
}

func (m *MusicIntent) backfillEngineAdapter() {
	m.Seeds = IntentSeeds{}
	allReferences := append(append([]IntentReference(nil), m.References...), m.Journey.Waypoints...)
	for _, anchor := range m.InferredAnchors {
		// Unassessed and unsuitable inferred anchors must not silently steer the
		// compatibility engine. Only affirmative evidence makes one a seed.
		if anchor.Suitability.State == EvidenceMatch {
			allReferences = append(allReferences, anchor.Reference)
		}
	}
	for _, ref := range allReferences {
		if ref.Influence != InfluencePositive {
			continue
		}
		if ref.TrackID != "" {
			m.Seeds.TrackIDs = appendUnique(m.Seeds.TrackIDs, ref.TrackID)
		} else {
			m.Seeds.Queries = appendUnique(m.Seeds.Queries, ref.Query)
		}
		if ref.Resolution != nil && ref.Resolution.Selected != nil {
			for _, representative := range ref.Resolution.Selected.Representatives {
				m.Seeds.TrackIDs = appendUnique(m.Seeds.TrackIDs, representative.TrackID)
			}
		}
	}
	m.Required = IntentSeeds{}
	for _, ref := range m.RequiredTracks {
		if ref.TrackID != "" {
			m.Required.TrackIDs = appendUnique(m.Required.TrackIDs, ref.TrackID)
		} else {
			m.Required.Queries = appendUnique(m.Required.Queries, ref.Query)
		}
	}
	m.Count = m.Controls.TotalTrackCount
	weightTotal := m.Controls.AudioWeight + m.Controls.CooccurrenceWeight
	m.Creativity = 0.5
	if weightTotal > 0 {
		m.Creativity = m.Controls.AudioWeight / weightTotal
	}
	m.Noise = m.Controls.Discovery
	m.Lookback = 1 + int(math.Round(m.Controls.TransitionSmoothness*9))
	m.Constraints = IntentConstraints{}
	for _, constraint := range m.HardConstraints {
		if !constraint.Supported {
			continue
		}
		switch constraint.Kind {
		case "exclude_artist":
			m.Constraints.ArtistsExclude = appendUnique(m.Constraints.ArtistsExclude, constraint.Value)
		case "exclude_reference_artists":
			m.Constraints.ExcludeSeedArtists = true
		case "no_back_to_back_artist":
			m.Constraints.NoRepeatArtistBackToBack = true
		}
	}
}

func migrateInferredReferences(references []IntentReference, anchors []InferredAnchor) ([]IntentReference, []InferredAnchor) {
	explicit := make([]IntentReference, 0, len(references))
	for _, reference := range references {
		if evidenceIsInferred(reference.Evidence) {
			anchors = append(anchors, InferredAnchor{
				Reference: reference, Role: "legacy_retrieval", Reason: "migrated model-inferred reference",
				Suitability: AnchorSuitability{State: EvidenceUnknown, Detail: "musical suitability was not recorded by the older contract"},
			})
			continue
		}
		explicit = append(explicit, reference)
	}
	return explicit, anchors
}

func evidenceIsInferred(evidence []SourceEvidence) bool {
	if len(evidence) == 0 {
		return false
	}
	for _, item := range evidence {
		if item.Explicit {
			return false
		}
	}
	return true
}
