package bridge

import (
	"context"
	"errors"
	"testing"
)

func TestSeparateMERTAndDSPSettingsCancelStaleRecommendationWork(t *testing.T) {
	for _, action := range []string{"mert setting", "dsp setting", "mert cache", "dsp cache"} {
		t.Run(action, func(t *testing.T) {
			api := New(newLoadedContainer(t), nil)
			var contexts []context.Context
			for _, operation := range []string{"intent-preview", generationOperation, "enhanced-analysis"} {
				ctx, _, finish := api.operations.begin(context.Background(), operation)
				defer finish()
				contexts = append(contexts, ctx)
			}
			var err error
			switch action {
			case "mert setting":
				err = api.SetMERTSimilarityEnabled(false)
			case "dsp setting":
				err = api.SetEnhancedAnalysisEnabled(false)
			case "mert cache":
				err = api.ClearMERTSimilarityCache(context.Background())
			case "dsp cache":
				err = api.ClearDSPAnalysisCache(context.Background())
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, ctx := range contexts {
				if !errors.Is(ctx.Err(), context.Canceled) {
					t.Fatal("settings change left stale work running")
				}
			}
		})
	}
}
