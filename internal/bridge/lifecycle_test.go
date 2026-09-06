package bridge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/ports"
)

func TestOperationSetSupersedesOlderWork(t *testing.T) {
	t.Parallel()
	var operations operationSet
	firstContext, firstCurrent, firstFinish := operations.begin(context.Background(), "parse")
	defer firstFinish()
	secondContext, secondCurrent, secondFinish := operations.begin(context.Background(), "parse")
	defer secondFinish()
	if !errors.Is(firstContext.Err(), context.Canceled) {
		t.Fatalf("first operation context = %v, want canceled", firstContext.Err())
	}
	if firstCurrent() || !secondCurrent() || secondContext.Err() != nil {
		t.Fatalf("stale/current ordering wrong: first=%v second=%v err=%v", firstCurrent(), secondCurrent(), secondContext.Err())
	}
}

func TestStaleBuildCannotReturnAfterNewerResult(t *testing.T) {
	t.Parallel()
	c := newLoadedContainer(t)
	recommender := &orderedRecommendationEngine{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
	c.Reco = recommender
	api := New(c, nil)
	req := BuildPlaylistRequest{
		Version: core.CurrentIntentVersion,
		Intent: core.MusicIntent{
			Version:    core.CurrentIntentVersion,
			References: []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "seed0001", Influence: core.InfluencePositive}},
			Controls:   core.IntentControls{TotalTrackCount: 1, AudioWeight: .5, CooccurrenceWeight: .5},
			Seed:       "9",
		},
	}
	firstDone := make(chan error, 1)
	go func() {
		_, err := api.BuildPlaylist(context.Background(), req)
		firstDone <- err
	}()
	select {
	case <-recommender.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first build did not start")
	}
	newer, err := api.BuildPlaylist(context.Background(), req)
	if err != nil || len(newer.Tracks) != 1 {
		t.Fatalf("newer build = %+v, %v", newer, err)
	}
	close(recommender.releaseFirst)
	select {
	case err := <-firstDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stale build error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stale build did not finish")
	}
}

type orderedRecommendationEngine struct {
	calls        int
	firstStarted chan struct{}
	releaseFirst chan struct{}
}

func (r *orderedRecommendationEngine) Build(_ context.Context, intent core.MusicIntent) (core.Playlist, error) {
	r.calls++
	if r.calls == 1 {
		close(r.firstStarted)
		<-r.releaseFirst
	}
	intent = intent.Normalized()
	return core.Playlist{
		Mode: intent.Mode, Seed: intent.Seed, Intent: intent,
		Tracks: []core.TrackRef{{ID: "seed0001", Artist: "Justice", Title: "Genesis"}},
	}, nil
}

func TestMatchingPreviewIntentIsReusedForGeneration(t *testing.T) {
	t.Parallel()
	api := New(newLoadedContainer(t), nil)
	ctx := context.Background()
	session := IntentSessionContext{
		SessionID: "session", Locale: "en-US",
		NowPlaying:   &core.TrackRef{ID: "seed0002", Artist: "Björk", Title: "Jóga"},
		RecentTracks: []core.TrackRef{{ID: "seed0001", Artist: "Justice", Title: "Genesis"}},
	}
	preview, err := api.ParseIntentWithContext(ctx, "like Justice, 10 tracks", session)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Cached {
		t.Fatal("first parse unexpectedly cached")
	}
	generated, err := api.GenerateFromPromptWithContext(ctx, "like Justice, 10 tracks", session)
	if err != nil {
		t.Fatal(err)
	}
	if !generated.Status.ParsedIntentReused {
		t.Fatal("generation did not reuse the matching preview intent")
	}
	if generated.Request.Reproducibility.ID == "" || generated.Request.Reproducibility.ID != generated.Playlist.Reproducibility.ID {
		t.Fatalf("initial result is not tied to its request: request=%+v result=%+v", generated.Request.Reproducibility, generated.Playlist.Reproducibility)
	}
	if generated.Request.SessionID != session.SessionID {
		t.Fatalf("session context was not carried into generation: %+v", generated.Request)
	}
}

