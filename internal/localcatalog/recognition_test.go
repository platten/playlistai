package localcatalog

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/recognition"
	"github.com/platten/playlistai/internal/librarylearn"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/mbindex"
)

func TestPackRecognitionProtectsNamesAndTitles(t *testing.T) {
	c, manager := openTestCatalog(t, []librarypack.Track{{ID: "a", Artist: "Ambient Piano", Title: "Chello"}}, nil)
	defer manager.Close()
	defer c.Close()
	prompt := "include Ambient Piano — Chello"
	source := recognition.Apply(context.Background(), prompt, lexicon.Extract(prompt), c, nil)
	found := false
	for _, atom := range source.Atoms {
		if atom.Kind == "genre" || atom.Kind == "instrumentation" {
			t.Fatalf("identity became musical constraint: %+v", atom)
		}
		if atom.Grounding != nil {
			if atom.Grounding.Provider != "paipack" || len(atom.Grounding.Candidates) != 1 || atom.Grounding.Candidates[0].ID != c.NamespacedID("a") {
				t.Fatalf("incorrect pack identity: %+v", atom)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("pack title was not grounded")
	}
}

func TestPackAlbumAndVocabularyRecognition(t *testing.T) {
	c, manager := openTestCatalog(t, []librarypack.Track{{ID: "album", Artist: "Seed Artist", Title: "Track", Album: "Piano Ambient"}}, nil)
	defer manager.Close()
	defer c.Close()
	c.metadata = &librarylearn.MetadataModel{Vocabulary: []string{"unheardwave", "Piano Ambient"}}
	prompt := "like the album Piano Ambient by Seed Artist, with unheardwave"
	source := recognition.Apply(context.Background(), prompt, lexicon.Extract(prompt), c, nil)
	album, genre := false, false
	for _, atom := range source.Atoms {
		if atom.Grounding != nil && atom.Grounding.MatchType == "artist_scoped_album" {
			album = atom.Kind == "album" && atom.Grounding.Candidates[0].Title == "Piano Ambient"
		}
		if atom.Kind == "genre" {
			if atom.Value != "unheardwave" {
				t.Fatalf("album leaked genre: %+v", atom)
			}
			genre = atom.ConceptID == ""
		}
	}
	if !album || !genre {
		t.Fatalf("album or literal unreviewed category lost: %+v", source)
	}
}

func TestPackRecognitionBoundariesAndCancellation(t *testing.T) {
	c, manager := openTestCatalog(t, testTracks(), nil)
	defer manager.Close()
	defer c.Close()
	if _, err := c.LookupArtistNames(context.Background(), make([]string, mbindex.MaxLookupKeys+1)); err == nil {
		t.Fatal("unbounded batch accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.LookupArtistNames(ctx, []string{"Seed Artist"}); err == nil {
		t.Fatal("cancellation ignored")
	}
	results, err := c.LookupArtistNames(context.Background(), []string{"Seed Artist", "Seed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results[0].Candidates) != 1 || len(results[1].Candidates) != 0 {
		t.Fatalf("nonexact lookup: %+v", results)
	}
	if c.SnapshotIdentity().Snapshot == "" {
		t.Fatal("missing generation identity")
	}
}
