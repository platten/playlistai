package schema

import (
	"encoding/json"
	"testing"
)

func TestArtistOnlyRequiresAttachedOutputRestriction(t *testing.T) {
	for _, prompt := range []string{
		"15 songs like Nine Inch Nails",
		"Take me from Nine Inch Nails to Marilyn Manson over 15 tracks.",
		"15 songs, including Nine Inch Nails, and other artists.",
		"15 songs not only by Nine Inch Nails.",
	} {
		w := Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 15, HardConstraints: []WireConstraint{{Kind: "require_artist", Value: "Nine Inch Nails", Span: "Nine Inch Nails"}}}
		raw, _ := json.Marshal(w)
		m, err := ParseForPrompt(raw, prompt)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range m.HardConstraints {
			if c.Kind == "require_artist" {
				t.Fatalf("%q invented artist-only restriction: %+v", prompt, c)
			}
		}
	}
}

func TestArtistOnlyShieldsGenreLikeArtistNames(t *testing.T) {
	for _, prompt := range []string{"12 songs only by Electronic.", "12 songs by Electronic only."} {
		raw, _ := json.Marshal(Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 12})
		m, err := ParseForPrompt(raw, prompt)
		if err != nil || len(m.References) != 1 || m.References[0].Query != "Electronic" || len(m.Preferences.Genres) != 0 || len(m.EssentialCriteria) != 0 {
			t.Fatalf("artist identity became genre for %q: %+v %v", prompt, m, err)
		}
	}
}
