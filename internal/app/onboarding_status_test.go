package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/llama"
	"github.com/platten/playlistai/internal/intent/nlu"
	"github.com/platten/playlistai/internal/preview/deezer"
)

func setupWriteFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func setupRead(t *testing.T, c *Container) SetupReadiness {
	t.Helper()
	status, err := c.SetupReadiness()
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func TestSetupReadinessUsesSelectedFilesDuringAsyncStartup(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.AI.ModelPath = modelFixture(t)
	cfg.AI.LlamaServerPath = filepath.Join(t.TempDir(), "llama-server.exe")
	// This is deliberately not executable code. A status read must only stat it.
	setupWriteFile(t, cfg.AI.LlamaServerPath, "inert runtime fixture")
	c := &Container{cfg: cfg}
	if status := setupRead(t, c); !status.Model.Ready || !status.Model.Required {
		t.Fatalf("selected local files mistaken for an incomplete async startup: %+v", status.Model)
	}
	if c.modelPath != "" || c.llama != nil || c.parser != nil {
		t.Fatal("status started or published a parser")
	}
	if err := os.Remove(cfg.AI.LlamaServerPath); err != nil {
		t.Fatal(err)
	}
	if status := setupRead(t, c); status.Model.Ready || !status.Model.Required {
		t.Fatalf("missing selected runtime: %+v", status.Model)
	}
	setupWriteFile(t, cfg.AI.LlamaServerPath, "inert runtime fixture")
	setupWriteFile(t, cfg.AI.ModelPath, "not GGUF")
	if status := setupRead(t, c); status.Model.Ready {
		t.Fatal("invalid GGUF treated as installed")
	}
}

func TestSetupReadinessRulesChoiceSurvivesRestartAndModelSelection(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.AI.ModelPath = filepath.Join(t.TempDir(), "configured-but-missing.gguf")
	c, err := New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !setupRead(t, c).Model.Required {
		t.Fatal("legacy configured model lost without an explicit rules choice")
	}
	if err := c.ClearModel(); err != nil {
		t.Fatal(err)
	}
	if err := c.SetOnboarded(); err != nil {
		t.Fatal(err)
	}
	if setupRead(t, c).Model.Required {
		t.Fatal("cleared TOML selection returned in same process")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c, err = New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if setupRead(t, c).Model.Required || c.Config().AI.ModelPath != "" || c.IntentParser().Info().Backend != "rules" {
		t.Fatal("restart resurrected a disabled TOML model")
	}
	prefs, err := config.LoadPrefsChecked(cfg.DataDir)
	if err != nil || !prefs.ModelDisabled || !prefs.OnboardingDone {
		t.Fatal("unrelated preferences save lost explicit rules choice", prefs, err)
	}
	c.modelFactory = func(context.Context, llama.Options) (managedParser, error) { return &managedFixture{}, nil }
	path := modelFixture(t)
	if err := c.SetModel(context.Background(), path, "fixture"); err != nil {
		t.Fatal(err)
	}
	prefs, err = config.LoadPrefsChecked(cfg.DataDir)
	if err != nil || prefs.ModelDisabled || prefs.ModelPath != path || !prefs.OnboardingDone || !setupRead(t, c).Model.Required {
		t.Fatal("successful model selection did not supersede rules choice", prefs, err)
	}
}

func TestSetupReadinessOptionalPoliciesAndPreviewOff(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	c := &Container{cfg: cfg, previewName: config.PreviewOff}
	status := setupRead(t, c)
	if status.Catalog.Required || status.Catalog.Supported || status.Metadata.Required || status.Intent.Required || status.Analysis.Required || !status.Preview.Ready {
		t.Fatalf("never-installed defaults or intentional preview off: %+v", status)
	}
	setupWriteFile(t, filepath.Join(cfg.DataDir, "metadata", "active"), "malformed prior pointer")
	setupWriteFile(t, filepath.Join(cfg.DataDir, "music-analysis", "active.json"), "missing-bundle")
	status = setupRead(t, c)
	if status.Metadata.Required || !status.Analysis.Required || status.Analysis.Ready {
		t.Fatalf("metadata cannot establish compatibility before catalog; analysis marker needs repair: %+v", status)
	}
	cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "a", Display: "Artist - Song"})
	c.runtime = RuntimeSnapshot{Catalog: cat, Resolver: cat}
	status = setupRead(t, c)
	if !status.Catalog.Ready || !status.Metadata.Required || status.Metadata.Ready {
		t.Fatalf("broken prior metadata not identified with loaded catalog: %+v", status)
	}
	prefs := config.Prefs{OnboardingDone: true, IntentAssistEnabled: true, IntentExtractorDir: filepath.Join(t.TempDir(), "missing-extractor"), AnalysisEnabled: true}
	if err := prefs.Save(cfg.DataDir); err != nil {
		t.Fatal(err)
	}
	status = setupRead(t, c)
	if !status.Intent.Required || status.Intent.Ready || !status.Analysis.Required || status.Intent.Supported != (nlu.CheckPackagedRuntime() == nil) {
		t.Fatalf("configured optional capabilities not assessed: %+v", status)
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "intent-nlu")); !os.IsNotExist(err) {
		t.Fatal("status created an asset directory", err)
	}
}

