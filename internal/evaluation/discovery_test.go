package evaluation

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/localcatalog"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/multichannel"
)

type testDiscoveryProvider struct {
	manager   *librarypack.Manager
	namespace string
	pins      int
	snapshot  string
}

func (p *testDiscoveryProvider) SnapshotID() string { return p.snapshot }

func (p *testDiscoveryProvider) Pin(context.Context) ([]*localcatalog.Catalog, func(), error) {
	p.pins++
	lease, err := p.manager.Pin()
	if err != nil {
		return nil, nil, err
	}
	catalog, err := localcatalog.Open(lease, localcatalog.Options{SourceID: p.namespace, Shared: true})
	if err != nil {
		return nil, nil, err
	}
	return []*localcatalog.Catalog{catalog}, func() { _ = catalog.Close() }, nil
}

func discoveryFixture(t *testing.T) (Runner, *testDiscoveryProvider, func()) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "discovery.paipack")
	_, err := librarypack.Write(ctx, path, librarypack.Pack{CorpusGeneration: "corpus", MetadataGeneration: "metadata", MERTGeneration: "mert", MERT: librarypack.VectorSpace{Name: "library_mert", Dimension: 2, DType: "float32", ByteOrder: "little", Normalized: true, Model: "fixture", ModelRevision: "one", GraphSHA256: strings.Repeat("a", 64), Decoder: "fixture", Preprocessing: "fixture", Sampling: "fixture", Pooling: "fixture", Scope: "sampled_windows", Missingness: "absent"}, Tracks: []librarypack.Track{{ID: "seed", Artist: "Seed", Title: "Origin", MERT: []float32{1, 0}}, {ID: "metadata", Artist: "Calm Artist", Title: "Calm Song", RawTags: []byte(`{"genre":"ambient","mood":"calm"}`)}, {ID: "mert", Artist: "Other", Title: "Near", MERT: []float32{0.8, 0.6}}}}, librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := librarypack.OpenManager(ctx, t.TempDir(), librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	staged, err := manager.Stage(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := localcatalog.BuildIndexes(ctx, staged.Generation(), localcatalog.IndexBuildOptions{Workers: 1}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate(ctx, staged); err != nil {
		t.Fatal(err)
	}
	provider := &testDiscoveryProvider{manager: manager, namespace: "evaluation", snapshot: "release-one"}
	base := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "base", Display: "Base - Track", Audio: []float32{1, 0}, Track: []float32{1, 0}})
	runner := Runner{Catalog: base, Resolver: base, Similarity: fakes.NewSimilarityEngine(base), Parser: rules.New(), K: 2}
	runner, release, err := runner.WithDiscovery(ctx, provider)
	if err != nil {
		t.Fatal(err)
	}
	return runner, provider, release
}

func discoveryIntent() core.MusicIntent {
	return core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeSimilar, VerificationPolicy: core.BestAvailable, Count: 2, TrackCountExplicit: true, References: []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "pack:evaluation:seed", Influence: core.InfluencePositive}}, Preferences: core.SemanticPreferences{Moods: []core.IntentPreference{{Value: "calm", Influence: core.InfluencePositive}}}, Controls: core.IntentControls{TotalTrackCount: 2, AudioWeight: 1, RecommendationMode: core.EnhancedHybrid}}
}

