package multichannel

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type semanticFixture struct {
	info               core.FeatureStoreInfo
	features           map[string]core.TrackFeatures
	positive, negative []core.SemanticHit
}

func (s *semanticFixture) Info() core.FeatureStoreInfo { return s.info }
func (s *semanticFixture) Features(_ context.Context, id string) (core.TrackFeatures, bool, error) {
	value, ok := s.features[id]
	return value, ok, nil
}
func (s *semanticFixture) Search(_ context.Context, text string, _ int) ([]core.SemanticHit, error) {
	if strings.Contains(strings.ToLower(text), "sleepy") && !strings.Contains(strings.ToLower(text), "relaxing") {
		return append([]core.SemanticHit(nil), s.negative...), nil
	}
	return append([]core.SemanticHit(nil), s.positive...), nil
}

func TestSeedlessSemanticRequestReturnsGroundedCatalogTracks(t *testing.T) {
	cat := semanticCatalog()
	sem := semanticData()
	engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, sem, sem, DefaultConfig())
	if !strings.Contains(engine.AlgorithmVersion(), "pilot/v1@abc123") {
		t.Fatalf("semantic version missing from generation identity: %q", engine.AlgorithmVersion())
	}
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeSimilar, Seed: "8",
		Preferences: core.SemanticPreferences{Moods: []core.IntentPreference{{Value: "relaxing", Influence: core.InfluencePositive, Explicit: true}}},
		Controls:    core.IntentControls{TotalTrackCount: 2, AudioWeight: .5, CooccurrenceWeight: .5},
	}
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(playlist.Tracks) != 2 || playlist.Tracks[0].ID == "" {
		t.Fatalf("seedless semantic playlist = %+v", playlist)
	}
	for _, track := range playlist.Tracks {
		if _, ok := cat.RowOf(track.ID); !ok {
			t.Fatalf("ungrounded track %q", track.ID)
		}
	}
	if len(playlist.Rationale) == 0 || !hasEvidence(playlist.Rationale[0], "semantic_text_match") {
		t.Fatalf("semantic evidence missing: %+v", playlist.Rationale)
	}
}

func TestSemanticNegationPenalizesMatchingCandidate(t *testing.T) {
	cat := semanticCatalog()
	sem := semanticData()
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeSimilar,
		Preferences: core.SemanticPreferences{
			Moods: []core.IntentPreference{{Value: "relaxing", Influence: core.InfluencePositive}, {Value: "sleepy", Influence: core.InfluenceNegative}},
		}, Controls: core.IntentControls{TotalTrackCount: 2, AudioWeight: .5, CooccurrenceWeight: .5}, Seed: "9"}.Normalized()
	retriever := NewSemanticRetriever(cat, fakes.NewSimilarityEngine(cat), sem, DefaultConfig())
	candidates, err := retriever.Retrieve(context.Background(), ports.RetrievalRequest{Intent: intent, Seed: 9})
	if err != nil {
		t.Fatal(err)
	}
	ranked, err := NewRanker(cat, DefaultConfig()).Rank(context.Background(), candidates, ports.RankRequest{Intent: intent})
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) < 2 || ranked[0].Track.ID != "instrumental" {
		t.Fatalf("negative sleepy match was not penalized: %+v", ranked)
	}
}

