package main

import (
	"fmt"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/musicconcepts"
)

// These expectations inspect the intent actually consumed by providers, not
// just the extractor's own evidence log. They intentionally exceed the older
// minimum-word/count fixture assertions.
type meaningExpectation struct {
	Preferences       []preferenceExpectation `json:"preferences,omitempty"`
	Constraints       []constraintExpectation `json:"constraints,omitempty"`
	ForbiddenCriteria []string                `json:"forbiddenCriteria,omitempty"`
	Start             string                  `json:"start,omitempty"`
	DurationSeconds   int                     `json:"durationSeconds,omitempty"`
	NoTemporal        bool                    `json:"noTemporal,omitempty"`
	SoftInstrumental  bool                    `json:"softInstrumental,omitempty"`
	EnergyArc         bool                    `json:"energyArc,omitempty"`
}
type preferenceExpectation struct {
	Kind     string `json:"kind"`
	Value    string `json:"value"`
	Polarity string `json:"polarity"`
	Strength string `json:"strength,omitempty"`
}
type constraintExpectation struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

func checkMeaning(want *meaningExpectation, m core.MusicIntent) []string {
	if want == nil {
		return nil
	}
	var issues []string
	groups := map[string][]core.IntentPreference{"genre": m.Preferences.Genres, "style": m.Preferences.Styles, "mood": m.Preferences.Moods, "instrumentation": m.Preferences.Instrumentation, "texture": m.Preferences.TextureDescriptions}
	groups["vocal"] = m.Preferences.VocalRequests()
	for _, expected := range want.Preferences {
		found := false
		for _, actual := range groups[expected.Kind] {
			if strings.EqualFold(musicconcepts.Canonical(expected.Kind, actual.Value), musicconcepts.Canonical(expected.Kind, expected.Value)) && string(actual.Influence) == expected.Polarity && (expected.Strength == "" || actual.Strength == expected.Strength) {
				found = true
			}
		}
		if !found {
			issues = append(issues, fmt.Sprintf("meaning lost: %s %s %q (%s)", expected.Strength, expected.Polarity, expected.Value, expected.Kind))
		}
	}
	for _, expected := range want.Constraints {
		found := false
		facet := strings.TrimPrefix(expected.Kind, "exclude_")
		for _, actual := range m.HardConstraints {
			found = found || actual.Kind == expected.Kind && strings.EqualFold(musicconcepts.Canonical(facet, actual.Value), musicconcepts.Canonical(facet, expected.Value))
		}
		if !found {
			issues = append(issues, "explicit constraint lost: "+expected.Kind+" "+expected.Value)
		}
	}
	for _, forbidden := range want.ForbiddenCriteria {
		for _, actual := range m.EssentialCriteria {
			if strings.EqualFold(actual.Value, forbidden) {
				issues = append(issues, "invented or misclassified essential criterion: "+forbidden)
			}
		}
	}
	if want.Start != "" && (m.Start == nil || !strings.EqualFold(m.Start.Query, want.Start)) {
		issues = append(issues, "actual starting artist requirement lost")
	}
	if want.DurationSeconds > 0 {
		if m.DurationSeconds != want.DurationSeconds {
			issues = append(issues, "duration unit/value lost")
		}
		if m.Controls.TotalTrackCount == want.DurationSeconds/60 {
			issues = append(issues, "minutes silently treated as track count")
		}
	}
	if want.NoTemporal && len(m.Temporal) > 0 {
		issues = append(issues, "invented temporal restriction")
	}
	if want.SoftInstrumental && core.WantsInstrumental(m) {
		issues = append(issues, "mostly instrumental became a strict vocal exclusion")
	}
	if want.EnergyArc {
		points := m.Journey.EnergyTrajectory
		if len(points) < 3 || points[1].Energy <= points[0].Energy || points[len(points)-1].Energy >= points[1].Energy {
			issues = append(issues, "build and cool-down trajectory lost")
		}
	}
	return issues
}
