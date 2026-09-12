package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/logging"
	"github.com/platten/playlistai/internal/ports"
	intentresolution "github.com/platten/playlistai/internal/resolution"
)

// IntentPreview is the parsed intent shown as chips on the Generate screen.
type IntentPreview struct {
	Intent             core.MusicIntent         `json:"intent"`
	Seeds              []string                 `json:"seeds"`
	RequiredTracks     []string                 `json:"requiredTracks"`
	Mode               string                   `json:"mode"`
	Count              int                      `json:"count"`
	Creativity         float64                  `json:"creativity"`
	Noise              float64                  `json:"noise"`
	Lookback           int                      `json:"lookback"`
	ArtistsExclude     []string                 `json:"artistsExclude"`
	NoRepeatArtist     bool                     `json:"noRepeatArtist"`
	ExcludeSeedArtists bool                     `json:"excludeSeedArtists"`
	Notes              string                   `json:"notes"`
	Backend            string                   `json:"backend"` // "rules" | "llama" | "none"
	ResolutionIssues   []intentresolution.Issue `json:"resolutionIssues"`
	Parser             ParserStatus             `json:"parser"`
	Cached             bool                     `json:"cached"`
	Timing             StageTiming              `json:"timing"`
}

type IntentSessionContext struct {
	GenerationID string          `json:"generationId"`
	SessionID    string          `json:"sessionId"`
	NowPlaying   *core.TrackRef  `json:"nowPlaying"`
	RecentTracks []core.TrackRef `json:"recentTracks"`
	Locale       string          `json:"locale"`
}

func (session IntentSessionContext) input(prompt string) ports.IntentInput {
	return ports.IntentInput{
		GenerationID: session.GenerationID,
		Prompt:       prompt, SessionID: session.SessionID, NowPlaying: session.NowPlaying,
		RecentTracks: session.RecentTracks, Locale: session.Locale,
	}
}

// ParseIntent turns a prompt into a preview without generating anything. It
// uses the active parser, falling back to the rules parser if the model
// errors (see app.Container.ParseIntent).
func (a *API) ParseIntent(ctx context.Context, prompt string) (IntentPreview, error) {
	return a.parseIntentOperation(ctx, ports.IntentInput{Prompt: prompt})
}

func (a *API) ParseIntentWithContext(ctx context.Context, prompt string, session IntentSessionContext) (IntentPreview, error) {
	return a.parseIntentOperation(ctx, session.input(prompt))
}

func (a *API) parseIntentOperation(ctx context.Context, input ports.IntentInput) (IntentPreview, error) {
	ctx, release := a.app.OperationContext(ctx)
	defer release()
	if err := ctx.Err(); err != nil {
		return IntentPreview{}, err
	}
	ctx = a.diagnosticContext(ctx)
	logging.Diagnostic(ctx, "generation.prompt", input)
	input.SkipMetadata = a.app.RecommendationMode() == core.DeejAIOnly
	ctx, current, finish := a.operations.begin(ctx, "intent-preview")
	defer finish()
	if a.app.IntentParser() == nil {
		return IntentPreview{Backend: "none", Seeds: []string{}, RequiredTracks: []string{}, ArtistsExclude: []string{}}, nil
	}
	// The composer invokes this after submission; legacy preview callers remain
	// supported. The frontend owns the parse/build transaction's progress UI.
	started := time.Now()
	entry, reused, err := a.parseIntentCached(ctx, input, nil)
	if err != nil {
		logging.Diagnostic(ctx, "intent.error", err.Error())
		return IntentPreview{}, err
	}
	m, backend := entry.intent, entry.outcome.Backend
	logging.Diagnostic(ctx, "intent.parsed", m)
	logging.Diagnostic(ctx, "intent.parser_status", parserStatus(entry.outcome))
	var issues []intentresolution.Issue
	if a.runtime().Resolver != nil {
		m, issues = intentresolution.Apply(a.runtime().Resolver, m)
	}
	preview := IntentPreview{
		Intent:             m,
		Seeds:              referenceQueries(m.References),
		RequiredTracks:     referenceQueries(m.RequiredTracks),
		Mode:               string(m.Mode),
		Count:              m.Count,
		Creativity:         m.Creativity,
		Noise:              m.Noise,
		Lookback:           m.Lookback,
		ArtistsExclude:     orEmpty(m.Constraints.ArtistsExclude),
		NoRepeatArtist:     m.Constraints.NoRepeatArtistBackToBack,
		ExcludeSeedArtists: m.Constraints.ExcludeSeedArtists,
		Notes:              m.NotesForUser,
		Backend:            backend,
		ResolutionIssues:   issues,
		Parser:             parserStatus(entry.outcome),
		Cached:             reused,
		Timing:             StageTiming{Stage: "parse", Milliseconds: time.Since(started).Milliseconds()},
	}
	if err := ctx.Err(); err != nil {
		return IntentPreview{}, err
	}
	if !current() {
		return IntentPreview{}, context.Canceled
	}
	return preview, nil
}

