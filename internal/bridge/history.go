package bridge

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/history"
)

// SavedPlaylistSummary is one row of the Generate screen's "start from a past
// playlist" dropdown.
type SavedPlaylistSummary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`   // short title derived from the intent/model
	Prompt     string `json:"prompt"` // the original request — loaded into the box as a starting point
	Notes      string `json:"notes"`
	Mode       string `json:"mode"`
	TrackCount int    `json:"trackCount"`
	CreatedAt  int64  `json:"createdAt"` // unix seconds
}

// ListSavedPlaylists returns previously generated playlists, newest first. It
// returns an empty list (not an error) when history is unavailable so the UI
// can simply hide the option.
func (a *API) ListSavedPlaylists() ([]SavedPlaylistSummary, error) {
	if a.app.History == nil {
		return []SavedPlaylistSummary{}, nil
	}
	recs, err := a.app.History.List(a.context(), 50)
	if err != nil {
		return nil, err
	}
	out := make([]SavedPlaylistSummary, 0, len(recs))
	for _, r := range recs {
		out = append(out, SavedPlaylistSummary{
			ID:         r.ID,
			Name:       r.Name,
			Prompt:     r.Prompt,
			Notes:      r.Notes,
			Mode:       r.Mode,
			TrackCount: r.TrackCount,
			CreatedAt:  r.CreatedAt.Unix(),
		})
	}
	return out, nil
}

// DeleteSavedPlaylist removes one saved playlist by id.
func (a *API) DeleteSavedPlaylist(id string) error {
	if a.app.History == nil {
		return nil
	}
	return a.app.History.Delete(a.context(), id)
}

// saveGenerated persists a just-generated playlist. Best-effort: a failure is
// logged, never surfaced to the caller.
func (a *API) saveGenerated(prompt string, m core.MusicIntent, req BuildPlaylistRequest, pl PlaylistResult) {
	if a.app.History == nil {
		return
	}

	name := deriveTitle(m, prompt)
	if llm := sanitizeTitle(a.app.SuggestTitle(a.context(), prompt, 0)); llm != "" {
		name = llm
	}

	intentJSON, _ := json.Marshal(m)
	reqJSON, _ := json.Marshal(req)
	tracksJSON, _ := json.Marshal(pl.Tracks)

	if _, err := a.app.History.Save(a.context(), history.Record{
		Name:        name,
		Prompt:      strings.TrimSpace(prompt),
		Notes:       m.NotesForUser,
		Mode:        pl.Mode,
		TrackCount:  len(pl.Tracks),
		IntentJSON:  intentJSON,
		RequestJSON: reqJSON,
		TracksJSON:  tracksJSON,
	}); err != nil {
		a.log.Warn("could not save generated playlist to history", "err", err)
	}
}

// deriveTitle builds a short playlist name from the parsed intent, used when the
// model isn't available to name it. Examples: "Like Bonobo", "Justice → Kavinsky",
// "Weekend Wind-down" (from a seedless prompt).
func deriveTitle(m core.MusicIntent, prompt string) string {
	seeds := make([]string, 0, len(m.Seeds.Queries))
	for _, s := range m.Seeds.Queries {
		if s = strings.TrimSpace(s); s != "" {
			seeds = append(seeds, s)
		}
	}

	switch {
	case len(seeds) >= 2 && m.Mode == core.ModeJourney:
		return clip(strings.Join(seeds, " → "), 48)
	case len(seeds) >= 1:
		return clip("Like "+strings.Join(seeds, ", "), 48)
	default:
		return firstWords(prompt, 6)
	}
}

// sanitizeTitle cleans a model-produced title: first line only, no surrounding
// quotes, collapsed whitespace, trimmed trailing punctuation, length-capped.
func sanitizeTitle(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.Trim(s, `"'“”‘’ `)
	s = strings.Join(strings.Fields(s), " ")
	s = strings.TrimRight(s, ".!,;:—- ")
	return clip(s, 48)
}

func firstWords(s string, n int) string {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) == 0 {
		return "Untitled playlist"
	}
	if len(fields) > n {
		fields = fields[:n]
	}
	out := strings.Join(fields, " ")
	r := []rune(out)
	r[0] = unicode.ToUpper(r[0])
	return clip(string(r), 48)
}

func clip(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max-1])) + "…"
}
