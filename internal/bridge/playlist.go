package bridge

import (
	"errors"

	"github.com/platten/playlistai/internal/core"
)

// BuildPlaylistRequest is the parameter block for BuildPlaylist. It carries only
// knobs — no natural language — so the frontend can re-run it on every slider
// change without re-parsing an intent.
type BuildPlaylistRequest struct {
	Version      int      `json:"version"`
	ReferenceIDs []string `json:"referenceIds"`
	RequiredIDs  []string `json:"requiredIds"`
	// SeedIDs is the v1 shape retained for saved/generated request compatibility.
	// A v1 request migrates these IDs to both reference and required tracks.
	SeedIDs           []string `json:"seedIds"`
	Mode              string   `json:"mode"` // "", "similar", "journey" ("" auto-picks by seed count)
	Creativity        float64  `json:"creativity"`
	Noise             float64  `json:"noise"`
	Lookback          int      `json:"lookback"`
	Count             int      `json:"count"`
	Seed              int64    `json:"seed"` // 0 → engine picks one and echoes it back
	NoRepeatArtist    bool     `json:"noRepeatArtist"`
	ArtistsExclude    []string `json:"artistsExclude"`
	ExcludeSeedArtist bool     `json:"excludeSeedArtist"`
}

// PlaylistTrack is one row of a generated playlist, with its provenance.
type PlaylistTrack struct {
	ID     string `json:"id"`
	Artist string `json:"artist"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`   // required | nearest | interp
	Detail string `json:"detail"` // human-readable rationale
}

// PlaylistResult is the outcome of BuildPlaylist.
type PlaylistResult struct {
	Tracks  []PlaylistTrack  `json:"tracks"`
	Mode    string           `json:"mode"`
	Seed    int64            `json:"seed"` // the RNG seed actually used (for "regenerate")
	Notices []PlaylistNotice `json:"notices"`
}

type PlaylistNotice struct {
	Code      string `json:"code"`
	Detail    string `json:"detail"`
	Requested int    `json:"requested"`
	Actual    int    `json:"actual"`
}

// BuildPlaylist runs the recommendation walk from an explicit knob set.
func (a *API) BuildPlaylist(req BuildPlaylistRequest) (PlaylistResult, error) {
	return a.runBuild(req)
}

// runBuild is the shared path for BuildPlaylist and GenerateFromPrompt.
func (a *API) runBuild(req BuildPlaylistRequest) (PlaylistResult, error) {
	if a.app.Reco == nil {
		return PlaylistResult{}, errors.New("recommendation engine not ready — load the catalog first")
	}

	req = req.normalized()
	intent := core.MusicIntent{
		Version:    core.CurrentIntentVersion,
		Seeds:      core.IntentSeeds{TrackIDs: req.ReferenceIDs},
		Required:   core.IntentSeeds{TrackIDs: req.RequiredIDs},
		Mode:       core.Mode(req.Mode),
		Count:      req.Count,
		Creativity: req.Creativity,
		Noise:      req.Noise,
		Lookback:   req.Lookback,
		Seed:       req.Seed,
		Constraints: core.IntentConstraints{
			NoRepeatArtistBackToBack: req.NoRepeatArtist,
			ArtistsExclude:           req.ArtistsExclude,
			ExcludeSeedArtists:       req.ExcludeSeedArtist,
		},
	}

	pl, err := a.app.Reco.Build(a.context(), intent)
	if err != nil {
		return PlaylistResult{}, err
	}

	out := PlaylistResult{
		Mode:    string(pl.Mode),
		Seed:    pl.Seed,
		Tracks:  make([]PlaylistTrack, 0, len(pl.Tracks)),
		Notices: make([]PlaylistNotice, 0, len(pl.Notices)),
	}
	for _, notice := range pl.Notices {
		out.Notices = append(out.Notices, PlaylistNotice{
			Code: notice.Code, Detail: notice.Detail, Requested: notice.Requested, Actual: notice.Actual,
		})
	}
	for i, ref := range pl.Tracks {
		pt := PlaylistTrack{ID: ref.ID, Artist: ref.Artist, Title: ref.Title}
		if i < len(pl.Rationale) {
			pt.Kind = pl.Rationale[i].Kind
			pt.Detail = pl.Rationale[i].Detail
		}
		out.Tracks = append(out.Tracks, pt)
	}
	return out, nil
}

func (r BuildPlaylistRequest) normalized() BuildPlaylistRequest {
	if r.Version < core.CurrentIntentVersion {
		if len(r.ReferenceIDs) == 0 && len(r.RequiredIDs) == 0 {
			r.ReferenceIDs = append([]string(nil), r.SeedIDs...)
			r.RequiredIDs = append([]string(nil), r.SeedIDs...)
		}
		r.Version = core.CurrentIntentVersion
	}
	return r
}
