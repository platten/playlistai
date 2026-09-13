package multichannel

import (
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestAcousticThesaurusNegationAndUnsupportedSenses(t *testing.T) {
	track := acousticFixture("a", .95)
	for _, tt := range []struct {
		word     string
		negative bool
		state    string
	}{
		{"soothing", false, "supporting"}, {"soothing", true, "opposing"},
		{"dreamlike", false, "unknown"}, {"tranquil", false, "unknown"},
	} {
		got := acousticComparisons(track, []core.AudioClause{{Kind: "mood", Text: tt.word, Negative: tt.negative}})[0]
		if got.AcousticState != tt.state || got.Clause.Text != tt.word {
			t.Fatalf("%+v: %+v", tt, got)
		}
	}
	if acousticClass("mood", "electronic", "mood_electronic") != "" {
		t.Fatal("unreviewed sense inferred from classifier name")
	}
	if acousticClass("genre", "soul", "genre_dortmund") != "" {
		t.Fatal("combined class proves narrower genre")
	}
	if acousticClass("genre", "rnb", "genre_rosamerica") != "rhy" {
		t.Fatal("exact R&B class missing")
	}
}
