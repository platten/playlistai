package ports

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

// IntentInput is the raw material for parsing: user text plus optional session
// context. No catalog data is ever passed in — the parser must not see track
// lists or embeddings.
type IntentInput struct {
	TrackCount   int  // explicit UI control; zero preserves legacy prompt-only behavior
	SkipMetadata bool // desktop engine-only policy; not a model-generated field
	GenerationID string
	Prompt       string
	SessionID    string
	NowPlaying   *core.TrackRef  // resolves "like this"
	RecentTracks []core.TrackRef // resolves "keep it going"
	Locale       string
	// IntentProposals come only from the local auxiliary text encoder. They are
	// advisory and contain no catalog lists or audio embeddings.
	IntentProposals []core.IntentProposal
	// SourceFacts is the immutable extraction snapshot shared by every model
	// attempt for this request. Callers may omit it; the local client prepares it.
	SourceFacts *core.IntentTranslation
}

// ParserInfo describes the active backend for the UI badge.
type ParserInfo struct {
	Name            string
	Backend         string // "llama" | "rules"
	Version         string // parser implementation/version, excluding the selected model
	Ready           bool   // false until a model is downloaded & loaded (llama backend)
	ContractVersion int
	Evidence        bool // parser emits source-grounded interpretation evidence
}

// IntentParser translates natural language into a MusicIntent. It is local-only
// (llama.cpp or a rule-based fallback) and never selects or ranks output tracks.
type IntentParser interface {
	Parse(ctx context.Context, in IntentInput) (core.MusicIntent, error)
	Info() ParserInfo
}
