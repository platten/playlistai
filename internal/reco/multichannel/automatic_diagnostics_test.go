package multichannel

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/logging"
	"github.com/platten/playlistai/internal/ports"
)

func TestAutomaticPhaseDiagnosticsOptInAndPolicyIndependent(t *testing.T) {
	c, intent, retriever := automaticFixture(t, 1)
	intent.OriginalDescription = "private request not for phase logs"
	automaticSupport(c, intent, "0", "house")
	engine := NewAutomatic(c, nil, retriever, DefaultConfig()).
		WithIntentPreparer(func(_ context.Context, intent core.MusicIntent, _ ports.Catalog, _ ports.ReferenceResolver) (core.MusicIntent, error) {
			return intent, nil
		}).
		WithFeaturePreparer(func(context.Context, core.MusicIntent, ports.Catalog) error { return nil }).
		WithPreparedRetriever(retriever)
	store := &logging.Store{}
	ctx := logging.WithDiagnostics(context.Background(), store)
	first, err := engine.Build(ctx, intent)
	if err != nil || len(store.Read(0)) != 0 {
		t.Fatal("opt-out diagnostics retained", err)
	}
	store.SetDebug(true)
	second, err := engine.Build(ctx, intent)
	if err != nil || first.Search.ID != second.Search.ID {
		t.Fatal("diagnostics changed the frozen result", err)
	}
	entries := store.Read(0)
	if len(entries) != 14 { // Seven exercised phases, each started/completed.
		t.Fatal("unexpected phase record count", len(entries))
	}
	for _, entry := range entries {
		if !strings.Contains(entry.Text, "automatic.phase") || strings.Contains(entry.Text, intent.OriginalDescription) || strings.Contains(entry.Text, "Artist") || strings.Contains(entry.Text, "Song") {
			t.Fatal("unexpected diagnostic content", entry)
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	finish := automaticPhase(ctx, "fixture")
	cancel()
	finish(3, errors.New("private provider URL or input"))
	last := store.Read(0)
	text := last[len(last)-1].Text
	if strings.Contains(text, "private provider") || !strings.Contains(text, `"contextStopped":true`) || !strings.Contains(text, `"items":3`) || !strings.Contains(text, `"error":true`) {
		t.Fatal(text)
	}
}