// GenerateResult is the outcome of GenerateFromPrompt: the playlist plus the
// resolved request so the Playlist screen can keep re-running it as the user
// moves the sliders.
type GenerateResult struct {
	Playlist PlaylistResult       `json:"playlist"`
	Request  BuildPlaylistRequest `json:"request"`
	Notes    string               `json:"notes"`
	// Name is a short (<= 6 words) label for the playlist — the model's title
	// when a local model is active, otherwise derived from the parsed intent.
	Name   string           `json:"name"`
	Status GenerationStatus `json:"status"`
}

// GenerateFromPrompt preserves the parsed intent through reference resolution
// and the selected recommendation policy. Building alone is not exposure.
func (a *API) GenerateFromPrompt(ctx context.Context, prompt string) (GenerateResult, error) {
	return a.generateFromPromptOperation(ctx, ports.IntentInput{Prompt: prompt}, nil)
}

func (a *API) GenerateFromPromptWithContext(ctx context.Context, prompt string, session IntentSessionContext) (GenerateResult, error) {
	return a.generateFromPromptOperation(ctx, session.input(prompt), nil)
}

type ResolutionSelection struct {
	Kind    core.ReferenceKind `json:"kind"`
	Query   string             `json:"query"`
	TrackID string             `json:"trackId"`
}

// GenerateFromPromptResolved applies choices made only for references that the
// preview identified as ambiguous, then runs the same shared resolution path.
func (a *API) GenerateFromPromptResolved(ctx context.Context, prompt string, selections []ResolutionSelection) (GenerateResult, error) {
	return a.generateFromPromptOperation(ctx, ports.IntentInput{Prompt: prompt}, selections)
}

func (a *API) GenerateFromPromptResolvedWithContext(ctx context.Context, prompt string, selections []ResolutionSelection, session IntentSessionContext) (GenerateResult, error) {
	return a.generateFromPromptOperation(ctx, session.input(prompt), selections)
}

func (a *API) generateFromPromptOperation(ctx context.Context, input ports.IntentInput, selections []ResolutionSelection) (GenerateResult, error) {
	ctx, release := a.app.OperationContext(ctx)
	defer release()
	ctx = a.diagnosticContext(ctx)
	logging.Diagnostic(ctx, "generation.prompt", input)
	a.operations.cancel("intent-preview")
	ctx, current, finish := a.operations.begin(ctx, generationOperation)
	defer finish()
	ctx, finishGeneration := a.beginGeneration(ctx, input.GenerationID)
	defer finishGeneration()
	result, err := a.generateFromPrompt(ctx, input, selections)
	if err != nil {
		logging.Diagnostic(ctx, "generation.error", err.Error())
	}
	if err == nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return GenerateResult{}, contextErr
		}
		if !current() {
			return GenerateResult{}, context.Canceled
		}
		a.preparePresentation(result.Request, &result.Playlist)
	}
	return result, err
}

