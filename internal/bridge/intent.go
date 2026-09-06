package bridge

import (
	"errors"
	"fmt"
	"strings"

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
}

// ParseIntent turns a prompt into a preview without generating anything. It
// uses the active parser, falling back to the rules parser if the model
// errors (see app.Container.ParseIntent).
func (a *API) ParseIntent(prompt string) IntentPreview {
	if a.app.IntentParser() == nil {
		return IntentPreview{Backend: "none", Seeds: []string{}, RequiredTracks: []string{}, ArtistsExclude: []string{}}
	}
	// No progress bar for the live, keystroke-debounced preview.
	m, backend := a.app.ParseIntent(a.context(), ports.IntentInput{Prompt: prompt}, nil)
	m = m.Normalized()
	var issues []intentresolution.Issue
	if a.app.Resolver != nil {
		m, issues = intentresolution.Apply(a.app.Resolver, m)
	}
	return IntentPreview{
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
	}
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
	Name string `json:"name"`
}

// GenerateFromPrompt parses a prompt, resolves its seed phrases against the
// catalog, and runs the walk.
func (a *API) GenerateFromPrompt(prompt string) (GenerateResult, error) {
	return a.generateFromPrompt(prompt, nil)
}

type ResolutionSelection struct {
	Kind    core.ReferenceKind `json:"kind"`
	Query   string             `json:"query"`
	TrackID string             `json:"trackId"`
}

// GenerateFromPromptResolved applies choices made only for references that the
// preview identified as ambiguous, then runs the same shared resolution path.
func (a *API) GenerateFromPromptResolved(prompt string, selections []ResolutionSelection) (GenerateResult, error) {
	return a.generateFromPrompt(prompt, selections)
}

func (a *API) generateFromPrompt(prompt string, selections []ResolutionSelection) (GenerateResult, error) {
	if a.app.IntentParser() == nil || a.app.Reco == nil || a.app.Catalog == nil || a.app.Resolver == nil {
		return GenerateResult{}, errors.New("not ready — download the catalog first")
	}

	// Stream intent-parse progress to the Generate screen ("intent" op).
	prog := NewWailsProgress()
	prog.Report("intent", 0, -1, "understanding your request")
	m, _ := a.app.ParseIntent(a.context(), ports.IntentInput{Prompt: prompt}, prog)
	m = m.Normalized()
	m.References = applySelections(m.References, selections)
	m.Journey.Waypoints = applySelections(m.Journey.Waypoints, selections)
	m.RequiredTracks = applySelections(m.RequiredTracks, selections)
	m, issues := intentresolution.Apply(a.app.Resolver, m)
	if err := intentresolution.BlockingError(issues); err != nil {
		return GenerateResult{}, err
	}
	resolvedReferences := len(m.Seeds.TrackIDs)
	if resolvedReferences == 0 && len(m.Required.TrackIDs) == 0 {
		if len(m.Seeds.Queries) == 0 {
			return GenerateResult{}, errors.New(
				"name an artist as the starting point, e.g. \"something like Bonobo, 20 tracks\"")
		}
		return GenerateResult{}, fmt.Errorf(
			"no catalog match for %s — the catalog is ~957k tracks and plenty of artists aren't in it; "+
				"try a different, better-known artist as the starting point", quoteList(m.Seeds.Queries))
	}

	req := BuildPlaylistRequest{
		Version: core.CurrentIntentVersion,
		Intent:  m,
	}

	pl, err := a.runBuild(req)
	if err != nil {
		return GenerateResult{}, err
	}
	m.Seed = pl.Seed
	req.Intent = m     // pin the generated seed while retaining the complete interpretation
	req.Seed = pl.Seed // legacy readers still find the replay seed

	name := a.playlistName(prompt, m)
	a.saveGenerated(name, prompt, m, req, pl)

	return GenerateResult{Playlist: pl, Request: req, Notes: m.NotesForUser, Name: name}, nil
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
