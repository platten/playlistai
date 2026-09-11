package core

import (
	"math"
	"testing"
)

func TestIntentRejectsInvalidDomainContracts(t *testing.T) {
	ref := IntentReference{Kind: ReferenceTrack, Query: "song", Influence: InfluencePositive}
	cases := map[string]func(*MusicIntent){
		"policy": func(m *MusicIntent) { m.VerificationPolicy = "invalid" },
		"temporal basis": func(m *MusicIntent) {
			m.Temporal = []TemporalRequirement{{Basis: "unknown", StartYear: 1900, EndYear: 2000}}
		},
		"temporal scope": func(m *MusicIntent) {
			m.Temporal = []TemporalRequirement{{Basis: "composition", StartYear: 1900, EndYear: 2000, Scope: "invalid"}}
		},
		"destination":     func(m *MusicIntent) { m.Destination = &IntentReference{} },
		"attempt count":   func(m *MusicIntent) { m.AnchorAttempts = make([]InferredAnchor, 7) },
		"expansion empty": func(m *MusicIntent) { m.GenreExpansions = []GenreExpansion{{}} },
		"expansion lost criterion": func(m *MusicIntent) {
			m.GenreExpansions = []GenreExpansion{{Genre: "jazz", Characteristics: "improvisation"}}
		},
		"seed":                func(m *MusicIntent) { m.Seed = "invalid" },
		"anchor count":        func(m *MusicIntent) { m.InferredAnchors = make([]InferredAnchor, 4) },
		"reference kind":      func(m *MusicIntent) { r := ref; r.Kind = "unknown"; m.References = []IntentReference{r} },
		"reference influence": func(m *MusicIntent) { r := ref; r.Influence = "unknown"; m.References = []IntentReference{r} },
		"reference empty":     func(m *MusicIntent) { r := ref; r.Query = " "; m.References = []IntentReference{r} },
		"reference resolution": func(m *MusicIntent) {
			r := ref
			r.Resolution = &ReferenceResolution{Status: ResolutionResolved}
			m.References = []IntentReference{r}
		},
		"criterion kind":  func(m *MusicIntent) { m.EssentialCriteria = []MusicalCriterion{{Kind: "unknown", Value: "x"}} },
		"criterion empty": func(m *MusicIntent) { m.EssentialCriteria = []MusicalCriterion{{Kind: "style"}} },
		"criterion scope": func(m *MusicIntent) {
			m.EssentialCriteria = []MusicalCriterion{{Kind: "style", Value: "jazz", Scope: "unknown"}}
		},
		"negative anchor": func(m *MusicIntent) {
			r := ref
			r.Influence = InfluenceNegative
			m.InferredAnchors = []InferredAnchor{{Reference: r}}
		},
		"anchor score": func(m *MusicIntent) {
			m.InferredAnchors = []InferredAnchor{{Reference: ref, Suitability: AnchorSuitability{Score: math.NaN()}}}
		},
		"anchor state": func(m *MusicIntent) {
			m.InferredAnchors = []InferredAnchor{{Reference: ref, Suitability: AnchorSuitability{State: "invalid"}}}
		},
		"required artist":  func(m *MusicIntent) { r := ref; r.Kind = ReferenceArtist; m.RequiredTracks = []IntentReference{r} },
		"preference":       func(m *MusicIntent) { m.Preferences.Moods = []IntentPreference{{Value: " "}} },
		"vocal":            func(m *MusicIntent) { m.Preferences.VocalPreference = &IntentPreference{} },
		"constraint empty": func(m *MusicIntent) { m.HardConstraints = []HardConstraint{{}} },
		"constraint claim": func(m *MusicIntent) {
			m.HardConstraints = []HardConstraint{{Kind: "unknown", Value: "x", Supported: true}}
		},
		"mode policy":  func(m *MusicIntent) { m.Controls.RecommendationMode = "invalid" },
		"weight":       func(m *MusicIntent) { m.Controls.AudioWeight = math.Inf(1) },
		"count":        func(m *MusicIntent) { m.Controls.TotalTrackCount = MaxCount + 1 },
		"energy range": func(m *MusicIntent) { m.Journey.EnergyTrajectory = []EnergyPoint{{Position: 2}} },
		"energy order": func(m *MusicIntent) { m.Journey.EnergyTrajectory = []EnergyPoint{{Position: 1}, {Position: 0}} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := MusicIntent{}
			mutate(&m)
			if m.Validate() == nil {
				t.Fatal("invalid contract accepted")
			}
		})
	}
	m := MusicIntent{Temporal: []TemporalRequirement{{Basis: "original_release", StartYear: 1900, EndYear: 2000}}, References: []IntentReference{ref}, InferredAnchors: []InferredAnchor{{Reference: ref}}, GenreExpansions: []GenreExpansion{{Genre: "Jazz", Characteristics: "improvisation"}}, EssentialCriteria: []MusicalCriterion{{Kind: "style", Value: "jazz"}}, Preferences: SemanticPreferences{Moods: []IntentPreference{{Value: "calm", Influence: InfluencePositive}}}, HardConstraints: []HardConstraint{{Kind: "exclude_artist", Value: "artist", Supported: true}}, Journey: JourneyPlan{EnergyTrajectory: []EnergyPoint{{Position: 0}, {Position: 1, Energy: 1}}}}
	if err := m.Validate(); err != nil {
		t.Fatalf("valid rich intent: %v", err)
	}
}

func TestResolutionContracts(t *testing.T) {
	candidate := ResolutionCandidate{Kind: ReferenceArtist, EntityID: "artist", Confidence: .9, Representatives: []WeightedTrack{{TrackID: "a", Weight: .4}, {TrackID: "b", Weight: .6}}}
	valid := ReferenceResolution{Status: ResolutionResolved, Selected: &candidate}
	if err := validateResolution(ReferenceArtist, valid); err != nil {
		t.Fatal(err)
	}
	for name, res := range map[string]ReferenceResolution{
		"status": {Status: "invalid"}, "selection missing": {Status: ResolutionResolved},
		"kind":                    {Status: ResolutionResolved, Selected: &ResolutionCandidate{Kind: ReferenceTrack}},
		"alternative kind":        {Status: ResolutionAmbiguous, Alternatives: []ResolutionCandidate{{Kind: ReferenceTrack}}},
		"confidence":              {Status: ResolutionAmbiguous, Alternatives: []ResolutionCandidate{{Kind: ReferenceArtist, Confidence: 2}}},
		"representative identity": {Status: ResolutionAmbiguous, Alternatives: []ResolutionCandidate{{Kind: ReferenceArtist, Representatives: []WeightedTrack{{Weight: 1}}}}},
		"weight total":            {Status: ResolutionAmbiguous, Alternatives: []ResolutionCandidate{{Kind: ReferenceArtist, Representatives: []WeightedTrack{{TrackID: "a", Weight: .4}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			if validateResolution(ReferenceArtist, res) == nil {
				t.Fatal("invalid resolution accepted")
			}
		})
	}
	if err := validateResolution(ReferenceArtist, ReferenceResolution{Status: ResolutionUnresolved}); err != nil {
		t.Fatal(err)
	}
	valid.CatalogVersion = " v1 "
	cleaned := cleanResolution(&valid)
	cleaned.Selected.Representatives[0].Weight = .1
	if cleaned.CatalogVersion != "v1" || candidate.Representatives[0].Weight != .4 {
		t.Fatal("clean resolution altered original")
	}
}
