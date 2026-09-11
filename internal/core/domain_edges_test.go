package core

import (
	"reflect"
	"testing"
	"time"
)

func TestFeedbackValidationContracts(t *testing.T) {
	valid := FeedbackEvent{Version: FeedbackEventVersion, ID: "event", OccurredAt: time.Unix(1, 0), TrackID: "track", Type: FeedbackLike, Scope: FeedbackScopeDurable}
	for _, kind := range []FeedbackType{FeedbackLike, FeedbackDislike, FeedbackMoreLike, FeedbackLessLike, FeedbackAccepted, FeedbackRemoved, FeedbackExposure} {
		event := valid
		event.Type = kind
		event.Scope = FeedbackScopeRequest
		event.SessionID = "session"
		if err := event.Validate(); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	cases := map[string]func(*FeedbackEvent){
		"version": func(e *FeedbackEvent) { e.Version++ }, "id": func(e *FeedbackEvent) { e.ID = " " }, "time": func(e *FeedbackEvent) { e.OccurredAt = time.Time{} },
		"track": func(e *FeedbackEvent) { e.TrackID = " " }, "type": func(e *FeedbackEvent) { e.Type = "other" }, "scope": func(e *FeedbackEvent) { e.Scope = "other" },
		"durable exposure": func(e *FeedbackEvent) { e.Type = FeedbackExposure }, "unidentified request": func(e *FeedbackEvent) { e.Scope = FeedbackScopeRequest },
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := valid
			mutate(&e)
			if e.Validate() == nil {
				t.Fatal("accepted invalid feedback")
			}
		})
	}
}

func TestQualifiedReferencesAndTrackIdentity(t *testing.T) {
	for _, input := range []string{"Song by Artist", "Artist - Song", "Artist — Song", "Artist – Song", "Artist's Song", "Artist’s Song"} {
		artist, title, ok := QualifiedReferenceParts(" " + input + " ")
		if !ok || artist != "Artist" || title != "Song" {
			t.Fatalf("%q: %q %q %v", input, artist, title, ok)
		}
	}
	for _, input := range []string{"Song", "Artist - ", " by Artist"} {
		if _, _, ok := QualifiedReferenceParts(input); ok {
			t.Fatalf("qualified incomplete input %q", input)
		}
	}
	track := TrackRef{ID: "id", Title: "Song"}
	if track.Display() != "Song" || track.SpotifyURL() != "https://open.spotify.com/track/id" {
		t.Fatal("incorrect display or URL")
	}
	if ProvisionalRecordingKey(TrackRef{Artist: " ARTIST ", Title: "Some  Song"}) != "artist\x00some song" {
		t.Fatal("identity was not normalized")
	}
	ids := (Playlist{Tracks: []TrackRef{{ID: "b"}, {ID: "a"}, {ID: "b"}}}).IDs()
	if !reflect.DeepEqual(ids, []string{"b", "a", "b"}) {
		t.Fatalf("IDs reordered: %v", ids)
	}
	for _, mode := range []RecommendationMode{"", AcousticBrainzFirst, CLAPFirst, DeejAIOnly} {
		if !mode.Valid() {
			t.Fatalf("rejected %q", mode)
		}
	}
	if RecommendationMode("unknown").Valid() {
		t.Fatal("accepted unknown mode")
	}
}

func TestSeedInvalidInputsDoNotMutate(t *testing.T) {
	if !RNGSeed("").IsZero() || !ZeroRNGSeed.IsZero() || RNGSeed("1").IsZero() {
		t.Fatal("incorrect zero seed")
	}
	if _, err := RNGSeed("invalid").Int64(); err == nil {
		t.Fatal("invalid signed seed accepted")
	}
	if _, err := RNGSeed("invalid").MarshalText(); err == nil {
		t.Fatal("invalid seed marshaled")
	}
	var nilSeed *RNGSeed
	if nilSeed.UnmarshalJSON([]byte(`"1"`)) == nil {
		t.Fatal("nil receiver accepted")
	}
	for _, raw := range []string{`"unterminated`, `true`, `18446744073709551616`} {
		seed := RNGSeed("7")
		if seed.UnmarshalJSON([]byte(raw)) == nil || seed != "7" {
			t.Fatalf("invalid %q altered seed", raw)
		}
	}
	for _, raw := range []string{"", " null "} {
		seed := RNGSeed("7")
		if err := seed.UnmarshalJSON([]byte(raw)); err != nil || seed != ZeroRNGSeed {
			t.Fatalf("empty seed %q: %v", raw, err)
		}
	}
	if seed, err := RNGSeed(" ").Canonical(); err != nil || seed != ZeroRNGSeed {
		t.Fatal("blank seed not canonical")
	}
}
