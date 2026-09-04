package bridge

import (
	"errors"
	"fmt"

	"github.com/platten/playlistai/internal/ports"
)

// IntentPreview is the parsed intent shown as chips on the Generate screen.
type IntentPreview struct {
	Seeds          []string `json:"seeds"`
	Mode           string   `json:"mode"`
	Count          int      `json:"count"`
	Creativity     float64  `json:"creativity"`
	Noise          float64  `json:"noise"`
	Lookback       int      `json:"lookback"`
	ArtistsExclude []string `json:"artistsExclude"`
	NoRepeatArtist bool     `json:"noRepeatArtist"`
	Notes          string   `json:"notes"`
	Backend        string   `json:"backend"` // "rules" | "llama" | "none"
}

// ParseIntent turns a prompt into a preview without generating anything.
func (a *API) ParseIntent(prompt string) IntentPreview {
	if a.app.IntentParser() == nil {
		return IntentPreview{Backend: "none", Seeds: []string{}, ArtistsExclude: []string{}}
	}
	m, _ := a.app.IntentParser().Parse(a.context(), ports.IntentInput{Prompt: prompt})
	m = m.Normalized()
	return IntentPreview{
		Seeds:          orEmpty(m.Seeds.Queries),
		Mode:           string(m.Mode),
		Count:          m.Count,
		Creativity:     m.Creativity,
		Noise:          m.Noise,
		Lookback:       m.Lookback,
		ArtistsExclude: orEmpty(m.Constraints.ArtistsExclude),
		NoRepeatArtist: m.Constraints.NoRepeatArtistBackToBack,
		Notes:          m.NotesForUser,
		Backend:        a.app.IntentParser().Info().Backend,
	}
}

// GenerateResult is the outcome of GenerateFromPrompt: the playlist plus the
// resolved request so the Playlist screen can keep re-running it as the user
// moves the sliders.
type GenerateResult struct {
	Playlist PlaylistResult       `json:"playlist"`
	Request  BuildPlaylistRequest `json:"request"`
	Notes    string               `json:"notes"`
}

// GenerateFromPrompt parses a prompt, resolves its seed phrases against the
// catalog, and runs the walk.
func (a *API) GenerateFromPrompt(prompt string) (GenerateResult, error) {
	if a.app.IntentParser() == nil || a.app.Reco == nil || a.app.Catalog == nil {
		return GenerateResult{}, errors.New("not ready — download the catalog first")
	}

	m, _ := a.app.IntentParser().Parse(a.context(), ports.IntentInput{Prompt: prompt})
	m = m.Normalized()

	var seedIDs []string
	seen := map[string]struct{}{}
	addID := func(id string) {
		if id == "" {
			return
		}
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		seedIDs = append(seedIDs, id)
	}
	for _, id := range m.Seeds.TrackIDs {
		addID(id)
	}
	for _, q := range m.Seeds.Queries {
		if hits := a.app.Catalog.Resolve(q, 1); len(hits) > 0 {
			addID(hits[0].ID)
		}
	}
	if len(seedIDs) == 0 {
		return GenerateResult{}, fmt.Errorf(
			"couldn't find a seed track for %q — try naming an artist, e.g. \"like Bonobo\"", prompt)
	}

	req := BuildPlaylistRequest{
		SeedIDs:        seedIDs,
		Mode:           string(m.Mode),
		Creativity:     m.Creativity,
		Noise:          m.Noise,
		Lookback:       m.Lookback,
		Count:          m.Count,
		Seed:           0,
		NoRepeatArtist: m.Constraints.NoRepeatArtistBackToBack,
		ArtistsExclude: m.Constraints.ArtistsExclude,
	}

	pl, err := a.runBuild(req)
	if err != nil {
		return GenerateResult{}, err
	}
	req.Seed = pl.Seed // pin the seed the engine chose so re-runs are stable
	return GenerateResult{Playlist: pl, Request: req, Notes: m.NotesForUser}, nil
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
