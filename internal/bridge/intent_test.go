package bridge

import (
	"context"
	"path/filepath"
	"strings"
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
	if len(res.Request.ReferenceIDs) != 1 || len(res.Request.RequiredIDs) != 0 {
		t.Fatalf("resolved references/required = %#v/%#v", res.Request.ReferenceIDs, res.Request.RequiredIDs)
	}
	// The seed the walk used must be pinned into the returned request.
	if res.Request.Seed != res.Playlist.Seed || res.Request.Seed == 0 {
		t.Fatalf("request seed %d != playlist seed %d", res.Request.Seed, res.Playlist.Seed)
	}
	if res.Playlist.Tracks[0].Kind != "nearest" {
		t.Fatalf("reference seed should guide but not be emitted; first kind = %q", res.Playlist.Tracks[0].Kind)
	}
	// The generated name is a short label (<= 6 words), not the raw prompt.
	if res.Name == "" {
		t.Fatal("GenerateResult.Name is empty")
	}
	if w := len(strings.Fields(res.Name)); w > 6 {
		t.Fatalf("GenerateResult.Name has %d words (%q), want <= 6", w, res.Name)
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

func TestLegacyBuildRequestMigratesSeedsToRequired(t *testing.T) {
	t.Parallel()
	req := (BuildPlaylistRequest{SeedIDs: []string{"legacy"}}).normalized()
	if req.Version != 2 || len(req.ReferenceIDs) != 1 || len(req.RequiredIDs) != 1 {
		t.Fatalf("legacy request not migrated: %+v", req)
	}
	if req.ReferenceIDs[0] != "legacy" || req.RequiredIDs[0] != "legacy" {
		t.Fatalf("legacy seed identity changed: %+v", req)
	}
}
