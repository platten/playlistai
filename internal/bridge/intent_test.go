package bridge

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
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

func TestParseBuildRebuildPreservesIntent(t *testing.T) {
	t.Parallel()
	api := New(newLoadedContainer(t), nil)
	generated, err := api.GenerateFromPrompt("like Justice with microdetail, relaxing but not sleepy, no abstract drone, 10 tracks")
	if err != nil {
		t.Fatal(err)
	}
	original := generated.Request.Intent
	count, audio := 7, 0.9
	rebuilt, err := api.BuildPlaylist(BuildPlaylistRequest{
		Version: core.CurrentIntentVersion,
		Intent:  original,
		Overrides: ControlOverrides{
			TotalTrackCount: &count,
			AudioWeight:     &audio,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := rebuilt.Intent
	if got.Controls.TotalTrackCount != count || got.Controls.AudioWeight != audio {
		t.Fatalf("overrides not applied: %+v", got.Controls)
	}
	if got.Controls.Discovery != original.Controls.Discovery || got.Controls.CooccurrenceWeight != original.Controls.CooccurrenceWeight {
		t.Fatalf("unrelated controls changed: before=%+v after=%+v", original.Controls, got.Controls)
	}
	if !reflect.DeepEqual(got.References, original.References) ||
		!reflect.DeepEqual(got.Preferences, original.Preferences) ||
		!reflect.DeepEqual(got.Unsupported, original.Unsupported) {
		t.Fatalf("interpretation lost during rebuild:\nbefore=%+v\nafter=%+v", original, got)
	}
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
	if len(res.Request.Intent.References) != 1 || len(res.Request.Intent.RequiredTracks) != 0 {
		t.Fatalf("resolved references/required = %#v/%#v", res.Request.Intent.References, res.Request.Intent.RequiredTracks)
	}
	if res.Request.Intent.References[0].TrackID == "" {
		t.Fatal("reference was not resolved to a catalog track")
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
	if req.Version != core.CurrentIntentVersion || len(req.Intent.References) != 1 || len(req.Intent.RequiredTracks) != 1 {
		t.Fatalf("legacy request not migrated: %+v", req)
	}
	if req.Intent.References[0].TrackID != "legacy" || req.Intent.RequiredTracks[0].TrackID != "legacy" {
		t.Fatalf("legacy seed identity changed: %+v", req)
	}
}

func TestVersionThreeRequestKeepsEmbeddedIntent(t *testing.T) {
	t.Parallel()
	req := (BuildPlaylistRequest{
		Version: 3,
		Intent: core.MusicIntent{
			Version:    3,
			References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Björk", Influence: core.InfluencePositive}},
			Controls:   core.IntentControls{TotalTrackCount: 9, AudioWeight: .6, CooccurrenceWeight: .4},
		},
	}).normalized()
	if req.Version != core.CurrentIntentVersion || len(req.Intent.References) != 1 || req.Intent.References[0].Query != "Björk" {
		t.Fatalf("v3 embedded intent was not preserved: %+v", req)
	}
}
