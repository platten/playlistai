package bridge

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/config"
)

func newLoadedContainer(t *testing.T) *app.Container {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.Catalog.Dir = filepath.Join("..", "catalog", "testdata")
	cfg.Enrich.CachePath = filepath.Join(cfg.DataDir, "mb.sqlite")
	c, err := app.New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	if !c.Ready() {
		t.Fatal("container should be Ready with the fixture catalog")
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestParseIntent(t *testing.T) {
	t.Parallel()
	api := New(newLoadedContainer(t), nil)

	p := api.ParseIntent("upbeat instrumental tracks like Justice, 20 songs, adventurous")
	if p.Backend != "rules" {
		t.Fatalf("backend = %q", p.Backend)
	}
	if len(p.Seeds) != 1 || p.Seeds[0] != "Justice" {
		t.Fatalf("seeds = %#v", p.Seeds)
	}
	if p.Count != 20 {
		t.Fatalf("count = %d", p.Count)
	}
	if p.Creativity <= 0.5 {
		t.Fatalf("'adventurous' should lift creativity, got %v", p.Creativity)
	}
	if p.Notes == "" {
		t.Fatal("notes empty")
	}
	// zero-value slices must serialize as [] not null
	if p.ArtistsExclude == nil {
		t.Fatal("ArtistsExclude should be non-nil")
	}
}

func TestGenerateFromPrompt(t *testing.T) {
	t.Parallel()
	api := New(newLoadedContainer(t), nil)

	res, err := api.GenerateFromPrompt("like Justice, 10 tracks")
	if err != nil {
		t.Fatalf("GenerateFromPrompt: %v", err)
	}
	if len(res.Playlist.Tracks) != 10 {
		t.Fatalf("playlist length = %d, want 10", len(res.Playlist.Tracks))
	}
	if len(res.Request.SeedIDs) != 1 {
		t.Fatalf("resolved seedIDs = %#v", res.Request.SeedIDs)
	}
	// The seed the walk used must be pinned into the returned request.
	if res.Request.Seed != res.Playlist.Seed || res.Request.Seed == 0 {
		t.Fatalf("request seed %d != playlist seed %d", res.Request.Seed, res.Playlist.Seed)
	}
	if res.Playlist.Tracks[0].Kind != "seed" {
		t.Fatalf("first track kind = %q", res.Playlist.Tracks[0].Kind)
	}

	// A prompt with no findable seed is an error, not a panic.
	if _, err := api.GenerateFromPrompt("zqxjkw nothing here"); err == nil {
		t.Fatal("expected an error for an unresolvable prompt")
	}
}

func TestGenerateFromPromptNeedsCatalog(t *testing.T) {
	t.Parallel()
	api := New(newTestContainer(t), nil) // no catalog
	if _, err := api.GenerateFromPrompt("like Justice"); err == nil {
		t.Fatal("expected an error when the catalog is not loaded")
	}
}
