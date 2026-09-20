package localcatalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
)

// Opt-in structural smoke check, not a musical judgment or a text-model test.
// Only aggregate coverage is logged; the source pack is never changed.
func TestUserPackReadOnlyRecommendationEvidence(t *testing.T) {
	path := os.Getenv("PLAYLISTAI_TEST_PAIPACK")
	if path == "" {
		t.Skip("set PLAYLISTAI_TEST_PAIPACK to opt in with a private pack")
	}
	ctx := context.Background()
	manager, err := librarypack.OpenManager(ctx, filepath.Join(t.TempDir(), "isolated-import"), librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	staged, err := manager.Stage(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate(ctx, staged); err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Open(lease, Options{SourceID: "private-smoke"})
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	executor, err := NewExecutor(catalog, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, criterion := range []core.MusicalCriterion{{Kind: "instrumentation", Value: "piano"}, {Kind: "instrumentation", Value: "cello"}, {Kind: "genre", Value: "ambient"}, {Kind: "genre", Value: "dance"}} {
		hits, err := catalog.Search(ctx, MetadataQuery{Text: criterion.Value, Criterion: &criterion, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range hits {
			if catalog.CriterionEvidence(ctx, hit.Track.ID, criterion) != core.EvidenceMatch {
				t.Fatal("typed search and evidence disagree")
			}
		}
		t.Logf("%s/%s bounded metadata matches: %d", criterion.Kind, criterion.Value, len(hits))
	}
	manifest := catalog.Manifest()
	if manifest.Coverage.CLAP > 0 {
		query := make([]float32, manifest.CLAP.Dimension)
		query[0] = 1
		result, err := executor.Query(ctx, Query{CLAP: &NeighborQuery{Vector: query, Space: &manifest.CLAP, Limit: 3}})
		if err != nil || len(result.Candidates) == 0 {
			t.Fatalf("stored CLAP query unavailable: %v", err)
		}
	}
	t.Logf("validated pack v%d: tracks=%d MERT=%d CLAP=%d DSP=%d", manifest.Version, manifest.Coverage.Tracks, manifest.Coverage.MERT, manifest.Coverage.CLAP, manifest.Coverage.DSP)
}