func (a *API) generateFromPrompt(ctx context.Context, input ports.IntentInput, selections []ResolutionSelection) (GenerateResult, error) {
	if a.app.IntentParser() == nil || a.runtime().Reco == nil || a.runtime().Catalog == nil || a.runtime().Resolver == nil {
		return GenerateResult{}, errors.New("not ready — download the catalog first")
	}

	// Stream intent-parse progress to the Generate screen ("intent" op).
	prog := generationProgress(ctx)
	recommendationMode := a.app.RecommendationMode()
	input.SkipMetadata = recommendationMode == core.DeejAIOnly
	prog.Report("intent", 0, -1, "understanding your request")
	parseStarted := time.Now()
	entry, parsedIntentReused, err := a.parseIntentCached(ctx, input, prog)
	if err != nil {
		return GenerateResult{}, err
	}
	m := entry.intent
	logging.Diagnostic(ctx, "intent.parsed", m)
	logging.Diagnostic(ctx, "intent.parser_status", parserStatus(entry.outcome))
	m.Controls.RecommendationMode = recommendationMode
	m.VerificationPolicy = core.BestAvailable
	timings := []StageTiming{{Stage: "parse", Milliseconds: time.Since(parseStarted).Milliseconds()}}
	m.References = applySelections(m.References, selections)
	m.InferredAnchors = applyAnchorSelections(m.InferredAnchors, selections)
	m.Journey.Waypoints = applySelections(m.Journey.Waypoints, selections)
	m.RequiredTracks = applySelections(m.RequiredTracks, selections)
	resolveStarted := time.Now()
	if a.app.Knowledge != nil && m.Version >= 8 && m.Controls.RecommendationMode != core.DeejAIOnly {
		if knowledge, ok := a.app.Knowledge.(ports.IterativeMusicKnowledge); ok {
			m, err = knowledge.PrepareMusic(ctx, m, a.runtime().Catalog, a.runtime().Resolver, prog)
		} else {
			m, err = a.app.Knowledge.ResolveMusic(ctx, m, a.runtime().Catalog, a.runtime().Resolver, prog)
		}
		if err != nil {
			return GenerateResult{}, err
		}
		logKnowledgeDiagnostics(ctx, m.Knowledge)
	}
	m, _ = intentresolution.Apply(a.runtime().Resolver, m)
	timings = append(timings, StageTiming{Stage: "resolve", Milliseconds: time.Since(resolveStarted).Milliseconds()})
	if err := ctx.Err(); err != nil {
		return GenerateResult{}, err
	}
	if err := validatePromptStart(entry.outcome.Backend, entry.outcome.RequestedBackend, m); err != nil {
		return GenerateResult{}, err
	}

	req := BuildPlaylistRequest{
		Version: core.CurrentIntentVersion, Intent: m, SessionID: input.SessionID,
	}

	pl, err := a.runBuild(ctx, req)
	if err != nil {
		return GenerateResult{}, err
	}
	m = pl.Intent
	m.Seed = pl.Seed
	req.Intent = m     // pin the generated seed while retaining the complete interpretation
	req.Seed = pl.Seed // legacy readers still find the replay seed
	req.Reproducibility = pl.Reproducibility
	req.EnhancedAudio = pl.EnhancedAudio
	req.RequestID = pl.Reproducibility.ID

	titleStarted := time.Now()
	name := a.playlistName(ctx, input.Prompt, m)
	if err := ctx.Err(); err != nil {
		return GenerateResult{}, err
	}
	timings = append(timings, pl.Status.Timings...)
	timings = append(timings, StageTiming{Stage: "title", Milliseconds: time.Since(titleStarted).Milliseconds()})
	pl.Status.ParsedIntentReused = parsedIntentReused
	pl.Status.Parser = parserStatus(entry.outcome)
	pl.Status.Timings = timings
	status := pl.Status
	a.saveGenerated(ctx, name, input.Prompt, m, req, pl)
	a.log.Info("prompt generation completed", "state", status.State, "parser", status.Parser.Backend,
		"fallback", status.Parser.FallbackUsed, "intent_reused", status.ParsedIntentReused,
		"parse_ms", timings[0].Milliseconds, "resolve_ms", timings[1].Milliseconds)

	return GenerateResult{Playlist: pl, Request: req, Notes: m.NotesForUser, Name: name, Status: status}, nil
}

