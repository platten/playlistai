package multichannel

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func TestEnhancedWorkDeadlineKeepsCompletedLocalMatches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cat, intent := localPriorityFixture()
		intent.Count, intent.Controls.TotalTrackCount = 2, 2
		source := &metadataPriorityStream{block: true}
		engine := New(cat, fakes.NewSimilarityEngine(cat.Catalog), cat, DefaultConfig()).WithCandidateSource(source)
		engine.retriever = &metadataPriorityRetriever{poolRetriever{candidates: candidatesForTracks(refs(cat, "pack:fixture:local:one"))}}
		// Leave one second of acquisition time under the configured policy.
		started := time.Now().Add(-ports.EnhancedWorkLimit + time.Second)
		ctx, cancel := ports.WithGenerationBudgetSince(context.Background(), started)
		defer cancel()
		result, err := engine.Build(ctx, intent)
		if err != nil || len(result.Tracks) != 1 || result.Tracks[0].ID != "pack:fixture:local:one" {
			t.Fatalf("deadline discarded eligible prefix: %v %v", result.Tracks, err)
		}
		if result.Search == nil || result.Search.StopReason != "deadline" || result.Search.Validate() != nil {
			t.Fatal("deadline reason or completed snapshot missing")
		}
		if time.Since(started) >= ports.EnhancedGenerationLimit || ctx.Err() != nil {
			t.Fatal("assembly exceeded generation limit")
		}
	})
}
