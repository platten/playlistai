package bridge

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestTrackCountPreviewAndGeneration(t *testing.T) {
	api := New(newLoadedContainer(t), nil)
	for _, count := range []int{5, 10, 20, 40} {
		session := IntentSessionContext{TrackCount: count}
		preview, err := api.ParseIntentWithContext(context.Background(), "like Justice, 30 tracks", session)
		if err != nil {
			t.Fatal(err)
		}
		generated, err := api.GenerateFromPromptWithContext(context.Background(), "like Justice, 30 tracks", session)
		if err != nil {
			t.Fatal(err)
		}
		if preview.Intent.Count != count || generated.Request.Intent.Count != count {
			t.Fatalf("control lost: %d, %d", preview.Intent.Count, generated.Request.Intent.Count)
		}
	}
}

func TestTrackCountControl(t *testing.T) {
	original := (core.MusicIntent{Count: 30, OriginalDescription: "30 jazz tracks", Seed: core.NewRNGSeed(42)}).Normalized()
	for _, count := range []int{5, 10, 20, 40} {
		if err := validateTrackCount(count); err != nil {
			t.Fatal(err)
		}
		got := withTrackCount(original, count)
		if got.Count != count || got.Controls.TotalTrackCount != count || !got.TrackCountExplicit {
			t.Fatalf("count %d: %+v", count, got)
		}
		if got.OriginalDescription != original.OriginalDescription || got.Seed != original.Seed {
			t.Fatal("lost original intent")
		}
	}
	if withTrackCount(original, 0).Count != 30 || original.Count != 30 {
		t.Fatal("changed legacy or cached intent")
	}
	for _, count := range []int{-1, 1, 25, 200} {
		if validateTrackCount(count) == nil {
			t.Fatalf("accepted %d", count)
		}
	}
}
