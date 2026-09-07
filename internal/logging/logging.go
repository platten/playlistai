// Package logging retains a bounded, memory-only view of the current session.
package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
)

// Entry is one formatted application log record.
type Entry struct {
	ID    uint64 `json:"id"`
	Level string `json:"level"`
	Text  string `json:"text"`
}

// Store keeps the most recent records; it never writes logs to disk.
type Store struct {
	mu      sync.Mutex
	entries []Entry
	next    uint64
}

const capacity = 2000

// Read returns a detached snapshot newer than after.
func (s *Store) Read(after uint64) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Entry, 0, len(s.entries))
	for _, entry := range s.entries {
		if entry.ID > after {
			result = append(result, entry)
		}
	}
	return result
}

func (s *Store) append(level, text string) {
	if len(text) > 8192 {
		text = string([]rune(text[:8192])) + "… [truncated]"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	if len(s.entries) == capacity {
		copy(s.entries, s.entries[1:])
		s.entries = s.entries[:capacity-1]
	}
	s.entries = append(s.entries, Entry{ID: s.next, Level: level, Text: text})
}

type operation struct {
	group string
	attrs []slog.Attr
}
type handler struct {
	output     slog.Handler
	store      *Store
	operations []operation
}

// NewHandler mirrors enabled records to the session store and the original handler.
func NewHandler(output slog.Handler, store *Store) slog.Handler {
	return &handler{output: output, store: store}
}
func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.output.Enabled(ctx, level)
}
func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	var b bytes.Buffer
	var formatter slog.Handler = slog.NewTextHandler(&b, &slog.HandlerOptions{Level: slog.LevelDebug})
	for _, op := range h.operations {
		if op.group != "" {
			formatter = formatter.WithGroup(op.group)
		} else {
			formatter = formatter.WithAttrs(op.attrs)
		}
	}
	if err := formatter.Handle(ctx, r); err != nil {
		return err
	}
	h.store.append(r.Level.String(), strings.TrimSuffix(b.String(), "\n"))
	return h.output.Handle(ctx, r)
}
func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.output = h.output.WithAttrs(attrs)
	clone.operations = append(append([]operation(nil), h.operations...), operation{attrs: append([]slog.Attr(nil), attrs...)})
	return &clone
}
func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := *h
	clone.output = h.output.WithGroup(name)
	clone.operations = append(append([]operation(nil), h.operations...), operation{group: name})
	return &clone
}
