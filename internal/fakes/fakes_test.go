package fakes

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func sampleCatalog() *Catalog {
	return NewCatalog(2,
		CatalogTrack{ID: "a", Display: "Justice - Genesis", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		CatalogTrack{ID: "b", Display: "Justice - Stress", Audio: []float32{0.9, 0.1}, Track: []float32{0.8, 0.2}},
		CatalogTrack{ID: "c", Display: "Boards of Canada - Roygbiv", Audio: []float32{0, 1}, Track: []float32{0, 1}},
	)
}

func TestFakeCatalog(t *testing.T) {
	t.Parallel()
	c := sampleCatalog()

	if c.Len() != 3 || c.Dim() != 2 {
		t.Fatalf("len/dim: %d/%d", c.Len(), c.Dim())
	}
	if got := c.ID(1); got != "b" {
		t.Fatalf("ID(1)=%q", got)
	}
	if r, ok := c.RowOf("c"); !ok || r != 2 {
		t.Fatalf("RowOf(c)=%d,%v", r, ok)
	}
	m, ok := c.Meta("a")
	if !ok || m.Ref.Artist != "Justice" || m.Ref.Title != "Genesis" {
		t.Fatalf("meta: %+v ok=%v", m, ok)
	}
	if hits := c.Resolve("justice", 10); len(hits) != 2 {
		t.Fatalf("resolve justice: %d hits", len(hits))
	}
	if v, ok := c.VectorsByRow(0); !ok || v.Audio[0] != 1 {
		t.Fatalf("vectors by row: %+v ok=%v", v, ok)
	}

	var _ ports.Catalog = c
}

func TestFakeSimilarityEngineRanksByBlend(t *testing.T) {
	t.Parallel()
	c := sampleCatalog()
	s := NewSimilarityEngine(c)

	// Query aligned with track "a"; exclude "a" so "b" should win over "c".
	got, err := s.Search(context.Background(), ports.SimilarityQuery{
		AudioSum: []float32{1, 0},
		TrackSum: []float32{1, 0},
		Weights:  [2]float32{0.5, 0.5},
		K:        2,
		Exclude:  map[string]struct{}{"a": {}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 matches, got %d", len(got))
	}
	if got[0].ID != "b" {
		t.Fatalf("want b first, got %q (%+v)", got[0].ID, got)
	}
	if got[0].Score < got[1].Score {
		t.Fatalf("not sorted desc: %+v", got)
	}
}

func TestFakeRecommendationEngineResolvesSeeds(t *testing.T) {
	t.Parallel()
	r := &RecommendationEngine{Catalog: sampleCatalog()}

	pl, err := r.Build(context.Background(), core.MusicIntent{
		Version: core.CurrentIntentVersion,
		Seeds:   core.IntentSeeds{Queries: []string{"justice"}},
		Count:   5,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(pl.Tracks) != 1 {
		t.Fatalf("want the non-reference justice track, got %d", len(pl.Tracks))
	}

	if _, err := r.Build(context.Background(), core.MusicIntent{}); err != core.ErrNoSeeds {
		t.Fatalf("want ErrNoSeeds, got %v", err)
	}
}

func TestFakeEnricherReportsProgress(t *testing.T) {
	t.Parallel()
	rec := &RecordingProgress{}
	e := &Enricher{}

	refs := []core.TrackRef{{ID: "a", Title: "x"}, {ID: "b", Title: "y"}}
	out, err := e.Enrich(context.Background(), refs, rec)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if len(out) != 2 || !out[0].Matched || out[0].ISRC == "" {
		t.Fatalf("bad enrichment: %+v", out)
	}
	rows := rec.Snapshot()
	if len(rows) != 2 || rows[1].Done != 2 || rows[1].Total != 2 {
		t.Fatalf("progress rows: %+v", rows)
	}
}

func TestFakePreviewProviderFallsBackToBundled(t *testing.T) {
	t.Parallel()
	pp := &PreviewProvider{Miss: true}
	url, ok, err := pp.PreviewURL(context.Background(), core.TrackRef{ID: "a"}, "https://cdn/x.mp3")
	if err != nil || !ok || url != "https://cdn/x.mp3" {
		t.Fatalf("fallback: %q ok=%v err=%v", url, ok, err)
	}
	_, ok, _ = pp.PreviewURL(context.Background(), core.TrackRef{ID: "a"}, "")
	if ok {
		t.Fatal("no bundled url => miss")
	}
}
