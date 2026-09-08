package schema

import (
	"encoding/json"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestModelCategoryJourneyRepairsEntityDestinationAndStageScopes(t *testing.T) {
	for _, tc := range []struct{ prompt, start, end string }{
		{"A journey from ambient to energetic electronic", "ambient", "electronic"},
		{"A journey from vaporwave to energetic gabber", "vaporwave", "gabber"},
	} {
		t.Run(tc.prompt, func(t *testing.T) {
			w := Wire{Genres: []WirePreference{{Value: tc.start, Influence: "positive", Explicit: true, Span: tc.start}, {Value: "energetic " + tc.end, Influence: "positive", Explicit: true, Span: "energetic " + tc.end}}, Mode: "similar", TotalCount: 6,
				References:        []WireReference{{Kind: "artist", Value: tc.start, Span: tc.start, Explicit: true, Influence: "positive"}},
				Destination:       []WireReference{{Kind: "artist", Value: "energetic " + tc.end, Span: "energetic " + tc.end, Explicit: true, Influence: "positive"}},
				EssentialCriteria: []WireCriterion{{Kind: "genre", Value: tc.start, Span: tc.start, Scope: "playlist"}, {Kind: "genre", Value: tc.end, Span: tc.end, Scope: "playlist"}, {Kind: "mood", Value: "energetic", Span: "energetic", Scope: "journey_end"}},
			}
			raw, _ := json.Marshal(w)
			intent, err := ParseForPrompt(raw, tc.prompt)
			if err != nil {
				t.Fatal(err)
			}
			stages := core.JourneyCriteria(intent.EssentialCriteria)
			if intent.Mode != core.ModeJourney || intent.Destination != nil || len(intent.References) != 0 || len(stages) != 2 || stages[0].Scope != "journey_start" || stages[1].Scope != "journey_end" || stages[1].Value != tc.end || len(intent.Journey.EnergyTrajectory) != 2 {
				t.Fatalf("lost category journey: %+v", intent)
			}
		})
	}
}
