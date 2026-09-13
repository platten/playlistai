package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/enrich/musicbrainz"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/multichannel"
)

func TestGenerateGenreJourneyUsesStageLookupsAndReplays(t *testing.T) {
	c := newLoadedContainer(t)
	first, _ := c.Runtime().Catalog.Meta(c.Runtime().Catalog.ID(0))
	var last core.TrackRef
	for row := 1; row < c.Runtime().Catalog.Len(); row++ {
		meta, _ := c.Runtime().Catalog.Meta(c.Runtime().Catalog.ID(row))
		if meta.Ref.Artist != first.Ref.Artist {
			last = meta.Ref
			break
		}
	}
	if last.ID == "" {
		t.Fatal("fixture needs two artists")
	}
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Query().Get("query"))
		switch r.URL.Path {
		case "/ws/2/artist":
			if r.URL.Query().Get("limit") != "100" {
				t.Error("artist discovery lost its broad pool")
			}
			_, _ = w.Write([]byte(`{"count":0,"artists":[]}`))
		case "/ws/2/recording":
			genre, ref := "ambient", first.Ref
			if r.URL.Query().Get("query") == `tag:"electronic"` {
				genre, ref = "electronic", last
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"count": 1, "recordings": []map[string]any{{"id": ref.ID, "title": ref.Title, "artist-credit": []map[string]any{{"name": ref.Artist}}, "tags": []map[string]any{{"name": genre, "count": 2}}}}})
		case "/genres":
			_, _ = w.Write([]byte(`<html></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client, err := musicbrainz.New(musicbrainz.Config{UserAgent: "fixture", MirrorURL: srv.URL, Interval: time.Nanosecond, CachePath: filepath.Join(t.TempDir(), "metadata.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	// Preserve the metadata-only engine as a documented replay baseline. The
	// iterative desktop path requires preview analysis for musical clauses and
	// has separate synthetic CLAP/stream acceptance tests.
	if err := c.SetRecommendationMode(core.AcousticBrainzFirst); err != nil {
		t.Fatal(err)
	}
	c.Knowledge = struct{ ports.MusicKnowledge }{client}
	api := New(c, nil)
	useRecommendationEngine(api, multichannel.New(c.Runtime().Catalog, c.Runtime().Sim, c.Runtime().Resolver, multichannel.DefaultConfig()))
	const prompt = "A journey from ambient to energetic electronic"
	preview, err := api.ParseIntent(context.Background(), prompt)
	if err != nil || len(preview.ResolutionIssues) != 0 || len(preview.Seeds) != 0 || len(requests) != 0 {
		t.Fatalf("genre parsed as artist or typing went online: %+v %v", preview, err)
	}
	generated, err := api.GenerateFromPromptWithContext(context.Background(), prompt, IntentSessionContext{GenerationID: "genre-journey"})
	if err != nil {
		t.Fatal(err)
	}
	tracks := generated.Playlist.Tracks
	if len(tracks) != 2 || tracks[0].ID != first.Ref.ID || tracks[1].ID != last.ID || generated.Playlist.GenerationID != "genre-journey" {
		t.Fatalf("wrong journey: %+v", generated.Playlist)
	}
	if len(requests) < 4 || requests[0] != `tag:"ambient"` || requests[1] != `tag:"electronic"` {
		t.Fatalf("stages starved by enrichment or adjective queried as genre: %v", requests)
	}
	count := len(requests)
	replay, err := api.BuildPlaylist(context.Background(), generated.Request)
	if err != nil || len(replay.Tracks) != 2 || replay.Tracks[0].ID != tracks[0].ID || len(requests) != count {
		t.Fatal("history replay lost stages or searched again")
	}
}
