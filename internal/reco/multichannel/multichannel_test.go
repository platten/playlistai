package multichannel

import (
	"context"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func testCatalog() *fakes.Catalog {
	return fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "seed", Display: "Seed Artist - Origin", Audio: []float32{1, 0}, Track: []float32{0, 1}},
		fakes.CatalogTrack{ID: "audio", Display: "Audio Artist - Close", Audio: []float32{.99, .01}, Track: []float32{-1, 0}},
		fakes.CatalogTrack{ID: "audio-copy", Display: "Audio Artist - Close", Audio: []float32{.98, .02}, Track: []float32{-.99, .01}},
		fakes.CatalogTrack{ID: "cooc", Display: "Context Artist - Close", Audio: []float32{-1, 0}, Track: []float32{.01, .99}},
		fakes.CatalogTrack{ID: "taste", Display: "Taste Artist - Favorite", Audio: []float32{0, 1}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "blocked", Display: "Blocked Artist - Nope", Audio: []float32{.95, .05}, Track: []float32{.1, .9}},
		fakes.CatalogTrack{ID: "other", Display: "Other Artist - Elsewhere", Audio: []float32{.7, .7}, Track: []float32{.7, .7}},
		fakes.CatalogTrack{ID: "far", Display: "Far Artist - Edge", Audio: []float32{-.7, .7}, Track: []float32{.7, -.7}},
		fakes.CatalogTrack{ID: "last", Display: "Last Artist - End", Audio: []float32{.5, .86}, Track: []float32{.86, .5}},
	)
}

func testIntent(count int) core.MusicIntent {
	return core.MusicIntent{
		Version:    core.CurrentIntentVersion,
		References: []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "seed", Influence: core.InfluencePositive}},
		Controls:   core.IntentControls{TotalTrackCount: count, AudioWeight: .5, CooccurrenceWeight: .5},
		Mode:       core.ModeSimilar, Seed: "42",
	}.Normalized()
}

func TestRetrieverContributesIndependentChannelsAndProvenance(t *testing.T) {
	t.Parallel()
	cat := testCatalog()
	cfg := DefaultConfig()
	cfg.SeedAudioBudget, cfg.SeedCooccurrenceBudget, cfg.TasteClusterBudget = 4, 4, 1
	retriever := NewRetriever(cat, fakes.NewSimilarityEngine(cat), cfg)
	profile := core.TasteProfile{Clusters: []core.TasteCluster{{
		ID: "taste", Weight: 1,
		Affinity: core.EmbeddingAffinity{Audio: []float32{0, 1}, Cooccurrence: []float32{1, 0}},
	}}}
	candidates, err := retriever.Retrieve(context.Background(), ports.RetrievalRequest{
		Intent: testIntent(4), Profile: profile, RecentSelections: refs(cat, "last"), Seed: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"audio": ChannelSeedAudio, "cooc": ChannelSeedCooccurrence, "taste": ChannelTasteCluster}
	for id, channel := range want {
		candidate, ok := findCandidate(candidates, id)
		if !ok || !hasChannel(candidate, channel) {
			t.Fatalf("candidate %q missing %q provenance: %+v", id, channel, candidates)
		}
	}
	if _, found := findCandidate(candidates, "seed"); found {
		t.Fatal("reference track should be excluded from its retrieval results")
	}
	if _, found := findCandidate(candidates, "last"); found {
		t.Fatal("recent continuation track should not be retrieved again")
	}
	if !candidatesHaveChannel(candidates, ChannelSeedAudio) ||
		!candidatesHaveChannel(candidates, ChannelContinuationAudio) ||
		!candidatesHaveChannel(candidates, ChannelTasteCluster) {
		t.Fatalf("continuation did not retain original seed and taste channels: %+v", candidates)
	}
	other, ok := findCandidate(candidates, "other")
	if !ok || !hasChannel(other, ChannelSeedAudio) || !hasChannel(other, ChannelSeedCooccurrence) {
		t.Fatalf("union lost multi-channel provenance: %+v", other)
	}
}

func TestOrchestratorAppliesHardEligibilityBeforeSelection(t *testing.T) {
	t.Parallel()
	cat := testCatalog()
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	intent := testIntent(5)
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_artist", Value: "Blocked Artist", Supported: true}}
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]struct{}{}
	seenIDs := map[string]struct{}{}
	for _, track := range playlist.Tracks {
		if track.ID == "blocked" || track.ID == "seed" {
			t.Fatalf("ineligible track selected: %+v", track)
		}
		key := core.ProvisionalRecordingKey(track)
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("duplicate recording selected: %+v", track)
		}
		seen[key] = struct{}{}
		seenIDs[track.ID] = struct{}{}
	}
	if _, original := seenIDs["audio"]; original {
		if _, copy := seenIDs["audio-copy"]; copy {
			t.Fatal("same recording selected under two catalog IDs")
		}
	}
	if len(playlist.Rationale) == 0 || len(playlist.Rationale[0].Evidence) == 0 || len(playlist.Rationale[0].Sources) == 0 {
		t.Fatalf("per-pick evidence missing: %+v", playlist.Rationale)
	}
}

