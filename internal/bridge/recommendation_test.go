package bridge

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/deejai"
)

type forbiddenRecommendationService struct{}

func (forbiddenRecommendationService) Build(context.Context, core.MusicIntent) (core.Playlist, error) {
	panic("engine-only called enhanced recommender")
}
func (forbiddenRecommendationService) ResolveMusic(context.Context, core.MusicIntent, ports.Catalog, ports.ReferenceResolver, ports.Progress) (core.MusicIntent, error) {
	panic("engine-only called metadata enrichment")
}
func (forbiddenRecommendationService) GenreNames(context.Context) (core.GenreGraph, error) {
	panic("engine-only called genre provider during parsing")
}
func (forbiddenRecommendationService) IsCachedGenre(context.Context, string) bool {
	panic("engine-only called genre cache during parsing")
}

func TestEngineOnlySettingsGenerateAndHistoryReplay(t *testing.T) {
	c := newLoadedContainer(t)
	c.Knowledge = forbiddenRecommendationService{}
	a := New(c, nil)
	useRecommendationEngine(a, forbiddenRecommendationService{})
	if err := a.SetRecommendationMode(core.DeejAIOnly); err != nil {
		t.Fatal(err)
	}
	generated, err := a.GenerateFromPromptWithContext(context.Background(), "like Justice, 10 tracks", IntentSessionContext{GenerationID: "only-test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(generated.Playlist.Tracks) == 0 || generated.Playlist.Intent.Controls.RecommendationMode != core.DeejAIOnly || generated.Playlist.AudioEvidence != nil || generated.Playlist.Reproducibility.AlgorithmVersion != deejai.OnlyAlgorithmVersion || generated.Playlist.Reproducibility.ProfileSnapshot != "" {
		t.Fatalf("wrong engine output: %+v", generated.Playlist)
	}
	// Settings change new requests, not the mode pinned into an existing one.
	if err := a.SetRecommendationMode(core.CLAPFirst); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(generated.Request)
	if err != nil {
		t.Fatal(err)
	}
	var saved BuildPlaylistRequest
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	replayed, err := a.BuildPlaylist(context.Background(), saved)
	if err != nil || !reflect.DeepEqual(replayed.Tracks, generated.Playlist.Tracks) || replayed.Reproducibility.ID != generated.Playlist.Reproducibility.ID {
		t.Fatalf("saved mode lost: %+v %v", replayed, err)
	}
	list, err := a.ListSavedPlaylists()
	if err != nil || len(list) == 0 {
		t.Fatalf("history missing: %v", err)
	}
	loaded, err := a.LoadSavedPlaylist(list[0].ID)
	if err != nil || loaded.Request.Intent.Controls.RecommendationMode != core.DeejAIOnly {
		t.Fatalf("stored mode lost: %+v %v", loaded, err)
	}
}

func TestSettingsModeIsPinnedAfterParsingAndSeparatesCaches(t *testing.T) {
	a := New(newLoadedContainer(t), nil)
	first, err := a.GenerateFromPrompt(context.Background(), "like Justice, 10 tracks")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetRecommendationMode(core.CLAPFirst); err != nil {
		t.Fatal(err)
	}
	second, err := a.GenerateFromPrompt(context.Background(), "like Justice, 10 tracks")
	if err != nil || second.Request.Intent.Controls.RecommendationMode != core.CLAPFirst || !second.Status.ParsedIntentReused {
		t.Fatalf("cached parse ignored current settings: %+v %v", second, err)
	}
	// Same seed/intent apart from the policy must invalidate generation identity.
	changed := first.Request.Intent
	changed.Controls.RecommendationMode = core.CLAPFirst
	x, _ := generationIdentity(first.Request.Intent, "catalog", "algorithm", "", "")
	y, _ := generationIdentity(changed, "catalog", "algorithm", "", "")
	if x.ID == y.ID || x.IntentFingerprint == y.IntentFingerprint {
		t.Fatal("mode not included in generation identity")
	}
	input := ports.IntentInput{Prompt: "obscure category"}
	xkey, _ := a.intentCacheKey(input)
	input.SkipMetadata = true
	ykey, _ := a.intentCacheKey(input)
	if xkey == ykey {
		t.Fatal("metadata-enriched parse reused for engine-only")
	}
}

func TestEngineOnlyDisablesSubmittedGenreLookups(t *testing.T) {
	c := newLoadedContainer(t)
	c.Knowledge = forbiddenRecommendationService{}
	a := New(c, nil)
	if err := a.SetRecommendationMode(core.DeejAIOnly); err != nil {
		t.Fatal(err)
	}
	_, err := a.ParseIntentWithContext(context.Background(), "unmappedgenre music", IntentSessionContext{GenerationID: "no-network"})
	if err != nil {
		t.Fatal(err)
	}
}
