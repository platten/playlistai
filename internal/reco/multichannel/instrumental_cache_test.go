package multichannel

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
)

func TestStrictInstrumentalRequestFindsCompatibleCachedRecording(t *testing.T) {
	cat := testCatalog()
	service, _ := cachedAudioService(t, cat, "audio")
	intent, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: "Instrumental, no vocals"})
	if err != nil {
		t.Fatal(err)
	}
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	intent.VerificationPolicy = core.BestAvailable
	session, err := service.Begin(context.Background(), intent, cat.CatalogVersion(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	meta, _ := cat.Meta("audio")
	assessment, err := session.Check(context.Background(), meta.Ref, false)
	if err != nil || !assessment.Eligible {
		t.Fatalf("fixture must pass instrumental checks: %+v %v", assessment, err)
	}
	candidates, err := session.CachedCandidates(context.Background(), cat, 40, nil)
	if err != nil || len(candidates) != 1 || candidates[0].Track.ID != "audio" {
		t.Fatalf("cached eligible instrumental recording not discovered: %+v %v", candidates, err)
	}
}
