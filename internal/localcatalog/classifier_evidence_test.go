package localcatalog

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
)

func TestFlattenedClassifierGenresCannotValidateElectronicPlaylist(t *testing.T) {
	// Minimal public musical metadata from the first electronic audit. The
	// archived classifiers disagree with the ordinary recording genre tags.
	tracks := []librarypack.Track{
		{ID: "hollow", Artist: "A Perfect Circle", Title: "The Hollow", RawTags: json.RawMessage(`{"AB:GENRE":"Electronic;Trance;Rock;Jazz","GENRE":"Other"}`)},
		{ID: "combination", Artist: "Aerosmith", Title: "Combination", RawTags: json.RawMessage(`{"AB:GENRE":"Electronic;Ambient;Rock;Jazz","GENRE":"Hard Rock;Other"}`)},
		{ID: "run", Artist: "Bee Gees", Title: "Run to Me", RawTags: json.RawMessage(`{"AB:GENRE":"Electronic;Ambient;Rhythm and Blues;Jazz","GENRE":"Pop"}`)},
		{ID: "electronic", Artist: "Fixture", Title: "Electronic recording", RawTags: json.RawMessage(`{"GENRE":"Electronic"}`)},
	}
	c, manager := openTestCatalog(t, tracks, nil)
	defer manager.Close()
	defer c.Close()
	criterion := core.MusicalCriterion{Kind: "genre", Value: "electronic"}
	for _, id := range []string{"hollow", "combination", "run"} {
		if got := c.CriterionEvidence(context.Background(), c.NamespacedID(id), criterion); got != core.EvidenceUnknown {
			t.Errorf("%s falsely establishes electronic genre: %s", id, got)
		}
	}
	hits, err := c.Search(context.Background(), MetadataQuery{Text: "electronic", Criterion: &criterion, Limit: 10})
	if err != nil || len(hits) != 1 || hits[0].Track.ID != c.NamespacedID("electronic") {
		t.Fatalf("classifier leakage through typed retrieval: %+v %v", hits, err)
	}
}
