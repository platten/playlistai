package logging

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestSessionRetentionAndAttributes(t *testing.T) {
	store := &Store{}
	log := slog.New(NewHandler(slog.NewTextHandler(io.Discard, nil), store))
	log.Debug("hidden")
	log.With("component", "test").WithGroup("request").Warn("failed", "id", 42)
	records := store.Read(0)
	if len(records) != 1 || records[0].Level != "WARN" || !strings.Contains(records[0].Text, "component=test") || !strings.Contains(records[0].Text, "request.id=42") {
		t.Fatalf("records: %+v", records)
	}
	records[0].Text = "changed"
	if store.Read(0)[0].Text == "changed" {
		t.Fatal("snapshot aliases store")
	}
	for range capacity {
		log.Info("next")
	}
	records = store.Read(0)
	if len(records) != capacity || records[0].ID != 2 {
		t.Fatal("retention not bounded")
	}
	if len(store.Read(records[len(records)-1].ID)) != 0 {
		t.Fatal("cursor repeats records")
	}
}

func TestDiagnosticsAreOptInAndClearedWhenDisabled(t *testing.T) {
	store := &Store{}
	ctx := WithDiagnostics(context.Background(), store)
	Diagnostic(ctx, "generation.prompt", map[string]string{"prompt": "private request"})
	if got := store.Read(0); len(got) != 0 {
		t.Fatalf("disabled diagnostics retained: %+v", got)
	}

	log := slog.New(NewHandler(slog.NewTextHandler(io.Discard, nil), store))
	log.Info("ordinary record")
	store.SetDebug(true)
	Diagnostic(ctx, "generation.prompt", map[string]string{"prompt": "private request"})
	records := store.Read(0)
	if len(records) != 2 || records[1].Level != "DEBUG" || !strings.Contains(records[1].Text, `"private request"`) {
		t.Fatalf("enabled diagnostic missing: %+v", records)
	}

	store.SetDebug(false)
	records = store.Read(0)
	if store.DebugEnabled() || len(records) != 1 || records[0].Level != "INFO" {
		t.Fatalf("disabling diagnostics did not clear only debug records: %+v", records)
	}
}

func TestDiagnosticPayloadRetainsEmbeddingVectorsWithinMemoryBudget(t *testing.T) {
	store := &Store{}
	store.SetDebug(true)
	ctx := WithDiagnostics(context.Background(), store)
	embedding := make([]float32, 3072)
	for index := range embedding {
		embedding[index] = 0.1234567
	}
	Diagnostic(ctx, "analysis.clap_audio_embedding", map[string]any{"embedding": embedding})
	records := store.Read(0)
	if len(records) != 1 || strings.Contains(records[0].Text, diagnosticTruncationSuffix) || !strings.Contains(records[0].Text, `"embedding":[0.1234567`) {
		t.Fatalf("embedding diagnostic was missing or truncated: records=%d bytes=%d", len(records), len(records[0].Text))
	}
	if store.retainedBytes > diagnosticRetentionLimit {
		t.Fatalf("diagnostics exceeded memory budget: %d", store.retainedBytes)
	}
}

func TestConcurrentWritersAndBoundedRecord(t *testing.T) {
	store := &Store{}
	log := slog.New(NewHandler(slog.NewTextHandler(io.Discard, nil), store))
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				log.Error(strings.Repeat("x", 9000))
				_ = store.Read(0)
			}
		}()
	}
	wg.Wait()
	records := store.Read(0)
	if len(records) != 800 {
		t.Fatalf("lost records: %d", len(records))
	}
	for i, r := range records {
		if r.ID != uint64(i+1) || len(r.Text) > 8300 {
			t.Fatal("invalid ID or unbounded record")
		}
	}
}
