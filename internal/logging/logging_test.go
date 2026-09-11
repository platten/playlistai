package logging

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestSessionDebugDoesNotLowerOutputThreshold(t *testing.T) {
	store := &Store{}
	var output bytes.Buffer
	log := slog.New(NewHandler(slog.NewTextHandler(&output, nil), store)).With("component", "llama").WithGroup("request")
	log.Debug("not retained")
	store.SetDebug(true)
	log.Debug("private runtime details", "attempt", 2)
	log.Log(context.Background(), slog.LevelDebug+2, "custom debug level")
	log.Info("ordinary record")
	records := store.Read(0)
	if len(records) != 3 || records[0].Level != "DEBUG" || records[1].Level != "DEBUG+2" || !strings.Contains(records[0].Text, "component=llama") || !strings.Contains(records[0].Text, "request.attempt=2") {
		t.Fatalf("debug records or attributes missing: %+v", records)
	}
	if strings.Contains(output.String(), "runtime details") || strings.Contains(output.String(), "custom debug") || !strings.Contains(output.String(), "ordinary record") {
		t.Fatalf("session opt-in changed the output threshold: %s", output.String())
	}
	store.SetDebug(false)
	log.Debug("not retained after opt-out")
	if records = store.Read(0); len(records) != 1 || records[0].Level != "INFO" {
		t.Fatalf("debug records remained after opt-out: %+v", records)
	}
}

func TestSessionAndOutputThresholdsAreIndependent(t *testing.T) {
	for _, outputLevel := range []slog.Level{slog.LevelDebug, slog.LevelError} {
		t.Run(outputLevel.String(), func(t *testing.T) {
			store := &Store{}
			var output bytes.Buffer
			log := slog.New(NewHandler(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: outputLevel}), store))
			log.Debug("debug output")
			log.Info("session info")
			log.Error("both outputs")
			records := store.Read(0)
			if len(records) != 2 || records[0].Level != "INFO" || records[1].Level != "ERROR" {
				t.Fatalf("unexpected records while diagnostics are disabled: %+v", records)
			}
			if got := strings.Contains(output.String(), "debug output"); got != (outputLevel == slog.LevelDebug) {
				t.Fatalf("output handler's threshold was not respected: %s", output.String())
			}
		})
	}
}

type optOutDuringFormatting struct{ store *Store }

func (v optOutDuringFormatting) LogValue() slog.Value {
	v.store.SetDebug(false)
	return slog.StringValue("private value")
}

func TestOptOutDuringFormattingDoesNotRestoreDebugRecord(t *testing.T) {
	store := &Store{}
	store.SetDebug(true)
	log := slog.New(NewHandler(slog.NewTextHandler(io.Discard, nil), store))
	log.Debug("discard this record", "value", optOutDuringFormatting{store})
	if store.DebugEnabled() || len(store.Read(0)) != 0 {
		t.Fatal("an in-flight debug record survived opt-out")
	}
}

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
