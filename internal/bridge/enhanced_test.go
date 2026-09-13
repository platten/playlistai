package bridge

import (
	"context"
	"errors"
	"testing"
)

func TestInstallRecommendedMERTCanceledBeforeDownload(t *testing.T) {
	t.Parallel()
	a := New(newTestContainer(t), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.InstallRecommendedMERT(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled install: %v", err)
	}
	s, err := a.GetEnhancedAnalysisStatus(context.Background())
	if err != nil || s.Installed || s.Enabled {
		t.Fatalf("canceled install changed status: %+v %v", s, err)
	}
}
