package multichannel

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/similarity/brute"
)

// Set PLAYLISTAI_TEST_CATALOG to repeat this check against an installed catalog.
// Synthetic neighbors exercise retrieval, not assertions of real musical fit.
func TestClassicalArtistPromptBuildsPlaylist(t *testing.T) {
	fake := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "reference", Display: "Arvo Pärt - Reference", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "one", Display: "Fixture A - One", Audio: []float32{.99, .01}, Track: []float32{.99, .01}},
		fakes.CatalogTrack{ID: "two", Display: "Fixture B - Two", Audio: []float32{.98, .02}, Track: []float32{.98, .02}},
		fakes.CatalogTrack{ID: "three", Display: "Fixture C - Three", Audio: []float32{.97, .03}, Track: []float32{.97, .03}},
	)
	var cat ports.Catalog = fake
	var resolver ports.ReferenceResolver = fake
	count := 3
	if dir := os.Getenv("PLAYLISTAI_TEST_CATALOG"); dir != "" {
		real, err := catalog.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer real.Close()
		cat, resolver = real, real
		count = core.DefaultCount
	}
	const prompt = "classical music like Arvo Part"
	for _, backend := range []string{"model", "rules"} {
		t.Run(backend, func(t *testing.T) {
			var intent core.MusicIntent
			var err error
			if backend == "rules" {
				intent, err = rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
			} else {
				wire := schema.Wire{Genres: []schema.WirePreference{{Value: "classical", Explicit: true, Span: "classical", Influence: "positive"}}, Mode: "similar", TotalCount: 3,
					References: []schema.WireReference{{Kind: "artist", Value: "Arvo Pärt", Explicit: true, Span: "Arvo Part", Influence: "positive"}}}
				raw, _ := json.Marshal(wire)
				intent, err = schema.ParseForPrompt(raw, prompt)
			}
			if err != nil {
				t.Fatal(err)
			}
			intent.VerificationPolicy = core.BestAvailable
			intent.Controls.TotalTrackCount = count
			intent.Seed = "42"
			playlist, err := New(cat, brute.New(cat), resolver, DefaultConfig()).Build(context.Background(), intent)
			if err != nil || len(playlist.Tracks) != count {
				t.Fatalf("prompt should generate: tracks=%d outcome=%+v err=%v", len(playlist.Tracks), playlist.Outcome, err)
			}
			if len(playlist.Intent.References) != 1 || playlist.Intent.References[0].Resolution == nil || playlist.Intent.References[0].Resolution.Selected == nil || playlist.Intent.References[0].Resolution.Selected.Artist != "Arvo Pärt" || playlist.Intent.OriginalDescription != prompt || len(playlist.Intent.EssentialCriteria) != 1 || playlist.Intent.EssentialCriteria[0].Value != "classical" {
				t.Fatalf("explicit reference or original description lost: %+v", playlist.Intent)
			}
			if playlist.Outcome.State != core.OutcomePartial {
				t.Fatalf("catalog-only musical fit must remain approximate: %+v", playlist.Outcome)
			}
			t.Logf("%s: %d tracks, state=%s, artist=%s", backend, len(playlist.Tracks), playlist.Outcome.State, playlist.Intent.References[0].Resolution.Selected.Artist)
		})
	}
}