func TestRankerPenalizesNegativePreferenceAndRecentExposure(t *testing.T) {
	t.Parallel()
	cat := testCatalog()
	cfg := DefaultConfig()
	cfg.NegativePenalty, cfg.ExposurePenalty = 1, 1
	ranker := NewRanker(cat, cfg)
	candidates := []core.Candidate{
		candidateFor(cat, "audio", 1), candidateFor(cat, "other", .5),
	}
	profile := core.TasteProfile{
		Negative:      core.EmbeddingAffinity{Audio: []float32{1, 0}, Cooccurrence: []float32{-1, 0}},
		ExposureCount: 1, RecentExposures: map[string]float64{"audio-copy": 1},
	}
	ranked, err := ranker.Rank(context.Background(), candidates, ports.RankRequest{Intent: testIntent(2), Profile: profile})
	if err != nil {
		t.Fatal(err)
	}
	audio, _ := findCandidate(ranked, "audio")
	if !audio.Available.NegativeMatch || audio.Scores.NegativeMatch <= 0 ||
		!audio.Available.RecentExposure || audio.Scores.RecentExposure != 1 {
		t.Fatalf("negative/exposure components missing: %+v", audio)
	}
	if ranked[0].Track.ID == "audio" {
		t.Fatalf("penalized candidate still ranked first: %+v", ranked)
	}
}

func TestRankerColdStartMarksProfileFeaturesUnavailable(t *testing.T) {
	t.Parallel()
	cat := testCatalog()
	ranked, err := NewRanker(cat, DefaultConfig()).Rank(context.Background(),
		[]core.Candidate{candidateFor(cat, "audio", 1)}, ports.RankRequest{Intent: testIntent(1)})
	if err != nil {
		t.Fatal(err)
	}
	got := ranked[0]
	if got.Available.ListenerAffinity || got.Available.NegativeMatch || got.Available.RecentExposure || got.Available.Novelty {
		t.Fatalf("cold-start features presented as measured: %+v", got.Available)
	}
	if !got.Available.AudioSeedAffinity || !got.Available.CooccurrenceAffinity {
		t.Fatalf("seed features unexpectedly unavailable: %+v", got.Available)
	}
}

func TestGenerationIsReproducibleWithBoundedExploration(t *testing.T) {
	t.Parallel()
	cat := testCatalog()
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	intent := testIntent(4)
	intent.Controls.Discovery = .9
	first, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("fixed-seed generations differ:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if first.Seed != "42" || len(first.Tracks) != 4 {
		t.Fatalf("unexpected generation: %+v", first)
	}
	if first.Intent.Seed != intent.Seed || !reflect.DeepEqual(first.Intent.Controls, intent.Normalized().Controls) ||
		len(first.Intent.References) != 1 || first.Intent.References[0].Resolution == nil ||
		engine.AlgorithmVersion() != AlgorithmVersion {
		t.Fatalf("generation contract/version was not preserved: intent=%+v version=%q", first.Intent, engine.AlgorithmVersion())
	}
}

func candidateFor(cat ports.Catalog, id string, fusion float64) core.Candidate {
	meta, _ := cat.Meta(id)
	return core.Candidate{
		Track: meta.Ref, Sources: []core.RetrievalEvidence{{Channel: ChannelSeedAudio, QueryID: "seed", Rank: 1, Score: fusion}},
		Scores: core.CandidateScores{RetrievalFusion: fusion}, Available: core.CandidateFeatures{RetrievalFusion: true},
	}
}

func findCandidate(candidates []core.Candidate, id string) (core.Candidate, bool) {
	for _, candidate := range candidates {
		if candidate.Track.ID == id {
			return candidate, true
		}
	}
	return core.Candidate{}, false
}

func candidatesHaveChannel(candidates []core.Candidate, channel string) bool {
	for _, candidate := range candidates {
		if hasChannel(candidate, channel) {
			return true
		}
	}
	return false
}
