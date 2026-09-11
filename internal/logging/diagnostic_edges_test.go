package logging

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"
)

type disablingPayload struct{ store *Store }

func (p disablingPayload) MarshalJSON() ([]byte, error) {
	p.store.SetDebug(false)
	return []byte(`{"private":"value"}`), nil
}

func TestDiagnosticOptOutDuringSerialization(t *testing.T) {
	store := &Store{}
	store.SetDebug(true)
	store.appendDiagnostic("request", disablingPayload{store})
	if entries := store.Read(0); len(entries) != 0 {
		t.Fatalf("retained private payload after opt-out: %v", entries)
	}
}

func TestDiagnosticMarshalFailureAndContext(t *testing.T) {
	ctx := context.Background()
	if WithDiagnostics(ctx, nil) != ctx {
		t.Fatal("nil store changed context")
	}
	store := &Store{}
	store.SetDebug(true)
	Diagnostic(WithDiagnostics(ctx, store), "bad-payload", make(chan int))
	entries := store.Read(0)
	if len(entries) != 1 || !strings.Contains(entries[0].Text, "marshalError") {
		t.Fatalf("missing serialization diagnostic: %v", entries)
	}
}

func TestTruncationPreservesMultibyteBoundary(t *testing.T) {
	value := truncate(strings.Repeat("界", 100), 32)
	if len(value) > 32 || !utf8.ValidString(value) || !strings.HasSuffix(value, diagnosticTruncationSuffix) {
		t.Fatalf("invalid truncation %q", value)
	}
	handler := NewHandler(slog.NewTextHandler(io.Discard, nil), &Store{})
	if handler.WithGroup("") != handler {
		t.Fatal("empty group changed handler")
	}
}
