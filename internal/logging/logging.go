// Package logging retains a bounded, memory-only view of the current session.
package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Entry is one formatted application log record.
type Entry struct {
	ID    uint64 `json:"id"`
	Level string `json:"level"`
	Text  string `json:"text"`
}

// Store keeps the most recent records; it never writes logs to disk.
type Store struct {
	mu            sync.Mutex
	entries       []Entry
	next          uint64
	retainedBytes int
	debug         bool
}

const (
	capacity                   = 2000
	standardEntryLimit         = 8 << 10
	diagnosticEntryLimit       = 64 << 10
	diagnosticRetentionLimit   = 16 << 20
	diagnosticTruncationSuffix = "… [truncated]"
)

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

// DebugEnabled reports whether opt-in, potentially sensitive diagnostics are
// retained for the current session.
func (s *Store) DebugEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.debug
}

// SetDebug enables detailed diagnostics. Disabling it immediately removes
// retained debug entries so prompts and provider lookup terms do not linger.
func (s *Store) SetDebug(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.debug = enabled
	if enabled {
		return
	}
	kept := s.entries[:0]
	s.retainedBytes = 0
	for _, entry := range s.entries {
		var level slog.Level
		_ = level.UnmarshalText([]byte(entry.Level))
		if level >= slog.LevelInfo {
			kept = append(kept, entry)
			s.retainedBytes += len(entry.Text)
		}
	}
	s.entries = kept
}

func (s *Store) enabled(level slog.Level) bool {
	return level >= slog.LevelInfo || (level >= slog.LevelDebug && s.DebugEnabled())
}

func (s *Store) append(level slog.Level, text string) {
	text = truncate(text, standardEntryLimit)
	s.mu.Lock()
	defer s.mu.Unlock()
	// Recheck under the same lock as SetDebug: a record formatted before an
	// opt-out must not reintroduce private details after the store was cleared.
	if level < slog.LevelInfo && !s.debug {
		return
	}
	s.appendLocked(level.String(), text)
}

func (s *Store) appendDiagnostic(event string, payload any) {
	if !s.DebugEnabled() {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = []byte(fmt.Sprintf(`{"marshalError":%q}`, err.Error()))
	}
	text := fmt.Sprintf("time=%s level=DEBUG event=%q data=%s", time.Now().Format(time.RFC3339Nano), event, raw)
	text = truncate(text, diagnosticEntryLimit)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.debug {
		return
	}
	s.appendLocked(slog.LevelDebug.String(), text)
}

func (s *Store) appendLocked(level, text string) {
	s.next++
	for len(s.entries) > 0 && (len(s.entries) >= capacity || s.retainedBytes+len(text) > diagnosticRetentionLimit) {
		s.retainedBytes -= len(s.entries[0].Text)
		copy(s.entries, s.entries[1:])
		s.entries = s.entries[:len(s.entries)-1]
	}
	s.entries = append(s.entries, Entry{ID: s.next, Level: level, Text: text})
	s.retainedBytes += len(text)
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	limit -= len(diagnosticTruncationSuffix)
	for limit > 0 && !utf8.ValidString(text[:limit]) {
		limit--
	}
	return text[:limit] + diagnosticTruncationSuffix
}

type diagnosticStoreKey struct{}

// WithDiagnostics makes the session's opt-in diagnostic sink available to
// request-scoped parsers, provider clients, and recommendation stages.
func WithDiagnostics(ctx context.Context, store *Store) context.Context {
	if store == nil {
		return ctx
	}
	return context.WithValue(ctx, diagnosticStoreKey{}, store)
}

// Diagnostic records structured request data only while the user-controlled
// debug preference is enabled. It never writes to the process logger or disk.
func Diagnostic(ctx context.Context, event string, payload any) {
	store, _ := ctx.Value(diagnosticStoreKey{}).(*Store)
	if store != nil {
		store.appendDiagnostic(event, payload)
	}
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

// NewHandler retains INFO+ and opt-in DEBUG records in memory, independently of
// the output handler's threshold. Enabling session diagnostics never lowers the
// console/file output threshold.
func NewHandler(output slog.Handler, store *Store) slog.Handler {
	return &handler{output: output, store: store}
}
func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.store.enabled(level) || h.output.Enabled(ctx, level)
}
func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	if h.store.enabled(r.Level) {
		if err := h.retain(ctx, r); err != nil {
			return err
		}
	}
	if h.output.Enabled(ctx, r.Level) {
		return h.output.Handle(ctx, r)
	}
	return nil
}
func (h *handler) retain(ctx context.Context, r slog.Record) error {
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
	h.store.append(r.Level, strings.TrimSuffix(b.String(), "\n"))
	return nil
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
