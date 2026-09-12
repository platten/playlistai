package multichannel

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

func TestCachedAudioRetrievalFindsMoodOutsideSeedNeighbors(t *testing.T) {
	for _, seedless := range []bool{false, true} {
		cat, service, retriever := recommendationPoolFixture(t, 4, 2)
		service.Policy = audio.Policy{}
		retriever.candidates = retriever.candidates[:2] // strongest two are outside seed retrieval
		intent := testIntent(1)
		intent.VerificationPolicy = core.BestAvailable
		intent.Preferences.Moods = []core.IntentPreference{{Value: "relaxing", Influence: core.InfluencePositive}}
		intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_artist", Value: "p002"}}
		if seedless {
			intent.References = nil
			intent.Seeds = core.IntentSeeds{}
		}
		engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
		engine.retriever = retriever
		got, err := engine.Build(context.Background(), intent)
		if err != nil || len(got.Tracks) != 1 || got.Tracks[0].ID != "p003" {
			t.Fatalf("seedless=%v cached recall or exclusion failed: %v %v", seedless, got.IDs(), err)
		}
		if service.Resolver.(*noPreviewFetch).calls != 0 {
			t.Fatal("cached retrieval fetched audio")
		}
		found := false
		for _, explanation := range got.Rationale {
			for _, source := range explanation.Sources {
				found = found || source.Channel == "cached_audio"
			}
		}
		if !found {
			t.Fatal("cached retrieval provenance missing")
		}
	}
}
