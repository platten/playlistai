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

func TestDuplicateJourneyConstraintUsesTypedWaypoints(t *testing.T) {
	const prompt = "10 songs from Artist A through Artist B to Artist C"
	for _, kind := range []string{"journey", "journey_order", "waypoint_order", "transition_order"} {
		for _, value := range []string{"Artist A -> Artist B -> Artist C", "Artist A -> Artist C -> Artist B", "Artist A -> Artist B -> Artist C -> Artist D"} {
			w := Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 10,
				HardConstraints: []WireConstraint{{Kind: kind, Value: value, Span: prompt}}}
			for _, name := range []string{"Artist A", "Artist B", "Artist C"} {
				w.JourneyWaypoints = append(w.JourneyWaypoints, WireReference{Kind: "artist", Value: name, Span: name, Explicit: true, Influence: "positive"})
			}
			raw, _ := json.Marshal(w)
			m, err := ParseForPrompt(raw, prompt)
			if err != nil || m.Mode != core.ModeJourney || len(m.Journey.Waypoints) != 3 {
				t.Fatalf("lost typed journey: %+v %v", m, err)
			}
			if (len(m.HardConstraints) == 0) != (value == "Artist A -> Artist B -> Artist C") {
				t.Fatalf("removed an unrepresented/reordered requirement: %+v", m.HardConstraints)
			}
		}
	}
}

func TestArtistStageDescriptionIsNotCopiedToUnmentionedStages(t *testing.T) {
	const prompt = "10 tracks from Brian Eno's ambient sound through Boards of Canada to Jon Hopkins"
	w := Wire{Genres: []WirePreference{}, Mode: "journey", TotalCount: 10}
	for _, name := range []string{"Brian Eno", "Boards of Canada", "Jon Hopkins"} {
		w.JourneyWaypoints = append(w.JourneyWaypoints, WireReference{Kind: "artist", Value: name, Span: name, Influence: "positive", Explicit: true})
	}
	for _, scope := range []string{"journey_start", "journey_via", "journey_end"} {
		w.EssentialCriteria = append(w.EssentialCriteria, WireCriterion{Kind: "style", Value: "ambient", Span: "ambient", Scope: scope})
	}
	raw, _ := json.Marshal(w)
	m, err := ParseForPrompt(raw, prompt)
	if err != nil || len(m.EssentialCriteria) != 1 || m.EssentialCriteria[0].Scope != "journey_start" {
		t.Fatalf("opening description spread through the journey: %+v %v", m.EssentialCriteria, err)
	}
	preserveArtistStageDescriptions(&w, prompt+", remaining ambient throughout")
	if w.EssentialCriteria[2].Scope != "journey_end" {
		t.Fatal("ambiguous/repeated source wording was rewritten")
	}
}

func TestNamedDestinationRecoveryDoesNotPromoteSimilarityReferences(t *testing.T) {
	for _, tc := range []struct {
		prompt  string
		journey bool
		want    bool
	}{
		{"10 songs from Artist A to Artist B", true, true},
		{"İ: 10 songs from Artist A to Artist B", true, true},
		{"10 songs ending with Artist B", false, true},
		{"10 songs similar to Artist B", false, false},
		{"10 songs inspired by Artist A and Artist B", true, false},
	} {
		w := Wire{Mode: "similar", References: []WireReference{{Kind: "artist", Value: "Artist B", Span: "Artist B", Explicit: true, Influence: "positive"}}}
		if tc.journey {
			w.Mode = "journey"
			w.JourneyWaypoints = []WireReference{{Kind: "artist", Value: "Artist A", Span: "Artist A", Explicit: true, Influence: "positive"}, w.References[0]}
		}
		preserveNamedDestination(&w, tc.prompt)
		if (len(w.Destination) == 1) != tc.want {
			t.Fatalf("%q: destination=%+v", tc.prompt, w.Destination)
		}
	}
}
