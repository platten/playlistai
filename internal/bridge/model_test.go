package bridge

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/config"
)

func writeFakeGGUF(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, n)
	copy(b, "GGUF")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGetInstalledModels(t *testing.T) {
	t.Parallel()
	c := newTestContainer(t)
	api := New(c, nil)
	dataDir := c.Config().DataDir

	if got := api.GetInstalledModels(); len(got) != 0 {
		t.Fatalf("fresh dir: %+v", got)
	}

	// A curated model and a stray one, both in models/.
	writeFakeGGUF(t, filepath.Join(dataDir, "models", "llama-3.2-3b-instruct-q4km.gguf"), 128)
	writeFakeGGUF(t, filepath.Join(dataDir, "models", "my-tuned-thing.gguf"), 128)
	// A non-.gguf and a directory are ignored.
	writeFakeGGUF(t, filepath.Join(dataDir, "models", "notes.txt"), 10)

	got := api.GetInstalledModels()
	if len(got) != 2 {
		t.Fatalf("want 2 GGUFs, got %d: %+v", len(got), got)
	}

	var curated, stray *InstalledModel
	for i := range got {
		switch got[i].Name {
		case "llama-3.2-3b-instruct-q4km.gguf":
			curated = &got[i]
		case "my-tuned-thing.gguf":
			stray = &got[i]
		}
	}
	if curated == nil || curated.CatalogID != "llama-3.2-3b-instruct-q4km" || curated.Label == "" {
		t.Fatalf("curated entry not tagged: %+v", curated)
	}
	if stray == nil || stray.CatalogID != "" || stray.SizeBytes != 128 {
		t.Fatalf("stray entry wrong: %+v", stray)
	}
	if curated.Active || stray.Active {
		t.Fatal("nothing should be active")
	}
}

func TestGetModelCatalogIncludesVRAMTierPicks(t *testing.T) {
	t.Parallel()
	api := New(newTestContainer(t), nil)
	catalog := api.GetModelCatalog()
	want := map[int]string{
		8:  "qwen3.5-9b-q4km",
		12: "qwen3.5-9b-q4km",
		16: "qwen3.5-9b-q4km",
		24: "qwen3.5-35b-a3b-q4km",
		32: "qwen3.5-35b-a3b-q4km",
	}
	for tier, id := range want {
		var got []string
		for _, model := range catalog {
			for _, modelTier := range model.BestForVRAMGB {
				if modelTier == tier {
					got = append(got, model.ID)
				}
			}
		}
		if len(got) != 1 || got[0] != id {
			t.Fatalf("Settings catalog pick for %d GiB = %v, want %q", tier, got, id)
		}
	}
}

func TestSettingsAndWizardRecommendOnlySmallestInForcedCPUMode(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.Catalog.Dir = filepath.Join(cfg.DataDir, "catalog")
	cfg.Catalog.ArchiveURL = ""
	cfg.Enrich.CachePath = filepath.Join(cfg.DataDir, "metadata.sqlite")
	cfg.AI.GPULayers = -1
	c, err := app.New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	api := New(c, nil)
	catalog := api.GetModelCatalog()
	var smallest ModelInfo
	for _, m := range catalog {
		if m.SizeApprox > 0 && (smallest.ID == "" || m.SizeApprox < smallest.SizeApprox) {
			smallest = m
		}
	}
	for _, m := range catalog {
		if m.Recommended != (m.ID == smallest.ID) {
			t.Fatalf("CPU badge mismatch: %+v", m)
		}
	}
	wizard := api.GetModelRecommendations()
	if wizard.Hardware.Mode != "cpu" || wizard.Hardware.GPUAvailable || len(wizard.Models) != 1 || wizard.Models[0].ID != smallest.ID || !wizard.Models[0].Recommended {
		t.Fatalf("wizard/settings mismatch: %+v", wizard)
	}
}
