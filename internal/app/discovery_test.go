package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
)

func TestDiscoveryUnpublishedBuildDoesNotRequireAsset(t *testing.T) {
	c := &Container{cfg: config.Config{DataDir: t.TempDir()}}
	t.Cleanup(func() { _ = c.Close() })
	s, err := c.GetDiscoveryAssetStatus()
	if err != nil || s.Configured || s.Installed {
		t.Fatalf("status=%+v err=%v", s, err)
	}
	if _, err := c.InstallDiscoveryAsset(context.Background(), nil); err == nil {
		t.Fatal("unpublished source accepted")
	}
}

func TestDefaultDiscoveryReadinessAcceptsLocalOverride(t *testing.T) {
	for _, onboarded := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "upgrade"}[onboarded], func(t *testing.T) {
			cfg := config.Default()
			cfg.DataDir = t.TempDir()
			cfg.Catalog.Dir = filepath.Join(cfg.DataDir, "catalog")
			c := &Container{cfg: cfg}
			t.Cleanup(func() { _ = c.Close() })
			if err := (config.Prefs{OnboardingDone: onboarded}).Save(cfg.DataDir); err != nil {
				t.Fatal(err)
			}
			before, err := c.SetupReadiness()
			if err != nil {
				t.Fatal(err)
			}
			if !before.Discovery.Supported || !before.Discovery.Required || before.Discovery.Ready {
				t.Fatalf("missing default asset not required: %+v", before.Discovery)
			}
			pack := filepath.Join(t.TempDir(), "override.paipack")
			writeAppLibraryPack(t, pack, "shared-override", "track", "Artist/Track.flac")
			status, err := c.ImportDiscoveryAsset(context.Background(), pack, nil)
			if err != nil || !status.Installed || status.Source != "local" {
				t.Fatalf("local import status=%+v err=%v", status, err)
			}
			after, err := c.SetupReadiness()
			if err != nil || !after.Discovery.Ready {
				t.Fatalf("local override not ready: %+v %v", after.Discovery, err)
			}
			manager, err := c.discoveryManager()
			if err != nil {
				t.Fatal(err)
			}
			packs, release, err := manager.Pin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if len(packs) == 0 {
				t.Fatal("local override unavailable to recommendation pin")
			}
			tracks, err := packs[0].ArtistRecordings(context.Background(), "Local Artist")
			if err != nil || len(tracks) != 1 || !strings.HasPrefix(tracks[0].ID, "pack:") {
				t.Fatalf("override not a shared catalog: %+v %v", tracks, err)
			}
			personal, err := c.LocalLibraryStatus()
			if err != nil || personal.Installed {
				t.Fatalf("shared override changed personal library: %+v %v", personal, err)
			}
		})
	}
}

func TestDiscoveryConfiguredSourceRequiresUpgradeRepair(t *testing.T) {
	c := &Container{cfg: config.Config{DataDir: t.TempDir(), Discovery: config.DiscoveryConfig{ManifestURL: "https://example.invalid/discovery.json"}}}
	t.Cleanup(func() { _ = c.Close() })
	s, err := c.GetDiscoveryAssetStatus()
	if err != nil || !s.Configured || s.Installed {
		t.Fatalf("status=%+v err=%v", s, err)
	}
	for _, onboarded := range []bool{false, true} {
		r := SetupReadiness{Onboarded: onboarded, Discovery: SetupCapability{Supported: s.Configured, Required: s.Configured, Ready: s.Installed}}
		if got := strings.Join(r.CompletionSteps(), ","); got != "discovery" {
			t.Fatalf("onboarded=%v missing=%s", onboarded, got)
		}
	}
}

func TestDiscoveryMissingAssetGuardsOnlyEnhancedHybrid(t *testing.T) {
	c := &Container{cfg: config.Config{DataDir: t.TempDir(), Discovery: config.DiscoveryConfig{ManifestURL: "https://example.invalid/discovery.json"}}}
	t.Cleanup(func() { _ = c.Close() })
	for _, mode := range []core.RecommendationMode{core.EnhancedHybrid, core.DeejAIOnly, core.CLAPFirst, core.AcousticBrainzFirst} {
		intent := core.MusicIntent{}
		intent.Controls.RecommendationMode = mode
		_, err := c.pinDiscoveryRecommendationOverlay(context.Background(), intent, nil, nil, nil)
		if mode == core.EnhancedHybrid {
			if err == nil || !strings.Contains(err.Error(), "discovery data is required") {
				t.Fatalf("mode=%s err=%v", mode, err)
			}
		} else if err != nil {
			t.Fatalf("legacy mode %s changed: %v", mode, err)
		}
	}
}
