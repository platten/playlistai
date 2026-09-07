package logging

import (
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
