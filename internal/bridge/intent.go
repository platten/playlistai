package bridge

import (
	"errors"
	"fmt"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// IntentPreview is the parsed intent shown as chips on the Generate screen.
type IntentPreview struct {
	Seeds              []string `json:"seeds"`
	RequiredTracks     []string `json:"requiredTracks"`
	Mode               string   `json:"mode"`
	Count              int      `json:"count"`
	Creativity         float64  `json:"creativity"`
	Noise              float64  `json:"noise"`
	Lookback           int      `json:"lookback"`
	ArtistsExclude     []string `json:"artistsExclude"`
	NoRepeatArtist     bool     `json:"noRepeatArtist"`
	ExcludeSeedArtists bool     `json:"excludeSeedArtists"`
	Notes              string   `json:"notes"`
	Backend            string   `json:"backend"` // "rules" | "llama" | "none"
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
	return IntentPreview{
		Seeds:              orEmpty(m.Seeds.Queries),
		RequiredTracks:     orEmpty(m.Required.Queries),
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
	if a.app.IntentParser() == nil || a.app.Reco == nil || a.app.Catalog == nil {
		return GenerateResult{}, errors.New("not ready — download the catalog first")
	}

	// Stream intent-parse progress to the Generate screen ("intent" op).
	prog := NewWailsProgress()
	prog.Report("intent", 0, -1, "understanding your request")
	m, _ := a.app.ParseIntent(a.context(), ports.IntentInput{Prompt: prompt}, prog)
	m = m.Normalized()

	var referenceIDs []string
	var requiredIDs []string
	seen := map[string]struct{}{}
	addID := func(dst *[]string, id string) {
		if id == "" {
			return
		}
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		*dst = append(*dst, id)
	}
	for _, id := range m.Seeds.TrackIDs {
		addID(&referenceIDs, id)
	}
	for _, q := range m.Seeds.Queries {
		if hits := a.app.Catalog.Resolve(q, 1); len(hits) > 0 {
			addID(&referenceIDs, hits[0].ID)
		}
	}
	seen = map[string]struct{}{}
	for _, id := range m.Required.TrackIDs {
		addID(&requiredIDs, id)
	}
	for _, q := range m.Required.Queries {
		hits := a.app.Catalog.Resolve(q, 1)
		if len(hits) == 0 {
			return GenerateResult{}, fmt.Errorf("required track %q did not resolve in the catalog", q)
		}
		addID(&requiredIDs, hits[0].ID)
	}
	if len(referenceIDs) == 0 && len(requiredIDs) == 0 {
		if len(m.Seeds.Queries) == 0 {
			return GenerateResult{}, errors.New(
				"name an artist as the starting point, e.g. \"something like Bonobo, 20 tracks\"")
		}
		return GenerateResult{}, fmt.Errorf(
			"no catalog match for %s — the catalog is ~957k tracks and plenty of artists aren't in it; "+
				"try a different, better-known artist as the starting point", quoteList(m.Seeds.Queries))
	}

	req := BuildPlaylistRequest{
		Version:           core.CurrentIntentVersion,
		ReferenceIDs:      referenceIDs,
		RequiredIDs:       requiredIDs,
		Mode:              string(m.Mode),
		Creativity:        m.Creativity,
		Noise:             m.Noise,
		Lookback:          m.Lookback,
		Count:             m.Count,
		Seed:              0,
		NoRepeatArtist:    m.Constraints.NoRepeatArtistBackToBack,
		ArtistsExclude:    m.Constraints.ArtistsExclude,
		ExcludeSeedArtist: m.Constraints.ExcludeSeedArtists,
	}

	pl, err := a.runBuild(req)
	if err != nil {
		return GenerateResult{}, err
	}
	req.Seed = pl.Seed // pin the seed the engine chose so re-runs are stable

	name := a.playlistName(prompt, m)
	a.saveGenerated(name, prompt, m, req, pl)

	return GenerateResult{Playlist: pl, Request: req, Notes: m.NotesForUser, Name: name}, nil
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
