package ports

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

// IntentInput is the raw material for parsing: user text plus optional session
// context. No catalog data is ever passed in — the parser must not see track
// lists or embeddings.
type IntentInput struct {
	Prompt       string
	NowPlaying   *core.TrackRef  // resolves "like this"
	RecentTracks []core.TrackRef // resolves "keep it going"
	Locale       string
}

// ParserInfo describes the active backend for the UI badge.
type ParserInfo struct {
	Name    string
	Backend string // "llama" | "rules"
	Ready   bool   // false until a model is downloaded & loaded (llama backend)
}

// IntentParser translates natural language into a MusicIntent. It is local-only
// (llama.cpp or a rule-based fallback) and never selects or ranks output tracks.
type IntentParser interface {
	Parse(ctx context.Context, in IntentInput) (core.MusicIntent, error)
	Info() ParserInfo
}
