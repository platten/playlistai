package schema

import (
	"encoding/json"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestDescriptiveRequestCannotLoseDefiningElectronicCategory(t *testing.T) {
	const prompt = "Give me 10 tracks of spacious electronic music with detailed textures, a deep groove, and occasional sparkle—relaxing but not sleepy."
	w := Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 10,
		Moods: []WirePreference{{Value: "relaxing", Span: "relaxing", Influence: "positive", Explicit: true}, {Value: "not sleepy", Span: "not sleepy", Influence: "negative", Explicit: true}}}
	raw, _ := json.Marshal(w)
	m, err := ParseForPrompt(raw, prompt)
	if err != nil || len(m.Preferences.Genres) != 1 || m.Preferences.Genres[0].Value != "electronic" || len(m.EssentialCriteria) != 1 || m.EssentialCriteria[0].Value != "electronic" {
		t.Fatalf("defining category lost: %+v %v", m, err)
	}
	negative := m.Preferences.Moods[1]
	if negative.Value != "sleepy" || negative.Influence != core.InfluenceNegative || negative.Evidence[0].Text != "not sleepy" {
		t.Fatalf("negation direction or source wording lost: %+v", negative)
	}
	w.Styles = []WirePreference{{Value: "spacious electronic", Span: "spacious electronic", Influence: "positive", Explicit: true}}
	raw, _ = json.Marshal(w)
	m, err = ParseForPrompt(raw, prompt)
	if err != nil || len(m.Preferences.Genres) != 1 || len(m.EssentialCriteria) != 1 || m.EssentialCriteria[0].Value != "electronic" {
		t.Fatalf("a soft style was mistaken for the essential genre: %+v %v", m, err)
	}
}

func TestCategoryRepairPreservesArtistNamesCompoundsAndSoftInfluence(t *testing.T) {
	for _, prompt := range []string{"10 tracks like the artist Electronic", "10 tracks of unrecognized-post-rock", "10 songs with some rock influence"} {
		w := Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 10}
		if prompt == "10 tracks like the artist Electronic" {
			w.References = []WireReference{{Kind: "artist", Value: "Electronic", Span: "Electronic", Influence: "positive", Explicit: true}}
		}
		preserveDefiningCategory(&w, prompt)
		if len(w.Genres) != 0 || len(w.EssentialCriteria) != 0 {
			t.Fatalf("fallback invented a category for %q: %+v", prompt, w)
		}
	}
	wCompound := Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 10}
	preserveDefiningCategory(&wCompound, "10 tracks of post-rock")
	if len(wCompound.Genres) != 1 || wCompound.Genres[0].Value != "post-rock" {
		t.Fatalf("reviewed compound broadened to its parent: %+v", wCompound)
	}
	w := Wire{Genres: []WirePreference{{Value: "未知ジャンル", Span: "未知ジャンル", Influence: "positive", Explicit: true}}}
	preserveDefiningCategory(&w, "未知ジャンル, 10 songs")
	if len(w.Genres) != 1 || w.Genres[0].Value != "未知ジャンル" {
		t.Fatal("open-vocabulary category changed")
	}
}

func TestNegativeDescriptionRepairDoesNotStripGenreNames(t *testing.T) {
	w := Wire{Genres: []WirePreference{{Value: "no wave", Span: "no no wave", Influence: "negative", Explicit: true}}}
	normalizeNegativeDescriptions(&w)
	if w.Genres[0].Value != "no wave" {
		t.Fatal("negative genre identity changed")
	}
}

func TestScopedCategoryJourneyCannotRunAsSimilarMode(t *testing.T) {
	const prompt = "Give me a 10-song playlist that starts with acoustic folk, moves through folk rock, and ends with energetic alternative rock."
	w := Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 10,
		EssentialCriteria: []WireCriterion{
			{Kind: "genre", Value: "acoustic folk", Scope: "journey_start", Span: "acoustic folk"},
			{Kind: "genre", Value: "folk rock", Scope: "journey_via", Span: "folk rock"},
			{Kind: "genre", Value: "alternative rock", Scope: "journey_end", Span: "alternative rock"},
		}}
	raw, _ := json.Marshal(w)
	m, err := ParseForPrompt(raw, prompt)
	if err != nil || m.Mode != core.ModeJourney || len(core.JourneyCriteria(m.EssentialCriteria)) != 3 {
		t.Fatalf("stages flattened: %+v %v", m, err)
	}
}

func TestArtistJourneyDoesNotAcquireModelGuessedGenres(t *testing.T) {
	w := Wire{Genres: []WirePreference{}, Mode: "journey", TotalCount: 10,
		References: []WireReference{
			{Kind: "artist", Value: "Bonobo", Span: "Bonobo", Explicit: true, Influence: "positive"},
			{Kind: "artist", Value: "Massive Attack", Span: "Massive Attack", Explicit: true, Influence: "positive"},
		},
		EssentialCriteria: []WireCriterion{
			{Kind: "genre", Value: "downtempo", Span: "Bonobo", Scope: "journey_start"},
			{Kind: "genre", Value: "trip-hop", Span: "Massive Attack", Scope: "journey_end"},
		},
	}
	w.JourneyWaypoints = w.References
	raw, _ := json.Marshal(w)
	m, err := ParseForPrompt(raw, "Give me 10 songs that move from Bonobo to Massive Attack, including related discoveries.")
	if err != nil || len(m.EssentialCriteria) != 0 || len(m.References) != 2 || m.Destination == nil || m.Destination.Query != "Massive Attack" {
		t.Fatalf("artist identities became required musical characteristics: %+v %v", m, err)
	}
	w.EssentialCriteria = []WireCriterion{{Kind: "style", Value: "ambient", Span: "ambient sound", Scope: "journey_start"}}
	raw, _ = json.Marshal(w)
	m, err = ParseForPrompt(raw, "10 songs from Bonobo's ambient sound to Massive Attack")
	if err != nil || len(m.EssentialCriteria) == 0 || m.EssentialCriteria[0].Value != "ambient" {
		t.Fatal("actual descriptive wording was discarded", err)
	}
}
