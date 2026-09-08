package multichannel

import (
	"context"
	"encoding/json"
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

func TestSampleGenreJourneyGeneratesInStageOrder(t *testing.T) {
	cat := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "a", Display: "Fixture A - End One", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "b", Display: "Fixture B - End Two", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "y", Display: "Fixture Y - Start One", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "z", Display: "Fixture Z - Start Two", Audio: []float32{1, 0}, Track: []float32{1, 0}},
	)
	const prompt = "A journey from ambient to energetic electronic"
	for _, backend := range []string{"rules", "model"} {
		t.Run(backend, func(t *testing.T) {
			intent, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
			if backend == "model" {
				w := schema.Wire{Mode: "journey", TotalCount: 4, Genres: []schema.WirePreference{{Value: "ambient", Span: "ambient", Explicit: true, Influence: "positive"}, {Value: "electronic", Span: "electronic", Explicit: true, Influence: "positive"}}}
				raw, _ := json.Marshal(w)
				intent, err = schema.ParseForPrompt(raw, prompt)
			}
			if err != nil {
				t.Fatal(err)
			}
			intent.Controls.TotalTrackCount = 4
			intent.Seed = "42"
			intent.VerificationPolicy = core.BestAvailable
			intent.Knowledge = &core.KnowledgeSnapshot{ID: "reviewed-fixture"}
			for _, id := range []string{"a", "b", "y", "z"} {
				meta, _ := cat.Meta(id)
				genre := "electronic"
				if id == "y" || id == "z" {
					genre = "ambient"
				}
				intent.Knowledge.Candidates = append(intent.Knowledge.Candidates, meta.Ref)
				intent.Knowledge.Tracks = append(intent.Knowledge.Tracks, core.EnrichedTrack{Ref: meta.Ref, Matched: true, IdentityStatus: core.ResolutionResolved, GenreTags: []core.AttributedGenreTag{{Name: genre, Votes: 2, Source: "fixture"}}})
			}
			engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
			playlist, err := engine.Build(context.Background(), intent)
			if err != nil || len(playlist.Tracks) != 4 {
				t.Fatalf("playlist=%+v err=%v", playlist, err)
			}
			for i, track := range playlist.Tracks {
				if (track.ID == "y" || track.ID == "z") != (i < 2) {
					t.Fatalf("genre direction lost: %+v", playlist.Tracks)
				}
			}
			if len(playlist.Intent.Journey.EnergyTrajectory) != 2 || !noticeCode(playlist.Notices, "energy_trajectory_unsupported") {
				t.Fatal("unmeasured energy was lost or claimed verified")
			}
			again, err := engine.Build(context.Background(), playlist.Intent)
			if err != nil || trackIDs(again.Tracks) != trackIDs(playlist.Tracks) {
				t.Fatal("journey replay changed")
			}
			intent.Controls.TotalTrackCount = 1
			short, err := engine.Build(context.Background(), intent)
			if err != nil || short.Outcome.State != core.OutcomeNeedsClarification {
				t.Fatalf("single-track journey: %+v %v", short, err)
			}
		})
	}
}

func TestGenreJourneyLiveCatalog(t *testing.T) {
	dir := os.Getenv("PLAYLISTAI_TEST_JOURNEY_CATALOG")
	if dir == "" {
		t.Skip("set PLAYLISTAI_TEST_JOURNEY_CATALOG for online smoke validation")
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
	intent, err := rules.New().Parse(ctx, ports.IntentInput{Prompt: "A journey from ambient to energetic electronic"})
	if err != nil {
		t.Fatal(err)
	}
	intent.VerificationPolicy = core.BestAvailable
	intent.Controls.TotalTrackCount = 6
	intent.Seed = "42"
	intent, err = client.ResolveMusic(ctx, intent, cat, cat, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("lookup: %d candidates, %v", len(intent.Knowledge.Candidates), intent.Knowledge.Notices)
	playlist, err := New(cat, brute.New(cat), cat, DefaultConfig()).Build(ctx, intent)
	if err != nil || len(playlist.Tracks) < 2 {
		t.Fatalf("playlist=%+v err=%v", playlist, err)
	}
	o := &Orchestrator{knowledge: intent.Knowledge}
	criteria := core.JourneyCriteria(intent.EssentialCriteria)
	if o.bestCriterion(ctx, playlist.Tracks[0].ID, criteria[0]) != core.EvidenceMatch || o.bestCriterion(ctx, playlist.Tracks[len(playlist.Tracks)-1].ID, criteria[1]) != core.EvidenceMatch {
		t.Fatalf("endpoints lack genre evidence: %+v", playlist.Tracks)
	}
	t.Logf("tracks=%+v outcome=%+v", playlist.Tracks, playlist.Outcome)
}
