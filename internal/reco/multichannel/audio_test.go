package multichannel

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type audioFixtureEncoder struct{}

func (*audioFixtureEncoder) Identity() core.AudioModelIdentity {
	return core.AudioModelIdentity{Model: "synthetic", Revision: "1", Runtime: "fixture", Preprocessing: audio.PreprocessingVersion, Dimension: 2}
}
func (*audioFixtureEncoder) EmbedAudio(context.Context, []float32) ([]float32, error) {
	return nil, fmt.Errorf("fixture must reuse stored features")
}
func (*audioFixtureEncoder) EmbedText(_ context.Context, text string) ([]float32, error) {
	if text == "rock" || text == "sleepy" {
		return []float32{0, 1}, nil
	}
	return []float32{1, 0}, nil
}

type noPreviewFetch struct{ calls int }

func (r *noPreviewFetch) ResolveAudioPreview(context.Context, core.TrackRef, core.EnrichedTrack) (core.ResolvedAudioPreview, error) {
	r.calls++
	return core.ResolvedAudioPreview{}, nil
}

func cachedAudioService(t *testing.T, cat *fakes.Catalog) (*audio.Service, *noPreviewFetch) {
	t.Helper()
	store, err := audio.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	encoder := &audioFixtureEncoder{}
	resolver := &noPreviewFetch{}
	for _, id := range []string{"seed", "audio", "audio-copy", "cooc", "taste", "blocked", "other", "far", "last"} {
		meta, _ := cat.Meta(id)
		vector := []float32{0, 1}
		if id == "audio" || id == "seed" || id == "last" {
			vector = []float32{1, 0}
		}
		record := core.AudioAnalysis{TrackID: id, CatalogVersion: cat.CatalogVersion(), TrackKey: core.ProvisionalRecordingKey(meta.Ref), Model: encoder.Identity(), Identity: core.PreviewIdentity{Provider: "deezer", ProviderID: id, Status: core.ResolutionResolved}, AudioSHA256: strings.Repeat("0", 64), Segments: []core.AudioSegment{{StartSeconds: 0, EndSeconds: 10, Embedding: vector}}}
		record.ID = audio.Fingerprint(record)
		if err := store.Put(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	return &audio.Service{Resolver: resolver, Analyzer: encoder, Store: store, Authorized: true, ParityValidated: true, Policy: audio.Policy{Version: "synthetic/v1", DevelopmentSet: "control-flow-only", MinimumPositive: 0.6, MaximumNegative: 0.2}}, resolver
}

func TestAudioEligibilityChecksAllChannelsBeforePersonalization(t *testing.T) {
	cat := testCatalog()
	service, resolver := cachedAudioService(t, cat)
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
	intent := testIntent(5)
	intent.OriginalDescription = "ambient electronica, no rock"
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "ambient electronica", Scope: "playlist"}}
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_style", Value: "rock"}}
	intent.Controls.Discovery = 1
	var checked []string
	playlist, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, OnChecked: func(t core.TrackRef) { checked = append(checked, t.ID) }, Profile: core.TasteProfile{Clusters: []core.TasteCluster{{ID: "rock", Weight: 1, Affinity: core.EmbeddingAffinity{Audio: []float32{0, 1}, Cooccurrence: []float32{1, 0}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if playlist.Outcome.State != core.OutcomePartial || len(playlist.Tracks) == 0 {
		t.Fatalf("unexpected result: %+v", playlist)
	}
	for _, track := range playlist.Tracks {
		if track.ID != "audio" && track.ID != "last" {
			t.Fatalf("unchecked/mismatching candidate leaked: %+v", track)
		}
	}
	if len(checked) == 0 || playlist.AudioEvidence == nil || playlist.AudioEvidence.CacheHits == 0 || resolver.calls != 0 {
		t.Fatalf("missing progressive/cache evidence: %+v", playlist.AudioEvidence)
	}
}

func TestRequiredAudioConflictAndStageSpecificJourney(t *testing.T) {
	cat := testCatalog()
	service, _ := cachedAudioService(t, cat)
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
	intent := testIntent(2)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "electronic", Scope: "playlist"}}
	intent.RequiredTracks = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "cooc", Influence: core.InfluencePositive}}
	conflict, err := engine.Build(context.Background(), intent)
	if err != nil || conflict.Outcome.State != core.OutcomeNeedsClarification {
		t.Fatalf("required conflict lost: %+v %v", conflict, err)
	}
	intent.RequiredTracks = nil
	intent.Mode = core.ModeJourney
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "electronic", Scope: "journey_start"}, {Kind: "style", Value: "rock", Scope: "journey_end"}}
	journey, err := engine.Build(context.Background(), intent)
	if err != nil || len(journey.Tracks) != 2 {
		t.Fatalf("journey failed: %+v %v", journey, err)
	}
	if journey.Tracks[0].ID != "audio" && journey.Tracks[0].ID != "last" {
		t.Fatal("journey start did not match its stage")
	}
	if journey.Tracks[1].ID == "audio" || journey.Tracks[1].ID == "last" {
		t.Fatal("journey end did not match its stage")
	}
}