func TestIntentCacheKeyIncludesPromptParserSchemaAndSession(t *testing.T) {
	t.Parallel()
	api := New(newLoadedContainer(t), nil)
	base := ports.IntentInput{Prompt: "like Justice", Locale: "en-US"}
	baseKey, err := api.intentCacheKey(base)
	if err != nil {
		t.Fatal(err)
	}
	variants := []ports.IntentInput{
		{Prompt: "like Björk", Locale: "en-US"},
		{Prompt: "like Justice", Locale: "fr-FR"},
		{Prompt: "like Justice", Locale: "en-US", SessionID: "session-2"},
		{Prompt: "like Justice", Locale: "en-US", NowPlaying: &core.TrackRef{ID: "seed0001"}},
		{Prompt: "like Justice", Locale: "en-US", RecentTracks: []core.TrackRef{{ID: "seed0002"}}},
	}
	for _, variant := range variants {
		key, err := api.intentCacheKey(variant)
		if err != nil {
			t.Fatal(err)
		}
		if key == baseKey {
			t.Fatalf("cache key did not change for %+v", variant)
		}
	}
	parserKey, err := hashIntentCacheKey(base, api.app.ParserIdentity()+"-changed", schema.Version)
	if err != nil {
		t.Fatal(err)
	}
	schemaKey, err := hashIntentCacheKey(base, api.app.ParserIdentity(), schema.Version+1)
	if err != nil {
		t.Fatal(err)
	}
	if parserKey == baseKey || schemaKey == baseKey {
		t.Fatal("parser or schema version did not invalidate the intent cache key")
	}
}

func TestRankingInputChangesReproducibilityIdentity(t *testing.T) {
	t.Parallel()
	base := core.MusicIntent{
		Version:    core.CurrentIntentVersion,
		References: []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "seed0001", Influence: core.InfluencePositive}},
		Controls:   core.IntentControls{TotalTrackCount: 10, AudioWeight: .5, CooccurrenceWeight: .5},
		Seed:       "18446744073709551615",
	}.Normalized()
	first, err := generationIdentity(base, "catalog-a", "test-reco/v1", "taste-profile/v2", "profile-a")
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := generationIdentity(base, "catalog-a", "test-reco/v1", "taste-profile/v2", "profile-a")
	if err != nil {
		t.Fatal(err)
	}
	if repeated != first || first.AlgorithmVersion == "" || first.ProfileVersion == "" || first.RNGSeed != base.Seed {
		t.Fatalf("generation identity is incomplete or unstable: first=%+v repeated=%+v", first, repeated)
	}
	changed := base
	changed.Controls.AudioWeight = .8
	second, err := generationIdentity(changed, "catalog-a", "test-reco/v1", "taste-profile/v2", "profile-a")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || first.IntentFingerprint == second.IntentFingerprint {
		t.Fatalf("ranking input reused identity: first=%+v second=%+v", first, second)
	}
	otherCatalog, err := generationIdentity(base, "catalog-b", "test-reco/v1", "taste-profile/v2", "profile-a")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == otherCatalog.ID {
		t.Fatal("catalog version did not invalidate generation identity")
	}
	otherProfile, err := generationIdentity(base, "catalog-a", "test-reco/v1", "taste-profile/v2", "profile-b")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == otherProfile.ID {
		t.Fatal("profile snapshot did not invalidate generation identity")
	}
}

func TestPartialGenerationHasStructuredReasons(t *testing.T) {
	t.Parallel()
	c := newLoadedContainer(t)
	artists := map[string]struct{}{}
	for row := 0; row < c.Catalog.Len(); row++ {
		if meta, ok := c.Catalog.Meta(c.Catalog.ID(row)); ok {
			artists[meta.Ref.Artist] = struct{}{}
		}
	}
	constraints := make([]core.HardConstraint, 0, len(artists))
	for artist := range artists {
		constraints = append(constraints, core.HardConstraint{Kind: "exclude_artist", Value: artist, Supported: true})
	}
	api := New(c, nil)
	result, err := api.BuildPlaylist(context.Background(), BuildPlaylistRequest{
		Version: core.CurrentIntentVersion,
		Intent: core.MusicIntent{
			Version:         core.CurrentIntentVersion,
			References:      []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "seed0001", Influence: core.InfluencePositive}},
			HardConstraints: constraints,
			Controls:        core.IntentControls{TotalTrackCount: 10, AudioWeight: .5, CooccurrenceWeight: .5}, Seed: "7",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status.State != "partial" || len(result.Status.PartialReasons) == 0 || result.Status.PartialReasons[0].Code != "eligible_tracks_exhausted" {
		t.Fatalf("partial status = %+v", result.Status)
	}
}
