package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

type interruptedEnhancedMetadata struct {
	*fakes.Catalog
	cancel             context.CancelFunc
	legacy, contextual int
}

func (c *interruptedEnhancedMetadata) Meta(id string) (core.TrackMeta, bool) {
	c.legacy++
	c.cancel()
	return c.Catalog.Meta(id)
}

func (c *interruptedEnhancedMetadata) MetaContext(ctx context.Context, id string) (core.TrackMeta, bool) {
	c.contextual++
	c.cancel()
	return c.Catalog.Meta(id)
}

func TestEnhancedMetadataCancellationDoesNotPublishSnapshotOrStartAnalysis(t *testing.T) {
	for _, operation := range []string{"feedback", "analysis"} {
		t.Run(operation, func(t *testing.T) {
			c, err := New(context.Background(), testConfig(t), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			base := fakes.NewCatalog(1, fakes.CatalogTrack{ID: "track", Display: "Artist - Song", Audio: []float32{1}, Track: []float32{1}})
			cat := &interruptedEnhancedMetadata{Catalog: base, cancel: cancel}
			c.runtime.Catalog, c.runtime.Resolver = cat, base
			// Identity-only worker; nil reference input and empty cache avoid
			// native inference. Cancellation must precede analysis acquisition.
			model := core.AudioRepresentationIdentity{Model: "fixture", Revision: "1", Preprocessing: "fixture/v1", Runtime: "fixture/v1", Dimension: 1, WeightsSHA256: strings.Repeat("a", 64), Pooling: "mean-l2/v1"}
			c.enhanced.enabled = false
			c.enhanced.mertEnabled = true
			c.enhanced.worker = &audio.MERTWorker{Model: model}
			c.enhanced.manifest = &audio.MERTBundleManifest{Model: model}
			c.Feedback = &refreshFeedbackFixture{FeedbackStore: c.Feedback, events: []core.FeedbackEvent{{ID: "1", TrackID: "track", Type: core.FeedbackLike, Scope: core.FeedbackScopeDurable, OccurredAt: time.Unix(1, 0)}}}
			if operation == "feedback" {
				var snapshot *core.EnhancedAudioSnapshot
				snapshot, err = c.PrepareEnhancedAudio(ctx, core.MusicIntent{Controls: core.IntentControls{RecommendationMode: core.EnhancedHybrid}}, core.TasteProfile{CatalogVersion: base.CatalogVersion(), AsOf: time.Unix(2, 0)}, nil)
				if snapshot != nil {
					t.Error("canceled feedback published a new evidence snapshot")
				}
			} else {
				var report EnhancedAnalysisReport
				report, err = c.AnalyzeEnhancedTracks(ctx, []string{"track"}, false, nil)
				if report.Requested != 0 {
					t.Errorf("interrupted track admitted for analysis: %+v", report)
				}
			}
			if !errors.Is(err, context.Canceled) || cat.legacy != 0 || cat.contextual != 1 {
				t.Fatalf("cancellation not preserved: %v legacy=%d contextual=%d", err, cat.legacy, cat.contextual)
			}
		})
	}
}
