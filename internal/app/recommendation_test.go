package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
)

func TestRecommendationSettingsPersistAndPreserveOtherPrefs(t *testing.T) {
	cfg := testConfig(t)
	prefs := config.Prefs{AnalysisEnabled: true, PreviewProvider: "off", OnboardingDone: true, ModelID: "fixture"}
	if err := prefs.Save(cfg.DataDir); err != nil {
		t.Fatal(err)
	}
	c := &Container{cfg: cfg}
	if c.RecommendationMode() != core.EnhancedHybrid {
		t.Fatal("wrong default")
	}
	for _, mode := range []core.RecommendationMode{core.EnhancedHybrid, core.CLAPFirst, core.DeejAIOnly, core.AcousticBrainzFirst} {
		if err := c.SetRecommendationMode(mode); err != nil {
			t.Fatal(err)
		}
		saved := config.LoadPrefs(cfg.DataDir)
		if saved.RecommendationMode != string(mode) || !saved.AnalysisEnabled || !saved.OnboardingDone || saved.PreviewProvider != "off" || saved.ModelID != "fixture" {
			t.Fatalf("preferences lost: %+v", saved)
		}
		reloaded := &Container{cfg: cfg, recommendationMode: core.RecommendationMode(saved.RecommendationMode)}
		if reloaded.RecommendationMode() != mode {
			t.Fatal("restart lost mode")
		}
	}
	for _, invalid := range []core.RecommendationMode{"", "unknown"} {
		if err := c.SetRecommendationMode(invalid); err == nil || c.RecommendationMode() != core.AcousticBrainzFirst {
			t.Fatal("invalid mode applied")
		}
	}
	// A failed disk write must not falsely report a live setting change.
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	c.cfg.DataDir = path
	if err := c.SetRecommendationMode(core.CLAPFirst); err == nil || c.RecommendationMode() != core.AcousticBrainzFirst {
		t.Fatal("failed save changed mode")
	}
}

func TestRecommendationModeDefaultAndExplicitConfiguration(t *testing.T) {
	for _, saved := range []core.RecommendationMode{"", "unknown"} {
		c := &Container{cfg: testConfig(t), recommendationMode: saved}
		if c.RecommendationMode() != core.EnhancedHybrid {
			t.Fatal("unset or invalid preference did not use Enhanced Hybrid")
		}
		c.cfg.Recommendation.Strategy = config.RecommendationDeejAI
		if c.RecommendationMode() != core.DeejAIOnly {
			t.Fatal("explicit engine-only configuration lost")
		}
	}
}

func TestRecommendationSettingsConcurrentReadsAndWrites(t *testing.T) {
	c := &Container{cfg: testConfig(t)}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.SetOnboarded(); err != nil {
				t.Error(err)
			}
			if err := c.SetRecommendationMode(core.CLAPFirst); err != nil {
				t.Error(err)
			}
			if !c.RecommendationMode().Valid() {
				t.Error("invalid snapshot")
			}
		}()
	}
	wg.Wait()
	saved := config.LoadPrefs(c.cfg.DataDir)
	if !saved.OnboardingDone || saved.RecommendationMode != string(core.CLAPFirst) {
		t.Fatal("concurrent settings clobbered one another")
	}
}

func TestStartupMigratesOnlyCurrentRecommendationPreference(t *testing.T) {
	for _, legacy := range []core.RecommendationMode{core.CLAPFirst, core.AcousticBrainzFirst} {
		t.Run(string(legacy), func(t *testing.T) {
			cfg := testConfig(t)
			prefs := config.Prefs{RecommendationMode: string(legacy), OnboardingDone: true, ModelDisabled: true, PreviewProvider: "off", DebugLogging: true}
			if err := prefs.Save(cfg.DataDir); err != nil {
				t.Fatal(err)
			}
			c, err := New(context.Background(), cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			if c.RecommendationMode() != core.EnhancedHybrid {
				t.Fatal("legacy current mode remains active")
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			got, err := config.LoadPrefsChecked(cfg.DataDir)
			if err != nil || got.RecommendationMode != string(core.EnhancedHybrid) || !got.OnboardingDone || !got.ModelDisabled || got.PreviewProvider != "off" || !got.DebugLogging {
				t.Fatalf("migration lost settings: %+v %v", got, err)
			}
			before, err := os.ReadFile(filepath.Join(cfg.DataDir, "prefs.json"))
			if err != nil {
				t.Fatal(err)
			}
			c, err = New(context.Background(), cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(filepath.Join(cfg.DataDir, "prefs.json"))
			if err != nil || string(after) != string(before) {
				t.Fatal("restart changed migrated preferences", err)
			}
			if !legacy.Valid() {
				t.Fatal("historical mode contract was removed")
			}
		})
	}
}
