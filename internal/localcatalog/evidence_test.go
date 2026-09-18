package localcatalog

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type writableBase struct {
	testBase
	added []core.TrackMeta
}

func (b *writableBase) RegisterDynamicTrack(track core.TrackMeta) error {
	b.added = append(b.added, track)
	return nil
}

func TestOverlayPreservesRegistrationOnlyForWritableCombinedCatalog(t *testing.T) {
	local, manager := openTestCatalog(t, testTracks(), nil)
	defer manager.Close()
	defer local.Close()
	for _, mode := range []RecommendationMode{ModeCombined, ModeLibraryOnly} {
		base := &writableBase{}
		overlay, err := PinRecommendationOverlay(context.Background(), packProvider{manager}, base, base, baseRetriever{}, mode, 1)
		if err != nil {
			t.Fatal(err)
		}
		registrar, available := overlay.Catalog.(ports.DynamicTrackCatalog)
		if available != (mode == ModeCombined) {
			t.Fatalf("%s registration=%v", mode, available)
		}
		if available {
			if err := registrar.RegisterDynamicTrack(core.TrackMeta{Ref: core.TrackRef{ID: "external"}}); err != nil || len(base.added) != 1 {
				t.Fatalf("registration lost: %v", err)
			}
		}
		overlay.Close()
	}
	overlay, err := PinRecommendationOverlay(context.Background(), packProvider{manager}, testBase{}, testBase{}, baseRetriever{}, ModeCombined, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer overlay.Close()
	if _, ok := overlay.Catalog.(ports.DynamicTrackCatalog); ok {
		t.Fatal("read-only base advertises registration")
	}
}

func TestLocalQueriesDoNotMultiplyRepeatedReferences(t *testing.T) {
	ref := core.IntentReference{TrackID: "local:test:seed", Influence: core.InfluencePositive, Resolution: &core.ReferenceResolution{Selected: &core.ResolutionCandidate{Representatives: []core.WeightedTrack{{TrackID: "local:test:seed", Weight: 1}}}}}
	queries := recommendationQueries(ports.RetrievalRequest{Intent: core.MusicIntent{References: []core.IntentReference{ref, ref}}})
	if len(queries) != 1 {
		t.Fatalf("repeated seed creates %d queries", len(queries))
	}
}

func TestDSPRejectsUnknownValuesAndPreservesOppositePreferences(t *testing.T) {
	var record portableDSPRecord
	if err := json.Unmarshal([]byte(`{"windows":[{"observedSeconds":5,"features":{"bass_energy_ratio":{"value":0.9,"state":"unknown"}}}]}`), &record); err != nil {
		t.Fatal(err)
	}
	if value, ok := sampledDSPMean(record, "bass_energy_ratio"); ok || value != 0 {
		t.Fatalf("unknown observation accepted: %v %v", value, ok)
	}
	intent := core.MusicIntent{Preferences: core.SemanticPreferences{TextureDescriptions: []core.IntentPreference{
		{ConceptID: "texture.bass-heavy", Influence: core.InfluencePositive},
		{ConceptID: "texture.bass-heavy", Influence: core.InfluenceNegative},
		{ConceptID: "texture.bass-heavy", Scope: "journey_end", Influence: core.InfluencePositive},
	}}}
	preferences := reviewedDSPPreferences(intent)
	if len(preferences) != 2 || preferences[0].direction+preferences[1].direction != 0 {
		t.Fatalf("lost polarity/scope: %+v", preferences)
	}
}