func TestSetupReadinessHealthyCustomAnalysisDoesNotRequireGeneralFit(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	// The install directory includes a content fingerprint, unlike manifest.ID.
	version := "custom-0123456789abcdef"
	dir := filepath.Join(cfg.DataDir, "music-analysis", version)
	setupWriteFile(t, filepath.Join(dir, "worker.exe"), "worker")
	manifest := audio.BundleManifest{Version: 1, ID: "custom", Platform: runtime.GOOS + "/" + runtime.GOARCH, Model: core.AudioModelIdentity{Preprocessing: audio.PreprocessingVersion}, Artifacts: []audio.BundleArtifact{{Role: "worker", Name: "worker.exe", Size: 6}}}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	setupWriteFile(t, filepath.Join(dir, "bundle.json"), string(manifestJSON))
	setupWriteFile(t, filepath.Join(cfg.DataDir, "music-analysis", "active.json"), version)
	worker := &audio.Worker{Model: manifest.Model, BundleDir: dir}
	c := &Container{cfg: cfg, analysis: analysisState{
		manifest: &manifest, worker: worker,
		service: &audio.Service{Analyzer: worker, Store: &audio.Store{}, Resolver: deezer.New(deezer.Config{}), Authorized: true, ParityValidated: true},
	}}
	status := setupRead(t, c)
	if !status.Analysis.Ready || !status.Analysis.Supported || c.analysis.service.Ready() || c.analysis.enabled {
		t.Fatalf("healthy custom inference must be ready without calibrated fit or opt-in: %+v", status.Analysis)
	}
	if err := os.Remove(filepath.Join(dir, "worker.exe")); err != nil {
		t.Fatal(err)
	}
	status = setupRead(t, c)
	if status.Analysis.Ready || !status.Analysis.Required || !status.Analysis.Supported {
		t.Fatalf("removed installed custom worker: %+v", status.Analysis)
	}
	// A failed restart has no cached healthy manifest or worker. The active
	// v1 descriptor must still offer repair even without a native build.
	c.analysis = analysisState{}
	status = setupRead(t, c)
	if status.Analysis.Ready || !status.Analysis.Required || !status.Analysis.Supported {
		t.Fatalf("failed legacy activation was mistaken for unsupported: %+v", status.Analysis)
	}
}

func TestSetupAnalysisChecksRetainedArchiveAndActivePointer(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	version := "custom-0123456789abcdef"
	dir := filepath.Join(root, version)
	manifest := audio.BundleManifest{ID: "custom", Artifacts: []audio.BundleArtifact{{Role: "runtime", Name: "runtime.zip", Size: 7, ArchiveMember: "lib/runtime.dll", UnpackedSize: 7}}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	setupWriteFile(t, filepath.Join(dir, "bundle.json"), string(raw))
	setupWriteFile(t, filepath.Join(root, "active.json"), version)
	setupWriteFile(t, filepath.Join(dir, "runtime.dll"), "library")
	if setupAnalysisFilesPresent(dir, manifest) {
		t.Fatal("extracted runtime alone survives no restart")
	}
	setupWriteFile(t, filepath.Join(dir, "runtime.zip"), "archive")
	if !setupAnalysisFilesPresent(dir, manifest) {
		t.Fatal("complete artifact set unavailable")
	}
	activeDir, active := setupAnalysisActivation(root)
	if active == nil || activeDir != dir {
		t.Fatal("valid active bundle unavailable")
	}
	if err := os.Remove(filepath.Join(root, "active.json")); err != nil {
		t.Fatal(err)
	}
	if _, active := setupAnalysisActivation(root); active != nil {
		t.Fatal("deleted active pointer hidden by installed files")
	}
	setupWriteFile(t, filepath.Join(root, "active.json"), "../outside")
	if _, active := setupAnalysisActivation(root); active != nil {
		t.Fatal("active pointer escaped installation directory")
	}
}

func TestSetupReadinessCatalogSourcesAndPriorInstall(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"archive", "manifest", "custom directory", "partial catalog"} {
		t.Run(kind, func(t *testing.T) {
			cfg := testConfig(t)
			switch kind {
			case "archive":
				cfg.Catalog.ArchiveURL = "https://example.invalid/catalog"
			case "manifest":
				cfg.Catalog.ManifestURL = "https://example.invalid/manifest"
			case "custom directory":
				cfg.Catalog.Dir = filepath.Join(cfg.DataDir, "selected-catalog")
			case "partial catalog":
				setupWriteFile(t, filepath.Join(cfg.Catalog.Dir, "catalog.sqlite"), "broken")
			}
			status := setupRead(t, &Container{cfg: cfg})
			if status.Catalog.Ready || !status.Catalog.Supported || !status.Catalog.Required {
				t.Fatalf("configured catalog requires repair: %+v", status.Catalog)
			}
		})
	}
}
