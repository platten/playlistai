package audio

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
)

func TestSharedEnhancedHooksRespectGenerationTrackBudget(t *testing.T) {
	preview, _, resolver, _ := testService(t)
	store := preview.Store.(*Store)
	preview.DSPStore = store.DSP()
	fake := &mertFake{model: mertTestModel()}
	preview.MERT = &MERTService{Preview: preview, Analyzer: fake, Store: store.Representations(), ParityValidated: true}
	ctx := WithEnhancedBudget(context.Background(), 24, 2*time.Minute)
	for i := 0; i < 30; i++ {
		ref := core.TrackRef{ID: fmt.Sprint(i), Artist: "Synthetic", Title: fmt.Sprintf("Silence%d", i)}
		r, _, err := preview.AnalyzePreview(ctx, ref, "catalog")
		if err != nil || r.ID == "" {
			t.Fatal("CLAP stopped with optional budget", err)
		}
	}
	usage, err := store.Representations().Usage(ctx)
	if err != nil || usage.Records != 24 {
		t.Fatalf("MERT admission bypass: %+v %v", usage, err)
	}
	dspUsage, err := store.DSP().Usage(ctx)
	if err != nil || dspUsage.Records != 24 {
		t.Fatalf("DSP admission bypass: %+v %v", dspUsage, err)
	}
	if resolver.calls != 30 || fake.calls != 24 || EnhancedBudgetFor(ctx).Used() != 24 {
		t.Fatalf("wrong counts resolver%d MERT%d budget%d", resolver.calls, fake.calls, EnhancedBudgetFor(ctx).Used())
	}
	// Existing compatible enhanced rows do not consume a fresh request's budget.
	fresh := WithEnhancedBudget(context.Background(), 1, time.Minute)
	if _, _, err := preview.AnalyzePreview(fresh, core.TrackRef{ID: "0", Artist: "Synthetic", Title: "Silence0"}, "catalog"); err != nil {
		t.Fatal(err)
	}
	if EnhancedBudgetFor(fresh).Used() != 0 {
		t.Fatal("cache hit consumed admission")
	}
}
func TestExpiredEnhancedBudgetPreservesCLAP(t *testing.T) {
	preview, _, _, _ := testService(t)
	store := preview.Store.(*Store)
	preview.DSPStore = store.DSP()
	fake := &mertFake{model: mertTestModel()}
	preview.MERT = &MERTService{Preview: preview, Analyzer: fake, Store: store.Representations(), ParityValidated: true}
	ctx := WithEnhancedBudget(context.Background(), 24, 0)
	r, _, err := preview.AnalyzePreview(ctx, core.TrackRef{ID: "123", Artist: "Synthetic", Title: "Silence"}, "catalog")
	if err != nil || r.ID == "" || fake.calls != 0 {
		t.Fatal("expired optional budget stopped CLAP", err)
	}
}

type budgetExpiryAnalyzer struct{ mertFake }

func (f *budgetExpiryAnalyzer) EmbedAudio(ctx context.Context, _ []float32) ([]float32, error) {
	f.calls++
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestEnhancedDeadlineDuringInferenceDoesNotCancelCLAP(t *testing.T) {
	preview, _, _, _ := testService(t)
	store := preview.Store.(*Store)
	fake := &budgetExpiryAnalyzer{mertFake: mertFake{model: mertTestModel()}}
	preview.MERT = &MERTService{Preview: preview, Analyzer: fake, Store: store.Representations(), ParityValidated: true}
	ctx := WithEnhancedBudget(context.Background(), 24, time.Second)
	r, _, err := preview.AnalyzePreview(ctx, core.TrackRef{ID: "123", Artist: "Synthetic", Title: "Silence"}, "catalog")
	if err != nil || r.ID == "" || fake.calls != 1 {
		t.Fatal("optional inference deadline canceled CLAP", err)
	}
	usage, err := store.Representations().Usage(context.Background())
	if err != nil || usage.Records != 0 {
		t.Fatal("canceled MERT cached", err)
	}
}
