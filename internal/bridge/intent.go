package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
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
	SessionID    string          `json:"sessionId"`
	NowPlaying   *core.TrackRef  `json:"nowPlaying"`
	RecentTracks []core.TrackRef `json:"recentTracks"`
	Locale       string          `json:"locale"`
}

func (session IntentSessionContext) input(prompt string) ports.IntentInput {
	return ports.IntentInput{
		Prompt: prompt, SessionID: session.SessionID, NowPlaying: session.NowPlaying,
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
	ctx, current, finish := a.operations.begin(ctx, "intent-preview")
	defer finish()
	if a.app.IntentParser() == nil {
		return IntentPreview{Backend: "none", Seeds: []string{}, RequiredTracks: []string{}, ArtistsExclude: []string{}}, nil
	}
	// No progress bar for the live, keystroke-debounced preview.
	started := time.Now()
	entry, reused, err := a.parseIntentCached(ctx, input, nil)
	if err != nil {
		return IntentPreview{}, err
	}
	m, backend := entry.intent, entry.outcome.Backend
	var issues []intentresolution.Issue
	if a.app.Resolver != nil {
		m, issues = intentresolution.Apply(a.app.Resolver, m)
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

// GenerateFromPrompt parses a prompt, resolves its seed phrases against the
// catalog, and runs the walk.
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
	a.operations.cancel("intent-preview")
	ctx, current, finish := a.operations.begin(ctx, "prompt-generation")
	defer finish()
	result, err := a.generateFromPrompt(ctx, input, selections)
	if err == nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return GenerateResult{}, contextErr
		}
		if !current() {
			return GenerateResult{}, context.Canceled
		}
	}
	return result, err
}

func (a *API) generateFromPrompt(ctx context.Context, input ports.IntentInput, selections []ResolutionSelection) (GenerateResult, error) {
	if a.app.IntentParser() == nil || a.app.Reco == nil || a.app.Catalog == nil || a.app.Resolver == nil {
		return GenerateResult{}, errors.New("not ready — download the catalog first")
	}

	// Stream intent-parse progress to the Generate screen ("intent" op).
	prog := NewWailsProgress()
	prog.Report("intent", 0, -1, "understanding your request")
	parseStarted := time.Now()
	entry, parsedIntentReused, err := a.parseIntentCached(ctx, input, prog)
	if err != nil {
		return GenerateResult{}, err
	}
	m := entry.intent
	timings := []StageTiming{{Stage: "parse", Milliseconds: time.Since(parseStarted).Milliseconds()}}
	m.References = applySelections(m.References, selections)
	m.Journey.Waypoints = applySelections(m.Journey.Waypoints, selections)
	m.RequiredTracks = applySelections(m.RequiredTracks, selections)
	resolveStarted := time.Now()
	m, issues := intentresolution.Apply(a.app.Resolver, m)
	timings = append(timings, StageTiming{Stage: "resolve", Milliseconds: time.Since(resolveStarted).Milliseconds()})
	if err := ctx.Err(); err != nil {
		return GenerateResult{}, err
	}
	if err := intentresolution.BlockingError(issues); err != nil {
		return GenerateResult{}, err
	}
	if err := validatePromptStart(entry.outcome.Backend, m); err != nil {
		return GenerateResult{}, err
	}

	req := BuildPlaylistRequest{
		Version: core.CurrentIntentVersion, Intent: m, SessionID: input.SessionID,
	}

	pl, err := a.runBuild(ctx, req)
	if err != nil {
		return GenerateResult{}, err
	}
	m.Seed = pl.Seed
	req.Intent = m     // pin the generated seed while retaining the complete interpretation
	req.Seed = pl.Seed // legacy readers still find the replay seed
	req.Reproducibility = pl.Reproducibility
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

func validatePromptStart(backend string, intent core.MusicIntent) error {
	if backend == "llama" || len(intent.Seeds.TrackIDs) > 0 || len(intent.Required.TrackIDs) > 0 {
		return nil
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
