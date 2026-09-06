package bridge

import (
	"encoding/json"
	"fmt"
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

// SavedPlaylist is a loadable, migrated history record. Request contains the
// complete resolved intent so replay does not require parsing the prompt again.
type SavedPlaylist struct {
	Summary SavedPlaylistSummary `json:"summary"`
	Request BuildPlaylistRequest `json:"request"`
	Tracks  []PlaylistTrack      `json:"tracks"`
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

// LoadSavedPlaylist reads and migrates v1-v3 JSON blobs into the current contract.
func (a *API) LoadSavedPlaylist(id string) (SavedPlaylist, error) {
	if a.app.History == nil {
		return SavedPlaylist{}, fmt.Errorf("saved playlist %q not found", id)
	}
	record, ok, err := a.app.History.Get(a.context(), id)
	if err != nil {
		return SavedPlaylist{}, err
	}
	if !ok {
		return SavedPlaylist{}, fmt.Errorf("saved playlist %q not found", id)
	}
	var request BuildPlaylistRequest
	if err := json.Unmarshal(record.RequestJSON, &request); err != nil {
		return SavedPlaylist{}, fmt.Errorf("decode saved request: %w", err)
	}
	if request.Intent.Version == 0 && request.Version == 0 &&
		len(request.ReferenceIDs) == 0 && len(request.RequiredIDs) == 0 && len(request.SeedIDs) == 0 &&
		len(record.IntentJSON) > 0 {
		var intent core.MusicIntent
		if err := json.Unmarshal(record.IntentJSON, &intent); err != nil {
			return SavedPlaylist{}, fmt.Errorf("decode saved intent: %w", err)
		}
		request.Intent = intent.Normalized()
		request.Version = core.CurrentIntentVersion
	}
	request = request.normalized()
	var tracks []PlaylistTrack
	if err := json.Unmarshal(record.TracksJSON, &tracks); err != nil {
		return SavedPlaylist{}, fmt.Errorf("decode saved tracks: %w", err)
	}
	return SavedPlaylist{
		Summary: SavedPlaylistSummary{
			ID: record.ID, Name: record.Name, Prompt: record.Prompt, Notes: record.Notes,
			Mode: record.Mode, TrackCount: record.TrackCount, CreatedAt: record.CreatedAt.Unix(),
		},
		Request: request,
		Tracks:  orEmptyTracks(tracks),
	}, nil
}

// playlistName produces a short (<= 6 words) label for a generated playlist:
// the local model's title when one is available, otherwise one derived from the
// parsed intent.
func (a *API) playlistName(prompt string, m core.MusicIntent) string {
	name := deriveTitle(m, prompt)
	if llm := sanitizeTitle(a.app.SuggestTitle(a.context(), prompt, 0)); llm != "" {
		name = llm
	}
	return clampWords(name, 6)
}

// saveGenerated persists a just-generated playlist. Best-effort: a failure is
// logged, never surfaced to the caller.
func (a *API) saveGenerated(name, prompt string, m core.MusicIntent, req BuildPlaylistRequest, pl PlaylistResult) {
	if a.app.History == nil {
		return
	}

	m = m.Normalized()
	req = req.normalized()
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

func orEmptyTracks(tracks []PlaylistTrack) []PlaylistTrack {
	if tracks == nil {
		return []PlaylistTrack{}
	}
	return tracks
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

// clampWords keeps at most the first n whitespace-separated words of s.
func clampWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) <= n {
		return strings.TrimSpace(s)
	}
	return strings.Join(f[:n], " ")
}
