package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/llama"
	"github.com/platten/playlistai/internal/ports"
)

func TestCloseCancelsWorkBeforeClosingResources(t *testing.T) {
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, release := c.OperationContext(context.Background())
	var closed atomic.Int32
	c.RegisterCloser(func() error { closed.Add(1); return nil })
	done := make(chan error, 1)
	go func() { done <- c.Close() }()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel work")
	}
	if closed.Load() != 0 {
		t.Fatal("resources closed while a lease was held")
	}
	release()
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil || closed.Load() != 1 {
		t.Fatal("close is not idempotent", err)
	}
	c.RegisterCloser(func() error { closed.Add(1); return nil })
	if closed.Load() != 2 {
		t.Fatal("late resource registration leaked")
	}
	late, finish := c.OperationContext(context.Background())
	defer finish()
	if late.Err() != context.Canceled || c.Ready() {
		t.Fatal("closed application accepted work")
	}
	if err := c.EnsureCatalog(context.Background(), nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := c.LoadCatalog(); err == nil {
		t.Fatal("closed application loaded a catalog")
	}
}

func TestCatalogPublicationIsCompleteAndConcurrentLoadIsIdempotent(t *testing.T) {
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	c.cfg.Catalog.Dir = filepath.Join("..", "catalog", "testdata")
	initialClosers := len(c.closers)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := c.LoadCatalog(); err != nil {
				t.Error(err)
			}
		})
	}
	for range 1000 {
		runtime := c.Runtime()
		if runtime.Catalog != nil && (runtime.Resolver == nil || runtime.Sim == nil || runtime.Reco == nil || runtime.BaselineReco == nil) {
			t.Fatal("partially wired runtime was published")
		}
	}
	wg.Wait()
	if !c.Ready() || len(c.closers) != initialClosers+1 {
		t.Fatal("catalog opened repeatedly or never became ready")
	}
}

type managedFixture struct {
	fakes.IntentParser
	closed atomic.Int32
}

func (p *managedFixture) Close() error { p.closed.Add(1); return nil }

func modelFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.gguf")
	if err := os.WriteFile(path, []byte("GGUF0000"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSlowModelCannotUndoClear(t *testing.T) {
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	started, finish := make(chan struct{}), make(chan struct{})
	model := &managedFixture{IntentParser: fakes.IntentParser{Meta: ports.ParserInfo{Backend: "fixture"}}}
	c.modelFactory = func(context.Context, llama.Options) (managedParser, error) {
		close(started)
		<-finish
		return model, nil
	}
	path := modelFixture(t)
	done := make(chan error, 1)
	go func() { done <- c.SetModel(context.Background(), path, "slow") }()
	<-started
	if err := c.ClearModel(); err != nil {
		t.Fatal(err)
	}
	close(finish)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if c.IntentParser().Info().Backend != "rules" || model.closed.Load() != 1 {
		t.Fatal("stale model replaced rules or leaked")
	}
}

func TestModelSwapPersistsBeforePublicationAndClosesOld(t *testing.T) {
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	var models []*managedFixture
	c.modelFactory = func(context.Context, llama.Options) (managedParser, error) {
		p := &managedFixture{IntentParser: fakes.IntentParser{Meta: ports.ParserInfo{Backend: "fixture"}}}
		models = append(models, p)
		return p, nil
	}
	path := modelFixture(t)
	for _, id := range []string{"one", "two"} {
		if err := c.SetModel(context.Background(), path, id); err != nil {
			t.Fatal(err)
		}
	}
	if models[0].closed.Load() != 1 || models[1].closed.Load() != 0 {
		t.Fatal("wrong model closed during swap")
	}
	if prefs, err := config.LoadPrefsChecked(c.cfg.DataDir); err != nil || prefs.ModelID != "two" {
		t.Fatal(prefs, err)
	}
	if err := os.WriteFile(filepath.Join(c.cfg.DataDir, "prefs.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.SetModel(context.Background(), path, "three"); err == nil {
		t.Fatal("corrupt settings overwritten")
	}
	if _, id := c.CurrentModel(); id != "two" || models[2].closed.Load() != 1 {
		t.Fatal("failed save changed active model or leaked replacement")
	}
	if err := c.ClearModel(); err == nil {
		t.Fatal("clear hid failed preference save")
	}
}

func TestSettingsRejectCorruptPreferencesWithoutChangingActiveState(t *testing.T) {
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	path := filepath.Join(c.cfg.DataDir, "prefs.json")
	broken := []byte("{broken-settings")
	if err := os.WriteFile(path, broken, 0600); err != nil {
		t.Fatal(err)
	}
	provider, mode := c.PreviewProviderName(), c.RecommendationMode()
	setters := []func() error{
		func() error { return c.SetPreviewProvider(config.PreviewSpotify) },
		func() error { return c.SetRecommendationMode(core.CLAPFirst) },
		func() error { return c.SetDebugLogging(true) },
		func() error { return c.SetAnalysisEnabled(false) }, c.SetOnboarded,
	}
	for _, setter := range setters {
		if err := setter(); err == nil {
			t.Fatal("setter accepted corrupt settings")
		}
	}
	if c.PreviewProviderName() != provider || c.RecommendationMode() != mode {
		t.Fatal("failed setter changed runtime")
	}
	if got, err := os.ReadFile(path); err != nil || !reflect.DeepEqual(got, broken) {
		t.Fatal("corrupt settings file was overwritten", err)
	}
}