func TestDiscoveryEvaluationFreezesSourcesSeedsAndFourOfflineVariants(t *testing.T) {
	runner, provider, release := discoveryFixture(t)
	defer release()
	dataset := Dataset{Version: ContractVersion, Name: "shared-fixture", Evidence: EvidenceSynthetic}
	for i := 0; i < 5; i++ {
		dataset.RecommendationCases = append(dataset.RecommendationCases, RecommendationCase{ID: fmt.Sprint(i), ListenerID: "fixture", OccurredAt: time.Date(2026, 1, i+1, 0, 0, 0, 0, time.UTC), Intent: discoveryIntent()})
	}
	report, err := runner.Run(context.Background(), dataset)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Development) != 4 || len(report.HeldOutTest) != 4 {
		t.Fatalf("variants dev/test: %d/%d", len(report.Development), len(report.HeldOutTest))
	}
	seeds := map[string]core.RNGSeed{}
	for _, variant := range append(report.Development, report.HeldOutTest...) {
		for _, item := range variant.Cases {
			if item.Error != "" {
				t.Fatalf("%s: %s", variant.Name, item.Error)
			}
			if item.Generation.CatalogVersion != runner.Resolver.CatalogVersion() {
				t.Fatalf("snapshot changed: %+v", item.Generation)
			}
			if item.Generation.DiscoverySnapshot == "" || item.Generation.DiscoverySnapshot != report.DiscoverySnapshot {
				t.Fatal("missing immutable discovery fingerprint")
			}
			if item.Generation.RNGSeed.IsZero() {
				t.Fatal("missing deterministic seed")
			}
			if prior, ok := seeds[item.CaseID]; ok && prior != item.Generation.RNGSeed {
				t.Fatalf("seed changed across variants: %s", item.CaseID)
			}
			seeds[item.CaseID] = item.Generation.RNGSeed
			if item.NDCGAtK != nil {
				t.Fatal("unjudged tracks treated as negative labels")
			}
		}
	}
	if provider.pins != 9 {
		t.Fatalf("expected one retained and eight request pins, got %d", provider.pins)
	}
	if !strings.Contains(strings.Join(report.Limitations, " "), "Channel filtering follows production retrieval") {
		t.Fatal("ablation cost limitation missing")
	}
	root := t.TempDir()
	if err := WriteBlindComparison(report, dataset, runner.Catalog, "discovery_baseline", "discovery_combined", "42", filepath.Join(root, "blind.json"), filepath.Join(root, "key.json")); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryAblationPreservesHardEligibilityAndControlsAudioEvidence(t *testing.T) {
	runner, _, release := discoveryFixture(t)
	defer release()
	ctx := context.Background()
	criterion := core.MusicalCriterion{Kind: "genre", Value: "ambient"}
	for _, enabled := range []bool{false, true} {
		catalog := discoveryAblationCatalog{Catalog: runner.Catalog, mert: enabled}
		if !catalog.SupportsCriterion(criterion) || catalog.CriterionEvidence(ctx, "pack:evaluation:metadata", criterion) != core.EvidenceMatch {
			t.Fatal("hard eligibility changed with ablation")
		}
		if _, ok, err := catalog.LibraryVector(ctx, "pack:evaluation:seed"); err != nil || ok != enabled {
			t.Fatalf("vector availability: %v %v", ok, err)
		}
		if _, ok := catalog.LibraryDSPPreference(ctx, "pack:evaluation:seed", discoveryIntent()); ok {
			t.Fatal("DSP leaked into MERT-only")
		}
	}
}

func TestDiscoveryVariantsPreserveProductionProfilesWithExistingKnowledge(t *testing.T) {
	runner, _, release := discoveryFixture(t)
	defer release()
	for _, variant := range runner.discoveryVariants(parametersFromConfig("test", multichannel.DefaultConfig())) {
		t.Run(variant.name, func(t *testing.T) {
			intent := discoveryIntent()
			intent.Seed = "42"
			intent.References = []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Calm Artist", Influence: core.InfluencePositive}}
			intent.Knowledge = &core.KnowledgeSnapshot{ID: "existing", PackProfiles: []core.DiscoveryProfile{{Artist: "stale"}}}
			playlist, err := variant.engine.Build(context.Background(), intent)
			if err != nil {
				t.Fatal(err)
			}
			if playlist.Intent.Knowledge == nil {
				t.Fatal("knowledge snapshot lost")
			}
			profiles := playlist.Intent.Knowledge.PackProfiles
			expected := variant.name == "discovery_metadata_only" || variant.name == "discovery_combined"
			if !expected && len(profiles) != 0 {
				t.Fatalf("disabled profiles survived: %+v", profiles)
			}
			if expected {
				found := false
				for _, profile := range profiles {
					found = found || profile.Artist == "Calm Artist" && len(profile.SupportingTracks) > 0 && len(profile.Moods) > 0
				}
				if !found {
					t.Fatalf("production profiles missing: %+v", profiles)
				}
			}
		})
	}
}