func logKnowledgeDiagnostics(ctx context.Context, knowledge *core.KnowledgeSnapshot) {
	if knowledge == nil {
		logging.Diagnostic(ctx, "metadata.knowledge_summary", "unavailable")
		return
	}
	logging.Diagnostic(ctx, "metadata.knowledge_summary", knowledge)
	for _, pool := range knowledge.ArtistPools {
		logging.Diagnostic(ctx, "metadata.artist_pool", pool)
	}
	for _, track := range knowledge.Tracks {
		logging.Diagnostic(ctx, "metadata.track", track)
	}
}

func applyAnchorSelections(anchors []core.InferredAnchor, selections []ResolutionSelection) []core.InferredAnchor {
	out := append([]core.InferredAnchor(nil), anchors...)
	for index := range out {
		selected := applySelections([]core.IntentReference{out[index].Reference}, selections)
		out[index].Reference = selected[0]
	}
	return out
}

func validatePromptStart(backend, requestedBackend string, intent core.MusicIntent) error {
	if intent.Controls.RecommendationMode == core.DeejAIOnly {
		return nil // BuildOnly returns an actionable, structured missing-seed outcome.
	}
	// A rules fallback while an LLM was requested must preserve the semantic
	// request and reach the orchestrator's structured unsupported outcome. It
	// must not silently reinterpret the request as catalog-only artist lookup.
	llmRequested := backend == "llama" || requestedBackend == "llama"
	if llmRequested || len(intent.Seeds.TrackIDs) > 0 || len(intent.Required.TrackIDs) > 0 {
		return nil
	}
	// Online artist recovery produces detailed notices, including misses and
	// outages. Let the orchestrator return those with a clarification outcome.
	if intent.Knowledge != nil {
		if core.WantsInstrumental(intent) || len(intent.Knowledge.Candidates) > 0 || len(intent.EssentialCriteria) > 0 || len(intent.Preferences.Genres) > 0 {
			return nil
		}
		for _, ref := range intent.References {
			if ref.Kind == core.ReferenceArtist && ref.Influence == core.InfluencePositive {
				return nil
			}
		}
	}
	if len(intent.Seeds.Queries) == 0 {
		return errors.New(
			"catalog-only mode requires a seed artist or track, e.g. \"something like Bonobo, 20 tracks\"")
	}
	return fmt.Errorf(
		"no catalog match for %s — the catalog is ~957k tracks and plenty of artists aren't in it; "+
			"catalog-only mode requires a different seed artist or track", quoteList(intent.Seeds.Queries))
}

func applySelections(references []core.IntentReference, selections []ResolutionSelection) []core.IntentReference {
	out := append([]core.IntentReference(nil), references...)
	for i := range out {
		for _, selection := range selections {
			if selection.Kind == out[i].Kind && strings.EqualFold(strings.TrimSpace(selection.Query), strings.TrimSpace(out[i].Query)) {
				out[i].TrackID = selection.TrackID
				out[i].Resolution = nil
			}
		}
	}
	return out
}

func referenceQueries(references []core.IntentReference) []string {
	out := make([]string, 0, len(references))
	for _, reference := range references {
		if reference.Query != "" {
			out = append(out, reference.Query)
		}
	}
	return orEmpty(out)
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// quoteList renders ["a","b"] as `"a", "b"` for an error message.
func quoteList(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
}
