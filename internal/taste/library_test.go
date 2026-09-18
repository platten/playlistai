package taste

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type libraryProfileCatalog struct {
	ports.Catalog
	vectors map[string]core.LibraryVector
}

func (c libraryProfileCatalog) LibraryVector(_ context.Context, id string) (core.LibraryVector, bool, error) {
	v, ok := c.vectors[id]
	return v, ok, nil
}
func (c libraryProfileCatalog) LibraryDSPPreference(context.Context, string, core.MusicIntent) (float64, bool) {
	return 0, false
}

func TestLibraryTasteKeepsSpacesSeparateAndCorrectsExplicitFeedback(t *testing.T) {
	source := core.LibraryEvidenceSource{PackID: "pack", SpaceID: "mert-contract", Generation: "generation", Scope: "sampled_windows"}
	other := source
	other.SpaceID = "different-contract"
	cat := libraryProfileCatalog{profileCatalog(), map[string]core.LibraryVector{
		"local:a": {Source: source, Values: []float32{1, 0}}, "local:b": {Source: source, Values: []float32{0, 1}},
		"local:c": {Source: other, Values: []float32{1, 0}}, "local:invalid": {Source: source, Values: []float32{float32(math.NaN()), 0}},
	}}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []core.FeedbackEvent{
		feedback(core.FeedbackLike, core.FeedbackScopeDurable, "local:a", "", now),
		feedback(core.FeedbackLike, core.FeedbackScopeDurable, "local:b", "", now),
		feedback(core.FeedbackLike, core.FeedbackScopeDurable, "local:c", "", now),
		feedback(core.FeedbackExposure, core.FeedbackScopeRequest, "a", "request", now),
		feedback(core.FeedbackLike, core.FeedbackScopeDurable, "local:invalid", "", now),
		feedback(core.FeedbackLessLike, core.FeedbackScopeRequest, "local:a", "request", now),
	}
	profile, err := BuildProfile(context.Background(), cat, events, ProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.Library) != 2 || profile.PositiveEvidence != 3 || profile.RequestEvidence != 0 || profile.ColdStart || len(profile.Clusters) != 0 {
		t.Fatalf("incorrect local profile: %+v", profile)
	}
	if !reflect.DeepEqual(profile.Positive.Audio, []float32{0, 0}) {
		t.Fatal("library vectors entered Deej-AI affinity")
	}
	var mert core.LibraryTaste
	for _, p := range profile.Library {
		if p.Source.SpaceID == source.SpaceID {
			mert = p
		}
	}
	if len(mert.Clusters) != 2 {
		t.Fatalf("interests or correction missing: %+v", mert)
	}
	contextual, err := BuildProfile(context.Background(), cat, events, ProfileOptions{RequestID: "request"})
	if err != nil {
		t.Fatal(err)
	}
	if contextual.PositiveEvidence != 2 || contextual.RequestEvidence != 1 {
		t.Fatalf("request correction did not override durable preference: %+v", contextual)
	}
	for _, p := range contextual.Library {
		if p.Source.SpaceID == source.SpaceID && !reflect.DeepEqual(p.RequestNegative, []float32{1, 0}) {
			t.Fatalf("request negative missing: %+v", p)
		}
	}
	corrected := append(append([]core.FeedbackEvent(nil), events...), feedback(core.FeedbackDislike, core.FeedbackScopeDurable, "local:a", "", now.Add(time.Hour)))
	next, err := BuildProfile(context.Background(), cat, corrected, ProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if next.PositiveEvidence != 2 || next.NegativeEvidence != 1 || next.SnapshotID == profile.SnapshotID {
		t.Fatalf("durable correction lost: %+v", next)
	}
	for _, p := range next.Library {
		if p.Source.SpaceID == source.SpaceID && (!reflect.DeepEqual(p.Positive, []float32{0, 1}) || !reflect.DeepEqual(p.Negative, []float32{1, 0})) {
			t.Fatalf("correction centroid: %+v", p)
		}
	}
	// A pack with no explicit judgments, or only exposure, remains cold start.
	cold, err := BuildProfile(context.Background(), cat, events[3:4], ProfileOptions{})
	if err != nil || !cold.ColdStart || len(cold.Library) != 0 {
		t.Fatalf("ownership/exposure became approval: %+v %v", cold, err)
	}
}

func TestLibraryTasteSnapshotIncludesRepresentationAndDecays(t *testing.T) {
	source := core.LibraryEvidenceSource{PackID: "pack", SpaceID: "space", Generation: "generation", Scope: "sampled_windows"}
	cat := libraryProfileCatalog{profileCatalog(), map[string]core.LibraryVector{"local:a": {Source: source, Values: []float32{1, 0}}, "local:b": {Source: source, Values: []float32{0, 1}}}}
	now := time.Now().UTC()
	events := []core.FeedbackEvent{feedback(core.FeedbackLike, core.FeedbackScopeDurable, "local:a", "", now.Add(-profileHalfLife)), feedback(core.FeedbackLike, core.FeedbackScopeDurable, "local:b", "", now)}
	first, err := BuildProfile(context.Background(), cat, events, ProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(float64(first.Library[0].Positive[1]/first.Library[0].Positive[0])-2) > 1e-6 {
		t.Fatalf("decay missing: %+v", first.Library)
	}
	v := cat.vectors["local:a"]
	v.Source.Generation = "replacement"
	cat.vectors["local:a"] = v
	second, err := BuildProfile(context.Background(), cat, events, ProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.SnapshotID == second.SnapshotID || len(second.Library) != 2 {
		t.Fatal("representation replacement did not change profile identity")
	}
}