type recordingKnowledgeReceiver struct {
	ports.Catalog
	tracks []core.EnrichedTrack
}

func (c *recordingKnowledgeReceiver) BindRecordingKnowledge(tracks []core.EnrichedTrack) {
	c.tracks = tracks
}

func TestDiscoveryAblationAlwaysForwardsRecordingIdentity(t *testing.T) {
	tracks := []core.EnrichedTrack{{Ref: core.TrackRef{ID: "base"}, RecordingID: "12345678-1234-1234-1234-123456789012", IdentityStatus: core.ResolutionResolved}}
	for _, flags := range [][3]bool{{false, false, false}, {false, false, true}, {true, false, false}, {true, true, true}} {
		receiver := &recordingKnowledgeReceiver{}
		catalog := discoveryAblationCatalog{Catalog: receiver, mert: flags[0], dsp: flags[1], profiles: flags[2]}
		catalog.BindRecordingKnowledge(tracks)
		if len(receiver.tracks) != 1 || receiver.tracks[0].RecordingID != tracks[0].RecordingID {
			t.Fatal("recording identity binding lost")
		}
	}
}

func TestDiscoveryRejectsChangedSnapshot(t *testing.T) {
	for _, field := range []string{"namespace", "companion"} {
		t.Run(field, func(t *testing.T) {
			runner, provider, release := discoveryFixture(t)
			defer release()
			if field == "namespace" {
				provider.namespace = "changed"
			} else {
				provider.snapshot = "companion-changed"
			}
			variants := runner.discoveryVariants(parametersFromConfig("test", multichannel.DefaultConfig()))
			intent := discoveryIntent()
			intent.Seed = "42"
			if _, err := variants[0].engine.Build(context.Background(), intent); err == nil || !strings.Contains(err.Error(), "snapshot changed") {
				t.Fatalf("changed pin accepted: %v", err)
			}
		})
	}
}

type fixedDiscoveryRetriever []core.Candidate

func (r fixedDiscoveryRetriever) Retrieve(context.Context, ports.RetrievalRequest) ([]core.Candidate, error) {
	return []core.Candidate(r), nil
}

func TestDiscoveryAblationFiltersSourcesAndRecomputesFusion(t *testing.T) {
	pack := &core.LibraryEvidenceSource{PackID: "fixture"}
	fixture := fixedDiscoveryRetriever{
		{Track: core.TrackRef{ID: "meta"}, Sources: []core.RetrievalEvidence{{Channel: localcatalog.MetadataChannel, Rank: 1, QueryWeight: 1, LibrarySource: pack}}},
		{Track: core.TrackRef{ID: "audio"}, Sources: []core.RetrievalEvidence{{Channel: localcatalog.MERTChannel, Rank: 1, QueryWeight: 1, LibrarySource: pack}}},
		{Track: core.TrackRef{ID: "both"}, Scores: core.CandidateScores{RetrievalFusion: 100}, Sources: []core.RetrievalEvidence{{Channel: localcatalog.MetadataChannel, Rank: 2, QueryWeight: 1, LibrarySource: pack}, {Channel: localcatalog.MERTChannel, Rank: 1, QueryWeight: 1, LibrarySource: pack}}},
	}
	for _, mert := range []bool{false, true} {
		out, err := (discoveryAblationRetriever{base: fixture, mert: mert}).Retrieve(context.Background(), ports.RetrievalRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 2 {
			t.Fatalf("ablation candidates: %+v", out)
		}
		for _, candidate := range out {
			if len(candidate.Sources) != 1 {
				t.Fatalf("leaked source: %+v", candidate)
			}
			if candidate.Scores.RetrievalFusion > 1 {
				t.Fatalf("stale fusion: %+v", candidate)
			}
			if (candidate.Sources[0].Channel == localcatalog.MERTChannel) != mert {
				t.Fatalf("wrong channel: %+v", candidate)
			}
		}
	}
}
