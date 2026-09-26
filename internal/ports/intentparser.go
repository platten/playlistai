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
	// RecognitionIdentity pins every offline resource that affected source
	// recognition. It is computed before parse-cache lookup and is never sent as
	// user-visible language to a model.
	RecognitionIdentity string
	// SourceFacts is the immutable extraction snapshot shared by every model
	// attempt for this request. Callers may omit it; the local client prepares it.
	SourceFacts *core.IntentTranslation
	// EnrichParsingContext is opt-in until the paired model evaluation passes.
	EnrichParsingContext bool
	// ObserveParseAttempt reports budgets without logging private prompt text.
	ObserveParseAttempt func(ParseAttemptObservation)
}

type ParseAttemptObservation struct {
	Attempt            int    `json:"attempt"`
	MandatoryTokens    *int   `json:"mandatoryTokens,omitempty"`
	MandatoryByteBound int    `json:"mandatoryByteBound"`
	OptionalByteBound  int    `json:"optionalByteBound"`
	OutputAllowance    int    `json:"outputAllowance"`
	UsedHintIndexes    []int  `json:"usedHintIndexes,omitempty"`
	OmittedHints       int    `json:"omittedHints"`
	Truncated          bool   `json:"truncated"`
	Error              string `json:"error,omitempty"`
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