func TestStopKeepsOnlyAlreadyCheckedCandidates(t *testing.T) {
	cat := testCatalog()
	service, _ := cachedAudioService(t, cat)
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
	intent := testIntent(5)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "electronic", Scope: "playlist"}}
	stop := make(chan struct{})
	seen := 0
	playlist, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, StopChecking: stop, OnChecked: func(core.TrackRef) {
		seen++
		if seen == 1 {
			close(stop)
		}
	}})
	if err != nil || len(playlist.Tracks) != 1 || playlist.Outcome.State != core.OutcomePartial || !playlist.AudioEvidence.Stopped {
		t.Fatalf("stop result=%+v err=%v", playlist, err)
	}
}

func TestProgressiveTracksHavePassedMetadataExclusions(t *testing.T) {
	cat := testCatalog()
	service, _ := cachedAudioService(t, cat)
	features := &semanticFixture{info: core.FeatureStoreInfo{SchemaVersion: 3, CatalogVersion: cat.CatalogVersion(), SupportedFacets: []string{"styles"}}, features: map[string]core.TrackFeatures{
		"audio": completeStyleFeature("audio", "rock"),
		"last":  completeStyleFeature("last", "electronic"),
	}}
	engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, features, nil, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
	intent := testIntent(5)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "electronic", Scope: "playlist"}}
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_style", Value: "rock"}}
	seen := 0
	_, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, OnChecked: func(track core.TrackRef) {
		seen++
		if track.ID != "last" {
			t.Fatalf("progressive track failed metadata exclusion: %s", track.ID)
		}
	}})
	if err != nil || seen == 0 {
		t.Fatalf("progressive results failed: %d %v", seen, err)
	}
}

func TestAnchorReplacementIsBoundedAndDoesNotRewriteIntent(t *testing.T) {
	cat := testCatalog()
	service, _ := cachedAudioService(t, cat)
	calls := 0
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service }).WithAnchorProposer(func(_ context.Context, _ core.MusicIntent, rejected []string) ([]core.InferredAnchor, error) {
		calls++
		if len(rejected) != 1 {
			t.Fatal("unexpected rejected proposals")
		}
		return []core.InferredAnchor{{Reference: core.IntentReference{Kind: core.ReferenceTrack, Query: "Seed Artist - Origin", TrackID: "seed", Influence: core.InfluencePositive}, Role: "replacement"}}, nil
	})
	intent := testIntent(1)
	intent.References = nil
	intent.InferredAnchors = []core.InferredAnchor{{Reference: core.IntentReference{Kind: core.ReferenceTrack, Query: "Context Artist - Close", TrackID: "cooc", Influence: core.InfluencePositive}}}
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "unfamiliar genre", Scope: "playlist"}}
	result, err := engine.Build(context.Background(), intent)
	if err != nil || calls != 1 || len(result.Intent.AnchorAttempts) != 2 || result.Intent.EssentialCriteria[0].Value != "unfamiliar genre" {
		t.Fatalf("replacement=%+v calls=%d err=%v", result, calls, err)
	}
}