func TestStrictNoVocalsExcludesVocalAndUnknownEvidence(t *testing.T) {
	cat := semanticCatalog()
	sem := semanticData()
	engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, sem, sem, DefaultConfig())
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeSimilar, Seed: "10",
		Preferences:     core.SemanticPreferences{Instrumentation: []core.IntentPreference{{Value: "instrumental", Influence: core.InfluencePositive}}},
		HardConstraints: []core.HardConstraint{{Kind: "exclude_vocals", Value: "vocals", Supported: false}},
		Controls:        core.IntentControls{TotalTrackCount: 2, AudioWeight: .5, CooccurrenceWeight: .5},
	}
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(playlist.Tracks) != 1 || playlist.Tracks[0].ID != "instrumental" {
		t.Fatalf("strict vocal eligibility = %+v", playlist.Tracks)
	}
	if !noticeCode(playlist.Notices, "semantic_constraints_enforced") {
		t.Fatalf("enforcement status missing: %+v", playlist.Notices)
	}
	if len(playlist.Intent.HardConstraints) != 1 || !playlist.Intent.HardConstraints[0].RuntimeEnforced || playlist.Intent.HardConstraints[0].Supported {
		t.Fatalf("runtime enforcement was not distinguished from base support: %+v", playlist.Intent.HardConstraints)
	}
	conflict := intent
	conflict.RequiredTracks = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "sleepy", Influence: core.InfluencePositive}}
	conflict.Controls.TotalTrackCount = 1
	if _, err := engine.Build(context.Background(), conflict); !errors.Is(err, core.ErrRequiredTrackConflict) {
		t.Fatalf("required vocal track conflict = %v", err)
	}
}

func TestSemanticFallbackIsExplicitAndSeedlessRequiresSidecar(t *testing.T) {
	cat := semanticCatalog()
	seeded := core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeSimilar, Seed: "11",
		References:  []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "sleepy", Influence: core.InfluencePositive}},
		Preferences: core.SemanticPreferences{Moods: []core.IntentPreference{{Value: "relaxing", Influence: core.InfluencePositive}}},
		Controls:    core.IntentControls{TotalTrackCount: 1, AudioWeight: .5, CooccurrenceWeight: .5}}
	playlist, err := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).Build(context.Background(), seeded)
	if err != nil {
		t.Fatal(err)
	}
	if !noticeCode(playlist.Notices, "semantic_fallback") {
		t.Fatalf("fallback not explicit: %+v", playlist.Notices)
	}
	seedless := seeded
	seedless.References = nil
	if _, err := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).Build(context.Background(), seedless); err == nil || !strings.Contains(err.Error(), "semantic sidecar") {
		t.Fatalf("seedless fallback error = %v", err)
	}
}

func semanticCatalog() *fakes.Catalog {
	return fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "sleepy", Display: "A - Sleepy", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "instrumental", Display: "B - Awake", Audio: []float32{0, 1}, Track: []float32{0, 1}},
		fakes.CatalogTrack{ID: "unknown", Display: "C - Unknown", Audio: []float32{.7, .7}, Track: []float32{.7, .7}},
	)
}

func semanticData() *semanticFixture {
	known := func(id, vocal string) core.TrackFeatures {
		return core.TrackFeatures{SchemaVersion: 1, CatalogVersion: "fake:v1", TrackID: id, VocalEvidence: core.FeatureValue{Value: vocal, Missingness: core.FeatureKnown, Confidence: .9}}
	}
	return &semanticFixture{
		info:     core.FeatureStoreInfo{SchemaVersion: 1, CatalogVersion: "fake:v1", FeatureVersion: "pilot/v1", ModelRevision: "abc123", SupportedFacets: []string{"vocal_evidence", "styles"}},
		features: map[string]core.TrackFeatures{"sleepy": known("sleepy", "vocal"), "instrumental": known("instrumental", "instrumental"), "unknown": {SchemaVersion: 1, TrackID: "unknown", VocalEvidence: core.FeatureValue{Missingness: core.FeatureUnknown}}},
		positive: []core.SemanticHit{{TrackID: "sleepy", Score: .95}, {TrackID: "instrumental", Score: .85}, {TrackID: "unknown", Score: .75}},
		negative: []core.SemanticHit{{TrackID: "sleepy", Score: .99}, {TrackID: "unknown", Score: .2}, {TrackID: "instrumental", Score: .05}},
	}
}

func hasEvidence(reason core.StepReason, component string) bool {
	for _, item := range reason.Evidence {
		if item.Component == component && item.Available {
			return true
		}
	}
	return false
}
func noticeCode(notices []core.PlaylistNotice, code string) bool {
	for _, notice := range notices {
		if notice.Code == code {
			return true
		}
	}
	return false
}

var _ ports.FeatureStore = (*semanticFixture)(nil)
var _ ports.SemanticSearcher = (*semanticFixture)(nil)
