package bridge

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestTrackCountPreviewAndGeneration(t *testing.T) {
	api := New(newLoadedContainer(t), nil)
	engine := &trackCountRecordingEngine{}
	useRecommendationEngine(api, engine)
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
		if preview.Intent.Count != count || generated.Request.Intent.Count != count || engine.count != count {
			t.Fatalf("control lost: preview=%d request=%d engine=%d", preview.Intent.Count, generated.Request.Intent.Count, engine.count)
		}
	}
}

// This bridge regression verifies count propagation. Full recommendation-pool
// selection is covered in multichannel; running it here makes a DTO check depend
// on search latency under Windows race and whole-program coverage instrumentation.
type trackCountRecordingEngine struct{ count int }

func (e *trackCountRecordingEngine) Build(_ context.Context, intent core.MusicIntent) (core.Playlist, error) {
	e.count = intent.Count
	return core.Playlist{Intent: intent, Seed: intent.Seed, Mode: intent.Mode}, nil
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
