package schema

import (
	"encoding/json"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestUnfamiliarGenreExpansionCannotReplaceRequirement(t *testing.T) {
	w := Wire{Mode: "similar", TotalCount: 5, EssentialCriteria: []WireCriterion{{Kind: "style", Value: "新しい音楽ジャンル", Scope: "playlist", Span: "新しい音楽ジャンル"}}, GenreExpansions: []core.GenreExpansion{{Genre: "新しい音楽ジャンル", Characteristics: "sparse percussion and shimmering textures", RelatedGenres: []string{"ambient", "electronic"}}}}
	raw, _ := json.Marshal(w)
	intent, err := ParseForPrompt(raw, "新しい音楽ジャンル")
	if err != nil || intent.OriginalDescription != "新しい音楽ジャンル" || intent.EssentialCriteria[0].Value != "新しい音楽ジャンル" {
		t.Fatalf("genre lost: %+v %v", intent, err)
	}
	w.EssentialCriteria = nil
	raw, _ = json.Marshal(w)
	if _, err := Parse(raw); err == nil {
		t.Fatal("related genre replaced original essential requirement")
	}
	w.EssentialCriteria = []WireCriterion{{Kind: "style", Value: "新しい音楽ジャンル", Scope: "playlist"}}
	w.GenreExpansions[0].RelatedGenres = []string{"a", "b", "c", "d"}
	raw, _ = json.Marshal(w)
	if _, err := Parse(raw); err == nil {
		t.Fatal("unbounded related genres")
	}
}
