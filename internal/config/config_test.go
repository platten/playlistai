package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultValidates(t *testing.T) {
	t.Parallel()
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
}

func TestLoadMissingFileReturnsDefault(t *testing.T) {
	t.Parallel()
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if cfg.Preview.Provider != PreviewDeezer {
		t.Fatalf("want default provider, got %q", cfg.Preview.Provider)
	}
}

func TestLoadOverlaysAndValidates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
data_dir = "/tmp/pai"

[preview]
provider = "off"

[ai]
n_ctx = 8192

[enrich]
min_score = 70

[recommendation]
seed_audio_budget = 17
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DataDir != "/tmp/pai" || cfg.Preview.Provider != PreviewOff || cfg.AI.NCtx != 8192 || cfg.Enrich.MinScore != 70 {
		t.Fatalf("overlay not applied: %+v", cfg)
	}
	if cfg.Recommendation.SeedAudioBudget != 17 || cfg.Recommendation.Strategy != RecommendationMultichannel {
		t.Fatalf("recommendation overlay not applied: %+v", cfg.Recommendation)
	}
	// Untouched keys keep defaults.
	if cfg.Enrich.UserAgent == "" {
		t.Fatal("default user agent lost")
	}
}

func TestValidateRejectsBadRecommendationConfig(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Recommendation.ExplorationChance = 1.1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid exploration chance to fail")
	}
	cfg = Default()
	cfg.Recommendation.Strategy = "unknown"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unknown recommendation strategy to fail")
	}
}

func TestLoadRejectsBadProvider(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[preview]\nprovider = \"pandora\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for unknown provider")
	}
}

func TestPrefsRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if p := LoadPrefs(dir); p != (Prefs{}) {
		t.Fatalf("empty dir should load zero prefs, got %+v", p)
	}

	want := Prefs{ModelPath: "/models/llama.gguf", ModelID: "llama-3.2-3b-instruct-q4km"}
	if err := want.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := LoadPrefs(dir); got != want {
		t.Fatalf("LoadPrefs = %+v, want %+v", got, want)
	}

	// clearing
	if err := (Prefs{}).Save(dir); err != nil {
		t.Fatal(err)
	}
	if got := LoadPrefs(dir); got != (Prefs{}) {
		t.Fatalf("after clear: %+v", got)
	}
}

func TestLoadPrefsToleratesGarbage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "prefs.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := LoadPrefs(dir); p != (Prefs{}) {
		t.Fatalf("garbage prefs should load zero value, got %+v", p)
	}
}

func TestLLMReady(t *testing.T) {
	t.Parallel()
	cfg := Default()
	if cfg.LLMReady() {
		t.Fatal("no model path => not ready")
	}
	f := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(f, []byte("not really a model but non-empty"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.AI.ModelPath = f
	if !cfg.LLMReady() {
		t.Fatal("existing non-empty file => ready")
	}
}
