package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/llama"
	"github.com/platten/playlistai/internal/intent/modelmgr"
	"github.com/platten/playlistai/internal/ports"
)

func TestServiceEdgesAnalysisManifestFailures(t *testing.T) {
	c := &Container{}
	for _, tc := range []struct{ name, data string }{
		{"malformed", "{"}, {"invalid", "{}"}, {"oversized", strings.Repeat(" ", (1<<20)+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bundle.json")
			if err := os.WriteFile(path, []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := c.InspectAnalysisBundle(path); err == nil {
				t.Fatal("invalid manifest accepted")
			}
			if err := c.InstallAnalysisBundle(context.Background(), path, nil); err == nil {
				t.Fatal("invalid manifest installed")
			}
		})
	}
	if _, err := c.InspectAnalysisBundle(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing manifest: %v", err)
	}
	if err := c.installAnalysisManifest(context.Background(), audio.BundleManifest{}, nil); err == nil || !strings.Contains(err.Error(), "storage unavailable") {
		t.Fatalf("missing storage: %v", err)
	}
	if err := c.ClearAnalysis(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestServiceEdgesCorruptPreferencesPreserveAnalysisAndModel(t *testing.T) {
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	old := &managedFixture{}
	c.llama, c.parser, c.modelPath, c.modelID = old, old, "original.gguf", "original"
	c.analysis.enabled = true
	prefsPath := filepath.Join(c.cfg.DataDir, "prefs.json")
	bad := []byte("{broken prefs")
	if err := os.WriteFile(prefsPath, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func() error{func() error { return c.SetAnalysisEnabled(false) }, c.RemoveAnalysisModel, c.ClearModel} {
		if err := change(); err == nil {
			t.Fatal("corrupt preferences silently replaced")
		}
		if !c.analysis.enabled || c.IntentParser() != old || old.closed.Load() != 0 {
			t.Fatal("failed persistence changed live services")
		}
	}
	replacement := &managedFixture{}
	c.modelFactory = func(context.Context, llama.Options) (managedParser, error) { return replacement, nil }
	if err := c.SetModel(context.Background(), modelFixture(t), "replacement"); err == nil {
		t.Fatal("model committed despite corrupt preferences")
	}
	if replacement.closed.Load() != 1 || c.IntentParser() != old {
		t.Fatal("failed commit leaked replacement or displaced original")
	}
	got, err := os.ReadFile(prefsPath)
	if err != nil || string(got) != string(bad) {
		t.Fatalf("corruption overwritten: %q %v", got, err)
	}
}

func TestServiceEdgesDownloadFailuresDoNotStartOrPersistModel(t *testing.T) {
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	original := c.IntentParser()
	failure := errors.New("offline download failure")
	calls := 0
	c.modelDownloader = func(ctx context.Context, m modelmgr.Model, dir string, _ ports.Progress) (string, error) {
		calls++
		if ctx.Err() != nil || m.ID != modelmgr.Catalog()[0].ID || dir != filepath.Join(c.cfg.DataDir, "models") {
			t.Fatal("incorrect download inputs")
		}
		return "", failure
	}
	c.modelFactory = func(context.Context, llama.Options) (managedParser, error) {
		t.Fatal("failed download started runtime")
		return nil, failure
	}
	if err := c.DownloadModel(context.Background(), "missing-model", nil); err == nil {
		t.Fatal("unknown model accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.DownloadModel(ctx, modelmgr.Catalog()[0].ID, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if calls != 0 {
		t.Fatal("unknown/canceled request downloaded")
	}
	if err := c.DownloadModel(context.Background(), modelmgr.Catalog()[0].ID, nil); !errors.Is(err, failure) {
		t.Fatalf("download failure: %v", err)
	}
	prefs, err := config.LoadPrefsChecked(c.cfg.DataDir)
	if err != nil || calls != 1 || prefs.ModelPath != "" || c.IntentParser() != original {
		t.Fatal("download failure changed selected model", err)
	}
	if _, err := c.ProposeAnchors(context.Background(), core.MusicIntent{}, nil); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("rules proposed unsupported anchors: %v", err)
	}
}

func TestServiceEdgesAnalysisStorageCancellation(t *testing.T) {
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.GetAnalysisStatus(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled usage query: %v", err)
	}
	if err := c.ClearAnalysis(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled clear: %v", err)
	}
	if _, err := c.GetAnalysisStatus(context.Background()); err != nil {
		t.Fatalf("canceled query damaged store: %v", err)
	}
	if err := c.loadAnalysis(context.Background()); err == nil {
		t.Fatal("missing active bundle loaded")
	}
}

func TestServiceEdgesInstallerProgressFiltering(t *testing.T) {
	for input, want := range map[string]string{"": "", "\rspinner": "", "37%": "", "ready": "ready", "downloading runtime": "downloading runtime"} {
		if got := trimLine(input); got != want {
			t.Fatalf("trimLine(%q)=%q, want %q", input, got, want)
		}
	}
}

func TestServiceEdgesRuntimeDiscoveryDoesNotImplyUsableHardware(t *testing.T) {
	cfg := testConfig(t)
	cfg.AI.LlamaServerPath = filepath.Join(cfg.DataDir, "missing-runtime")
	c, err := New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if status, builds := c.LlamaRuntime(); status.Available || len(builds) != 0 {
		t.Fatal("missing configured runtime reported installed")
	}
	// These marker files exercise discovery only. The canceled probe must never
	// execute them; they do not emulate an inference binary or native ABI.
	if err := os.WriteFile(cfg.AI.LlamaServerPath, []byte("discovery marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, builds := c.LlamaRuntime(); !status.Available || status.Source != "detected" || status.Path != cfg.AI.LlamaServerPath || len(builds) != 0 {
		t.Fatalf("explicit runtime: %+v %v", status, builds)
	}
	if err := os.MkdirAll(c.llamaStageDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"llama-primary", "llama-cpu"} {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if err := os.WriteFile(filepath.Join(c.llamaStageDir(), name), []byte("discovery marker"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if status, builds := c.LlamaRuntime(); !status.Available || status.Source != "staged" || len(builds) != 2 || builds[0] != "gpu" || builds[1] != "cpu" {
		t.Fatalf("staged priority: %+v %v", status, builds)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if device, available := c.LlamaHardware(ctx); available || device.TotalBytes != 0 {
		t.Fatalf("canceled probe invented hardware: %+v", device)
	}
	if c.ModelVRAMReserve() <= 0 {
		t.Fatal("model fitting omitted runtime reserve")
	}
}

func TestServiceEdgesRecommendedAnalysisRequiresLocalStorage(t *testing.T) {
	c := &Container{}
	if err := c.InstallRecommendedAnalysisBundle(context.Background(), nil); err == nil {
		t.Fatal("recommended model installed without local storage")
	}
}
