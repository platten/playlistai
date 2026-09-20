package app

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/multichannel"
)

// The real provider marks a fresh stream's snapshot recorded as soon as it is
// opened. That must not misclassify this fresh generation as a history replay.
type blockedDiscoverySource struct {
	opens    atomic.Int32
	pulls    atomic.Int32
	snapshot *core.KnowledgeSnapshot
}

func (s *blockedDiscoverySource) OpenCandidates(intent core.MusicIntent, _ ports.Catalog, _ ports.ReferenceResolver) ports.MusicCandidateStream {
	s.opens.Add(1)
	snapshot := core.KnowledgeSnapshot{}
	if intent.Knowledge != nil {
		snapshot = *intent.Knowledge
	}
	snapshot.DiscoveryRecorded = true
	s.snapshot = &snapshot
	return s
}
func (s *blockedDiscoverySource) Next(ctx context.Context) (core.TrackRef, error) {
	s.pulls.Add(1)
	<-ctx.Done()
	return core.TrackRef{}, ctx.Err()
}
func (s *blockedDiscoverySource) Snapshot() *core.KnowledgeSnapshot { return s.snapshot }

func TestEnhancedManagedDiscoveryChecksLocalCandidatesBeforeColdProvider(t *testing.T) {
	const wanted = 3
	ctx := context.Background()
	cfg := testConfig(t)
	cfg.Discovery.ManifestURL = "https://example.invalid/discovery/manifest.json"
	c := &Container{cfg: cfg}
	t.Cleanup(func() { _ = c.Close() })
	packPath := filepath.Join(t.TempDir(), "shared-metadata.paipack")
	tracks := []librarypack.Track{
		{ID: "good-a", Artist: "Alpha Artist", Title: "First", MusicBrainzRecording: "11111111-2222-3333-4444-555555555551", RawTags: []byte(`{"genre":"ambient"}`)},
		{ID: "good-a-copy", Artist: "Alpha Artist", Title: "First (alternate label)", MusicBrainzRecording: "11111111-2222-3333-4444-555555555551", RawTags: []byte(`{"genre":"ambient"}`)},
		{ID: "good-b", Artist: "Beta Artist", Title: "Second", MusicBrainzRecording: "11111111-2222-3333-4444-555555555552", RawTags: []byte(`{"genre":"ambient"}`)},
		{ID: "good-c", Artist: "Gamma Artist", Title: "Third", MusicBrainzRecording: "11111111-2222-3333-4444-555555555553", RawTags: []byte(`{"genre":"ambient"}`)},
		{ID: "good-d", Artist: "Delta Artist", Title: "Fourth", MusicBrainzRecording: "11111111-2222-3333-4444-555555555554", RawTags: []byte(`{"genre":"ambient"}`)},
		{ID: "excluded", Artist: "Excluded Artist", Title: "Excluded", RawTags: []byte(`{"genre":"ambient"}`)},
		{ID: "mismatch", Artist: "Wrong Artist", Title: "Ambient title", RawTags: []byte(`{"genre":"metal"}`)},
		{ID: "unknown", Artist: "Unknown Artist", Title: "Ambient words", RawTags: []byte(`{"comment":"ambient"}`)},
	}
	manifest, err := librarypack.Write(ctx, packPath, librarypack.Pack{CorpusGeneration: "latency-fixture", MetadataGeneration: "latency-metadata", Tracks: tracks}, librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	before := fileDigest(t, packPath)
	status, err := c.ImportDiscoveryAsset(ctx, packPath, nil)
	if err != nil || !status.Installed {
		t.Fatalf("managed discovery import failed: %+v %v", status, err)
	}
	if fileDigest(t, packPath) != before {
		t.Fatal("shared import modified source pack")
	}
	personal, err := c.LocalLibraryStatus()
	if err != nil || personal.Installed {
		t.Fatalf("shared discovery unexpectedly requires personal import: %+v %v", personal, err)
	}
	base := fakes.NewCatalog(3, fakes.CatalogTrack{ID: "bundled-unknown", Display: "Bundled Artist - Ambient words", Audio: []float32{1, 0, 0}, Track: []float32{0, 1, 0}})
	similarity := fakes.NewSimilarityEngine(base)
	engineConfig := multichannel.DefaultConfig()
	retriever := multichannel.NewRetriever(base, similarity, engineConfig)
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeSimilar, Seed: "42", VerificationPolicy: core.BestAvailable,
		Controls:          core.IntentControls{TotalTrackCount: wanted, RecommendationMode: core.EnhancedHybrid, AudioWeight: .5, CooccurrenceWeight: .5},
		Preferences:       core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "ambient", Influence: core.InfluencePositive, Explicit: true}}},
		EssentialCriteria: []core.MusicalCriterion{{Kind: "genre", Value: "ambient", Scope: "playlist"}},
		HardConstraints:   []core.HardConstraint{{Kind: "exclude_artist", Value: "Excluded Artist", Supported: true}},
	}.Normalized()
	overlay, err := c.pinDiscoveryRecommendationOverlay(ctx, intent, base, base, retriever)
	if err != nil {
		t.Fatal(err)
	}
	metadata, ok := overlay.Retriever.(interface{ SupportsIntentMetadata() bool })
	if !ok || !metadata.SupportsIntentMetadata() {
		overlay.Release()
		t.Fatal("real managed/shared overlay does not advertise metadata retrieval")
	}
	overlay.Release()
	source := &blockedDiscoverySource{}
	engine := multichannel.New(base, similarity, base, engineConfig).WithIntentOverlayProvider(c.pinDiscoveryRecommendationOverlay).WithCandidateSource(source)
	progress := &fakes.RecordingProgress{}
	audioChecked := 0
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	playlist, err := engine.BuildRecommendation(bounded, ports.RecommendationRequest{Intent: intent, Progress: progress, OnChecked: func(core.TrackRef) { audioChecked++ }})
	if err != nil {
		t.Fatalf("local candidates waited on cold provider: %v (provider pulls=%d)", err, source.pulls.Load())
	}
	if source.opens.Load() != 1 || source.pulls.Load() != 0 {
		t.Fatalf("expected a nonnil unopened-for-I/O stream: opens=%d pulls=%d", source.opens.Load(), source.pulls.Load())
	}
	if len(playlist.Tracks) != wanted || playlist.Outcome.State != core.OutcomeFulfilled {
		t.Fatalf("bounded eligible playlist not fulfilled: tracks=%+v outcome=%+v", playlist.Tracks, playlist.Outcome)
	}
	allowed := map[string]string{}
	for _, track := range tracks {
		if strings.HasPrefix(track.ID, "good-") {
			allowed["pack:discovery-"+manifest.PackID+":"+track.ID] = track.MusicBrainzRecording
		}
	}
	seen := map[string]bool{}
	for _, track := range playlist.Tracks {
		identity := allowed[track.ID]
		if identity == "" || track.Artist == "Excluded Artist" {
			t.Fatalf("identity/criterion/exclusion bypass: %+v", track)
		}
		if seen[identity] {
			t.Fatalf("missing or duplicate recording identity: %+v", track)
		}
		seen[identity] = true
	}
	checked := 0
	for _, row := range progress.Snapshot() {
		if strings.Contains(row.Note, "Checking candidates") {
			checked++
			if row.Done < 0 || row.Done > wanted || row.Total != wanted {
				t.Fatalf("unbounded checked progress: %+v", row)
			}
		}
	}
	if checked < wanted {
		t.Fatalf("local candidates did not emit checked progress: %+v", progress.Snapshot())
	}
	if audioChecked != 0 || playlist.AudioEvidence != nil {
		t.Fatal("metadata-only matches were advertised as audio-checked")
	}
}
