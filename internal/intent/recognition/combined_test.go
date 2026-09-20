package recognition

import (
	"context"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/mbindex"
)

type packNames struct{}

func (packNames) RecognitionProvider() string { return "paipack" }
func (packNames) SnapshotIdentity() mbindex.SnapshotIdentity {
	return mbindex.SnapshotIdentity{IndexVersion: "pack/v1", Snapshot: "generation"}
}
func (packNames) LookupArtistNames(_ context.Context, names []string) ([]mbindex.ArtistNameLookup, error) {
	out := make([]mbindex.ArtistNameLookup, len(names))
	for i, name := range names {
		out[i] = mbindex.ArtistNameLookup{Name: name, NameKey: core.NormalizeIdentityPart(name), Candidates: []mbindex.ArtistIdentity{{MBID: "paipack-artist:" + core.NormalizeIdentityPart(name), Name: name}}}
	}
	return out, nil
}
func (packNames) LookupArtistRecordings(context.Context, []mbindex.ArtistRecordingQuery) ([]mbindex.ArtistRecordingLookup, error) {
	return nil, nil
}

func TestCombinedRecognitionCorroboratesRatherThanInventsAmbiguity(t *testing.T) {
	store := recognitionStore(t)
	defer store.Close()
	lookup := Combine(store, packNames{})
	results, err := lookup.LookupArtistNames(context.Background(), []string{"Low", "Pack Exclusive"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results[0].Candidates) != 1 || strings.HasPrefix(results[0].Candidates[0].MBID, "paipack-artist:") {
		t.Fatalf("same artist became ambiguous: %+v", results[0])
	}
	if len(results[1].Candidates) != 1 || !strings.HasPrefix(results[1].Candidates[0].MBID, "paipack-artist:") {
		t.Fatalf("pack-only artist lost: %+v", results[1])
	}
	if !strings.Contains(lookup.SnapshotIdentity().Snapshot, "generation") {
		t.Fatal("pack generation missing")
	}
}
