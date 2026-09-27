package bridge

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
)

type generationKey struct{}
type liveGeneration struct {
	ID      string
	started time.Time
	stop    chan struct{}
	once    sync.Once
}
type liveGenerations struct {
	mu     sync.Mutex
	active map[string]*liveGeneration
}

var nextGeneration atomic.Uint64

func submissionBudget(ctx context.Context, milliseconds int64) (context.Context, context.CancelFunc) {
	if milliseconds <= 0 {
		return ports.WithGenerationBudget(ctx)
	}
	return ports.WithGenerationBudgetSince(ctx, time.UnixMilli(milliseconds))
}

func modeSubmissionBudget(ctx context.Context, mode core.RecommendationMode, milliseconds int64) (context.Context, context.CancelFunc) {
	if mode == core.Automatic {
		if milliseconds <= 0 {
			return ports.WithAutomaticGenerationBudget(ctx)
		}
		return ports.WithAutomaticGenerationBudgetSince(ctx, time.UnixMilli(milliseconds))
	}
	if mode == core.EnhancedHybrid {
		return submissionBudget(ctx, milliseconds)
	}
	return context.WithCancel(ctx)
}

func generationTimedOut(ctx context.Context, input ports.IntentInput, intent core.MusicIntent, stage string) GenerateResult {
	if intent.Version == 0 {
		intent = core.MusicIntent{Version: core.CurrentIntentVersion, OriginalDescription: input.Prompt}
	}
	if intent.Controls.RecommendationMode == "" {
		intent.Controls.RecommendationMode = core.Automatic
	}
	intent = withTrackCount(intent, input.TrackCount).Normalized()
	reason := core.OutcomeReason{Code: "discovery_budget", Detail: "The search time limit was reached during " + stage + "; no eligible playlist was completed.", Action: "Try a simpler prompt or generate again."}
	outcome := core.GenerationOutcome{State: core.OutcomePartial, Reasons: []core.OutcomeReason{reason}}
	result := PlaylistResult{GenerationID: generationProgress(ctx).generationID, Intent: intent, Mode: string(intent.Mode), Seed: intent.Seed, Outcome: outcome,
		Status: GenerationStatus{State: string(core.OutcomePartial), Reasons: outcome.Reasons}, Tracks: []PlaylistTrack{},
		Notices: []PlaylistNotice{{Code: reason.Code, Detail: reason.Detail}}}
	return GenerateResult{Playlist: result, Request: BuildPlaylistRequest{Version: core.CurrentIntentVersion, Intent: intent}, Status: result.Status, Name: deriveTitle(intent, input.Prompt)}
}

// Interpretation can exhaust its work budget while the finalization reserve is
// still live. Preserve source-owned instructions with the pure rules parser;
// this partial result never starts resolution, recommendation, or cache writes.
func interpretationTimedOut(ctx context.Context, input ports.IntentInput, entry parsedIntentEntry, mode core.RecommendationMode, elapsed time.Duration) (GenerateResult, error) {
	if err := ctx.Err(); err != nil {
		return GenerateResult{}, err
	}
	if entry.preparedInput != nil {
		input = *entry.preparedInput
	}
	intent, outcome := entry.intent, entry.outcome
	if intent.Version == 0 {
		var err error
		intent, err = rules.New().Parse(ctx, input)
		if err != nil {
			return GenerateResult{}, err
		}
		outcome.Backend, outcome.FallbackUsed, outcome.FallbackReason = "rules", true, "timeout"
	}
	if err := ctx.Err(); err != nil {
		return GenerateResult{}, err
	}
	intent.Controls.RecommendationMode = mode
	intent.PreparedMusicSnapshot = input.PreparedMusicSnapshot
	result := generationTimedOut(ctx, input, intent, "interpretation")
	if input.SourceFacts != nil {
		outcome.Recognition = input.SourceFacts.Recognition
	}
	result.Status.Parser = parserStatus(outcome)
	result.Status.Timings = []StageTiming{{Stage: "parse", Milliseconds: elapsed.Milliseconds()}}
	result.Playlist.Status = result.Status
	return result, nil
}

func (a *API) beginGeneration(ctx context.Context, id string) (context.Context, func()) {
	if id == "" {
		id = fmt.Sprintf("generation-%d", nextGeneration.Add(1))
	}
	started := ports.GenerationStarted(ctx)
	if started.IsZero() {
		started = time.Now()
	}
	g := &liveGeneration{ID: id, stop: make(chan struct{}), started: started}
	a.live.mu.Lock()
	if a.live.active == nil {
		a.live.active = map[string]*liveGeneration{}
	}
	if previous := a.live.active[id]; previous != nil {
		previous.once.Do(func() { close(previous.stop) })
	}
	a.live.active[id] = g
	a.live.mu.Unlock()
	return context.WithValue(ctx, generationKey{}, g), func() {
		a.live.mu.Lock()
		defer a.live.mu.Unlock()
		if a.live.active[id] == g {
			delete(a.live.active, id)
		}
	}
}
func generationFromContext(ctx context.Context) *liveGeneration {
	g, _ := ctx.Value(generationKey{}).(*liveGeneration)
	return g
}

// StopAndKeepCheckedTracks stops candidate discovery and audio analysis.
// Ranking and sequencing finish over checked tracks; ordinary cancellation
// still discards the result.
func (a *API) StopAndKeepCheckedTracks(generationID string) {
	a.live.mu.Lock()
	defer a.live.mu.Unlock()
	if g := a.live.active[generationID]; g != nil {
		g.once.Do(func() { close(g.stop) })
	}
}

func generationProgress(ctx context.Context) *WailsProgress {
	p := NewWailsProgress()
	p.ctx = ctx
	if g := generationFromContext(ctx); g != nil {
		p.generationID = g.ID
		p.started = g.started
	}
	return p
}

func withEvidenceIdentity(identity *Reproducibility, snapshot *core.AudioEvidenceSnapshot) {
	if snapshot == nil {
		return
	}
	identity.EvidenceSnapshot = snapshot.ID
	identity.ID = audioIdentity(identity.ID, snapshot.ID)
}
