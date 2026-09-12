package app

import (
	"context"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
)

func TestEnhancedAnalysisOptionalPreferencesAndResponsiveStatus(t *testing.T) {
	ctx := context.Background()
	c, err := New(ctx, testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := c.GetEnhancedAnalysisStatus(ctx)
	if err != nil || s.Enabled || !s.DSPAvailable || s.Installed || s.MERTAvailable {
		t.Fatalf("optional status %+v %v", s, err)
	}
	if err := c.SetRecommendationMode(core.EnhancedHybrid); err != nil {
		t.Fatal(err)
	}
	if err := c.SetEnhancedAnalysisEnabled(true); err != nil {
		t.Fatal(err)
	}
	prefs := config.LoadPrefs(c.cfg.DataDir)
	if !prefs.EnhancedAudioEnabled || prefs.RecommendationMode != string(core.EnhancedHybrid) {
		t.Fatal("preferences lost")
	}
	c.enhanced.opMu.Lock()
	done := make(chan error, 1)
	go func() { _, err := c.GetEnhancedAnalysisStatus(ctx); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("status blocked by ongoing analysis")
	}
	c.enhanced.opMu.Unlock()
	if err := c.ClearEnhancedAnalysis(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveMERT(); err != nil {
		t.Fatal(err)
	}
	if err := c.SetEnhancedAnalysisEnabled(false); err != nil {
		t.Fatal(err)
	}
	s, _ = c.GetEnhancedAnalysisStatus(ctx)
	if s.Enabled || !s.DSPAvailable {
		t.Fatal("remove disabled DSP capability")
	}
	if snapshot, err := c.PrepareEnhancedAudio(ctx, core.MusicIntent{}, core.TasteProfile{}, nil); err != nil || snapshot != nil {
		t.Fatal("legacy mode consumed enhanced evidence")
	}
}
