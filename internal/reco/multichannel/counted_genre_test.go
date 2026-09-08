package multichannel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/enrich/musicbrainz"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/similarity/brute"
)

// Synthetic musical annotations verify mechanics, not real listening quality.
func TestCountedOpenGenreBuildAndReplay(t *testing.T) {
	for _, genre := range []string{"Classical", "Gqom", "未知ジャンル"} {
		for _, backend := range []string{"rules", "model"} {
			t.Run(genre+"/"+backend, func(t *testing.T) {
				prompt := genre + " 10 tracks"
				intent, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
				if backend == "model" {
					wire := schema.Wire{Genres: []schema.WirePreference{{Value: genre, Span: genre, Explicit: true, Influence: "positive"}}, Mode: "similar", TotalCount: 20}
					raw, _ := json.Marshal(wire)
					intent, err = schema.ParseForPrompt(raw, prompt)
				} else {
					intent = rules.ApplyConfirmedGenre(intent, genre)
				}
				if err != nil {
					t.Fatal(err)
				}
				intent.Seed = "42"
				intent.VerificationPolicy = core.BestAvailable
				intent.Knowledge = &core.KnowledgeSnapshot{ID: "synthetic-genres"}
				var entries []fakes.CatalogTrack
				for i := range 10 {
					ref := core.TrackRef{ID: fmt.Sprint(i), Artist: fmt.Sprintf("Fixture %d", i), Title: "Recording"}
					entries = append(entries, fakes.CatalogTrack{ID: ref.ID, Display: ref.Artist + " - " + ref.Title, Audio: []float32{1, 0}, Track: []float32{1, 0}})
					intent.Knowledge.Candidates = append(intent.Knowledge.Candidates, ref)
					intent.Knowledge.Tracks = append(intent.Knowledge.Tracks, core.EnrichedTrack{Ref: ref, Matched: true, IdentityStatus: core.ResolutionResolved, GenreTags: []core.AttributedGenreTag{{Name: genre, Votes: 3, Source: "fixture"}}})
				}
				cat := fakes.NewCatalog(2, entries...)
				engine := New(cat, brute.New(cat), cat, DefaultConfig())
				playlist, err := engine.Build(context.Background(), intent)
				if err != nil || len(playlist.Tracks) != 10 || playlist.Intent.OriginalDescription != prompt {
					t.Fatalf("counted genre failed: %+v, %v", playlist, err)
				}
				again, err := engine.Build(context.Background(), playlist.Intent)
				if err != nil || trackIDs(again.Tracks) != trackIDs(playlist.Tracks) {
					t.Fatal("replay changed", err)
				}
			})
		}
	}
}

func TestCountedGenreLiveCatalog(t *testing.T) {
	dir := os.Getenv("PLAYLISTAI_TEST_COUNTED_GENRE_CATALOG")
	if dir == "" {
		t.Skip("set PLAYLISTAI_TEST_COUNTED_GENRE_CATALOG for online smoke validation")
	}
	cat, err := catalog.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	client, err := musicbrainz.New(musicbrainz.Config{UserAgent: "PlaylistAI/0.7.0 (https://github.com/platten/playlistai)", CachePath: filepath.Join(t.TempDir(), "metadata.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	intent, _ := rules.New().Parse(ctx, ports.IntentInput{Prompt: "Classical 10 tracks"})
	intent.Seed, intent.VerificationPolicy = "42", core.BestAvailable
	intent, err = client.ResolveMusic(ctx, intent, cat, cat, nil)
	if err != nil {
		t.Fatal(err)
	}
	playlist, err := New(cat, brute.New(cat), cat, DefaultConfig()).Build(ctx, intent)
	t.Logf("candidates=%d tracks=%d outcome=%+v notices=%v", len(intent.Knowledge.Candidates), len(playlist.Tracks), playlist.Outcome, intent.Knowledge.Notices)
	if err != nil || len(playlist.Tracks) == 0 || len(playlist.Tracks) > 10 || playlist.Intent.Controls.TotalTrackCount != 10 {
		t.Fatalf("counted genre live smoke failed: %v", err)
	}
}
