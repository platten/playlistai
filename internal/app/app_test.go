package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.Catalog.Dir = filepath.Join(cfg.DataDir, "catalog")
	cfg.Catalog.ArchiveURL = "" // tests never hit the network for the catalog
	cfg.Enrich.CachePath = filepath.Join(cfg.DataDir, "mb.sqlite")
	return cfg
}

func TestParserDefaultsToRules(t *testing.T) {
	t.Parallel()
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if got := c.IntentParser().Info().Backend; got != "rules" {
		t.Fatalf("parser backend = %q, want rules", got)
	}
}

func TestParseIntentDetailedReportsFallback(t *testing.T) {
	t.Parallel()
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	c.mu.Lock()
	c.parser = &fakes.IntentParser{
		Err: errors.New("model failed"),
		Meta: ports.ParserInfo{
			Name: "test-model", Backend: "llama", Version: "test/v1", Ready: true,
		},
	}
	c.mu.Unlock()

	outcome, err := c.ParseIntentDetailed(context.Background(), ports.IntentInput{Prompt: "like Justice"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.FallbackUsed || outcome.RequestedBackend != "llama" || outcome.Backend != "rules" || outcome.FallbackReason != "parser_error" {
		t.Fatalf("fallback outcome = %+v", outcome)
	}
}

func TestParseIntentDetailedDoesNotFallbackAfterCancellation(t *testing.T) {
	t.Parallel()
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	c.mu.Lock()
	c.parser = &fakes.IntentParser{
		Err: errors.New("model failed"),
		Meta: ports.ParserInfo{
			Name: "test-model", Backend: "llama", Version: "test/v1", Ready: true,
		},
	}
	c.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outcome, err := c.ParseIntentDetailed(ctx, ports.IntentInput{Prompt: "like Justice"}, nil)
	if !errors.Is(err, context.Canceled) || outcome.FallbackUsed {
		t.Fatalf("canceled parse = %+v, %v", outcome, err)
	}
}

func TestParserStaysRulesWhenLlamaBinaryMissing(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	model := filepath.Join(cfg.DataDir, "model.gguf")
	if err := os.WriteFile(model, []byte("not really a model but non-empty"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.AI.ModelPath = model
	cfg.AI.LlamaServerPath = filepath.Join(cfg.DataDir, "definitely-not-llama-server")

	c, err := New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	// The background attempt fails fast (binary not found); give it a moment.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c.IntentParser().Info().Backend == "rules" {
			time.Sleep(200 * time.Millisecond) // ensure it didn't flip afterwards
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := c.IntentParser().Info().Backend; got != "rules" {
		t.Fatalf("parser backend = %q, want rules (llama binary is missing)", got)
	}
}

func TestModelErrorPathsLeaveRulesParser(t *testing.T) {
	t.Parallel()
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if path, id := c.CurrentModel(); path != "" || id != "" {
		t.Fatalf("CurrentModel = %q/%q, want empty", path, id)
	}

	// bad GGUF path → error, parser unchanged
	if err := c.SetModel(context.Background(), filepath.Join(t.TempDir(), "nope.gguf"), "x"); err == nil {
		t.Fatal("SetModel should reject a missing file")
	}
	// unknown catalog id → error
	if err := c.DownloadModel(context.Background(), "not-a-real-model", nil); err == nil {
		t.Fatal("DownloadModel should reject an unknown id")
	}
	// clearing when nothing is set is a no-op that keeps rules
	if err := c.ClearModel(); err != nil {
		t.Fatalf("ClearModel: %v", err)
	}
	if got := c.IntentParser().Info().Backend; got != "rules" {
		t.Fatalf("parser backend = %q, want rules", got)
	}
}

func TestPrefsOverrideConfigModelPath(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	// a real (dummy) GGUF so LLMReady() passes and the overlay is exercised
	model := filepath.Join(cfg.DataDir, "chosen.gguf")
	if err := os.WriteFile(model, append([]byte("GGUF"), make([]byte, 32)...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (config.Prefs{ModelPath: model, ModelID: "chosen"}).Save(cfg.DataDir); err != nil {
		t.Fatal(err)
	}
	cfg.AI.LlamaServerPath = filepath.Join(cfg.DataDir, "no-such-llama-server")

	c, err := New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	// The background llama start fails (no binary); parser stays rules.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && c.IntentParser().Info().Backend != "rules" {
		time.Sleep(50 * time.Millisecond)
	}
	if got := c.IntentParser().Info().Backend; got != "rules" {
		t.Fatalf("parser = %q, want rules (llama binary missing)", got)
	}
}

func TestNewCreatesDataDirAndIsNotReady(t *testing.T) {
	t.Parallel()
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if c.Ready() {
		t.Fatal("skeleton container should not report Ready (no ports wired)")
	}
	if c.Config().DataDir == "" {
		t.Fatal("config not retained")
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	bad := testConfig(t)
	bad.Preview.Provider = "napster"
	if _, err := New(context.Background(), bad, nil); err == nil {
		t.Fatal("expected error for invalid config")
	}
}

func TestLoadCatalogFromFixture(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.Catalog.Dir = filepath.Join("..", "catalog", "testdata")

	c, err := New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if c.Catalog == nil {
		t.Fatal("catalog should have loaded from the fixture dir")
	}
	if c.Catalog.Len() != 256 {
		t.Fatalf("catalog Len = %d, want 256", c.Catalog.Len())
	}
	if c.Sim == nil || c.Sim.Len() != 256 {
		t.Fatalf("similarity engine not wired to the loaded catalog")
	}
	if c.Reco == nil {
		t.Fatal("recommendation engine not wired to the loaded catalog")
	}
	if !c.Ready() {
		t.Fatal("Ready() should be true once catalog + sim + reco are wired")
	}
}

func TestEnsureCatalogWithoutManifest(t *testing.T) {
	t.Parallel()
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if err := c.EnsureCatalog(context.Background(), nil); err == nil {
		t.Fatal("EnsureCatalog should error when no catalog source is configured")
	}
}

func TestCloseRunsClosersLIFOAndReturnsFirstError(t *testing.T) {
	t.Parallel()
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var order []int
	sentinel := errors.New("boom")
	c.RegisterCloser(func() error { order = append(order, 1); return nil })
	c.RegisterCloser(func() error { order = append(order, 2); return sentinel })
	c.RegisterCloser(func() error { order = append(order, 3); return nil })

	if err := c.Close(); !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}
	if len(order) != 3 || order[0] != 3 || order[2] != 1 {
		t.Fatalf("closers not LIFO: %v", order)
	}
}

func TestSetPreviewProviderSwapsAndPersists(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.Preview.Provider = config.PreviewDeezer
	c, err := New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if got := c.PreviewProviderName(); got != config.PreviewDeezer {
		t.Fatalf("initial provider = %q, want deezer", got)
	}
	if c.PreviewProvider() == nil {
		t.Fatal("deezer provider should be non-nil")
	}

	if err := c.SetPreviewProvider(config.PreviewOff); err != nil {
		t.Fatalf("SetPreviewProvider: %v", err)
	}
	if got := c.PreviewProviderName(); got != config.PreviewOff {
		t.Fatalf("provider = %q, want off", got)
	}
	if c.PreviewProvider() != nil {
		t.Fatal("off should leave the provider nil")
	}

	if err := c.SetPreviewProvider("napster"); err == nil {
		t.Fatal("expected an error for an unknown provider")
	}

	// Persisted: a fresh container in the same DataDir picks it up.
	c2, err := New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("New (reload): %v", err)
	}
	defer func() { _ = c2.Close() }()
	if got := c2.PreviewProviderName(); got != config.PreviewOff {
		t.Fatalf("reloaded provider = %q, want off (persisted)", got)
	}
}

func TestOnboardingDefaultsFalseThenPersists(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	c, err := New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if c.Onboarded() {
		t.Fatal("a fresh data dir should not be onboarded")
	}
	if err := c.SetOnboarded(); err != nil {
		t.Fatalf("SetOnboarded: %v", err)
	}
	if !c.Onboarded() {
		t.Fatal("Onboarded should be true after SetOnboarded")
	}
}

// A real latent bug this guards against: Prefs.Save replaces the whole file,
// so setting the model must not erase a previously-saved preview provider (or
// the onboarding flag), and vice versa.
func TestPrefFieldsDoNotClobberEachOther(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	c, err := New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if err := c.SetPreviewProvider(config.PreviewOff); err != nil {
		t.Fatal(err)
	}
	if err := c.SetOnboarded(); err != nil {
		t.Fatal(err)
	}
	if err := c.ClearModel(); err != nil { // exercises the model-prefs save path
		t.Fatal(err)
	}

	got := config.LoadPrefs(cfg.DataDir)
	if got.PreviewProvider != config.PreviewOff {
		t.Fatalf("ClearModel clobbered PreviewProvider: %+v", got)
	}
	if !got.OnboardingDone {
		t.Fatalf("ClearModel clobbered OnboardingDone: %+v", got)
	}
}
