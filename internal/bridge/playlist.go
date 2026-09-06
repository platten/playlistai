package bridge

import (
	"context"
	"errors"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// ControlOverrides contains only controls explicitly changed after parsing.
// Nil means "keep the resolved intent value".
type ControlOverrides struct {
	TotalTrackCount      *int          `json:"totalTrackCount,omitempty"`
	AudioWeight          *float64      `json:"audioWeight,omitempty"`
	CooccurrenceWeight   *float64      `json:"cooccurrenceWeight,omitempty"`
	Discovery            *float64      `json:"discovery,omitempty"`
	ArtistDiversity      *float64      `json:"artistDiversity,omitempty"`
	TransitionSmoothness *float64      `json:"transitionSmoothness,omitempty"`
	ExcludeSeedArtists   *bool         `json:"excludeSeedArtists,omitempty"`
	Seed                 *core.RNGSeed `json:"seed,omitempty"`
}

// BuildPlaylistRequest carries the complete resolved interpretation plus
// explicit UI overrides. Legacy fields remain for old history records.
type BuildPlaylistRequest struct {
	Version         int              `json:"version"`
	Intent          core.MusicIntent `json:"intent"`
	Overrides       ControlOverrides `json:"overrides"`
	Reproducibility Reproducibility  `json:"reproducibility"`
	SessionID       string           `json:"sessionId"`
	RequestID       string           `json:"requestId"`

	ReferenceIDs      []string     `json:"referenceIds,omitempty"`
	RequiredIDs       []string     `json:"requiredIds,omitempty"`
	SeedIDs           []string     `json:"seedIds,omitempty"`
	Mode              string       `json:"mode,omitempty"`
	Creativity        float64      `json:"creativity,omitempty"`
	Noise             float64      `json:"noise,omitempty"`
	Lookback          int          `json:"lookback,omitempty"`
	Count             int          `json:"count,omitempty"`
	Seed              core.RNGSeed `json:"seed,omitempty"`
	NoRepeatArtist    bool         `json:"noRepeatArtist,omitempty"`
	ArtistsExclude    []string     `json:"artistsExclude,omitempty"`
	ExcludeSeedArtist bool         `json:"excludeSeedArtist,omitempty"`
}

type PlaylistTrack struct {
	ID       string                   `json:"id"`
	Artist   string                   `json:"artist"`
	Title    string                   `json:"title"`
	Kind     string                   `json:"kind"`
	Detail   string                   `json:"detail"`
	Sources  []core.RetrievalEvidence `json:"sources"`
	Evidence []core.ComponentEvidence `json:"evidence"`
}

type PlaylistResult struct {
	Tracks          []PlaylistTrack  `json:"tracks"`
	Mode            string           `json:"mode"`
	Seed            core.RNGSeed     `json:"seed"`
	Notices         []PlaylistNotice `json:"notices"`
	Intent          core.MusicIntent `json:"intent"`
	Status          GenerationStatus `json:"status"`
	Reproducibility Reproducibility  `json:"reproducibility"`
}

type PlaylistNotice struct {
	Code      string `json:"code"`
	Detail    string `json:"detail"`
	Requested int    `json:"requested"`
	Actual    int    `json:"actual"`
}

func (a *API) BuildPlaylist(ctx context.Context, req BuildPlaylistRequest) (PlaylistResult, error) {
	ctx, current, finish := a.operations.begin(ctx, "playlist-build")
	defer finish()
	result, err := a.runBuild(ctx, req)
	if err == nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return PlaylistResult{}, contextErr
		}
		if !current() {
			return PlaylistResult{}, context.Canceled
		}
	}
	return result, err
}

func (a *API) runBuild(ctx context.Context, req BuildPlaylistRequest) (PlaylistResult, error) {
	if a.app.Reco == nil {
		return PlaylistResult{}, errors.New("recommendation engine not ready — load the catalog first")
	}
	intent := req.resolvedIntent()
	intent = applyOverrides(intent, req.Overrides)
	if err := intent.Validate(); err != nil {
		return PlaylistResult{}, err
	}
	intent = intent.Normalized()

	profileStarted := time.Now()
	profile, err := a.generationTasteProfile(ctx, req.SessionID, req.RequestID)
	if err != nil {
		return PlaylistResult{}, err
	}
	profileTiming := StageTiming{Stage: "profile", Milliseconds: time.Since(profileStarted).Milliseconds()}
	started := time.Now()
	var playlist core.Playlist
	if personalized, ok := a.app.Reco.(ports.PersonalizedRecommendationEngine); ok {
		playlist, err = personalized.BuildWithProfile(ctx, intent, profile)
	} else {
		playlist, err = a.app.Reco.Build(ctx, intent)
	}
	if err != nil {
		return PlaylistResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return PlaylistResult{}, err
	}
	out := PlaylistResult{
		Mode: string(playlist.Mode), Seed: playlist.Seed, Intent: playlist.Intent,
		Tracks:  make([]PlaylistTrack, 0, len(playlist.Tracks)),
		Notices: make([]PlaylistNotice, 0, len(playlist.Notices)),
	}
	for _, notice := range playlist.Notices {
		out.Notices = append(out.Notices, PlaylistNotice{
			Code: notice.Code, Detail: notice.Detail, Requested: notice.Requested, Actual: notice.Actual,
		})
	}
	for index, ref := range playlist.Tracks {
		track := PlaylistTrack{
			ID: ref.ID, Artist: ref.Artist, Title: ref.Title,
			Sources: []core.RetrievalEvidence{}, Evidence: []core.ComponentEvidence{},
		}
		if index < len(playlist.Rationale) {
			track.Kind = playlist.Rationale[index].Kind
			track.Detail = playlist.Rationale[index].Detail
			track.Sources = append(track.Sources, playlist.Rationale[index].Sources...)
			track.Evidence = append(track.Evidence, playlist.Rationale[index].Evidence...)
		}
		out.Tracks = append(out.Tracks, track)
	}
	out.Status = GenerationStatus{
		State: "complete", PartialReasons: []PlaylistNotice{},
		Timings: []StageTiming{profileTiming, {
			Stage: "recommend", Milliseconds: time.Since(started).Milliseconds(),
		}},
	}
	if len(out.Tracks) < out.Intent.Count {
		out.Status.State = "partial"
		out.Status.PartialReasons = append(out.Status.PartialReasons, out.Notices...)
		if len(out.Status.PartialReasons) == 0 {
			reason := PlaylistNotice{
				Code: "partial_result", Detail: "generation ended before the requested total was reached",
				Requested: out.Intent.Count, Actual: len(out.Tracks),
			}
			out.Notices = append(out.Notices, reason)
			out.Status.PartialReasons = append(out.Status.PartialReasons, reason)
		}
	}
	catalogVersion := "unknown"
	if a.app.Resolver != nil {
		catalogVersion = a.app.Resolver.CatalogVersion()
	}
	out.Reproducibility, err = generationIdentity(out.Intent, catalogVersion, a.recommendationVersion(), profile.AlgorithmVersion, profile.SnapshotID)
	if err != nil {
		return PlaylistResult{}, err
	}
	a.recordExposures(ctx, req, out)
	a.log.Info("playlist generation completed", "state", out.Status.State, "tracks", len(out.Tracks),
		"profile_ms", profileTiming.Milliseconds, "recommend_ms", out.Status.Timings[1].Milliseconds)
	return out, nil
}

func (r BuildPlaylistRequest) normalized() BuildPlaylistRequest {
	r.Intent = r.resolvedIntent().Normalized()
	r.Version = core.CurrentIntentVersion
	return r
}

func (r BuildPlaylistRequest) resolvedIntent() core.MusicIntent {
	// V3 introduced the complete typed intent. Later versions only add fields,
	// so any v3+ request must use its embedded intent rather than legacy knobs.
	if r.Version >= 3 && r.Intent.Version != 0 {
		return r.Intent
	}
	references := r.ReferenceIDs
	required := r.RequiredIDs
	version := r.Version
	if len(references) == 0 && len(required) == 0 {
		references = append([]string(nil), r.SeedIDs...)
		required = append([]string(nil), r.SeedIDs...)
		version = 1
	}
	return core.MusicIntent{
		Version:  version,
		Seeds:    core.IntentSeeds{TrackIDs: references},
		Required: core.IntentSeeds{TrackIDs: required},
		Mode:     core.Mode(r.Mode), Count: r.Count, Creativity: r.Creativity,
		Noise: r.Noise, Lookback: r.Lookback, Seed: r.Seed,
		Constraints: core.IntentConstraints{
			NoRepeatArtistBackToBack: r.NoRepeatArtist,
			ArtistsExclude:           r.ArtistsExclude,
			ExcludeSeedArtists:       r.ExcludeSeedArtist,
		},
	}.Normalized()
}

func applyOverrides(intent core.MusicIntent, overrides ControlOverrides) core.MusicIntent {
	if overrides.TotalTrackCount != nil {
		intent.Controls.TotalTrackCount = *overrides.TotalTrackCount
	}
	if overrides.AudioWeight != nil {
		intent.Controls.AudioWeight = *overrides.AudioWeight
	}
	if overrides.CooccurrenceWeight != nil {
		intent.Controls.CooccurrenceWeight = *overrides.CooccurrenceWeight
	}
	if overrides.Discovery != nil {
		intent.Controls.Discovery = *overrides.Discovery
	}
	if overrides.ArtistDiversity != nil {
		intent.Controls.ArtistDiversity = *overrides.ArtistDiversity
	}
	if overrides.TransitionSmoothness != nil {
		intent.Controls.TransitionSmoothness = *overrides.TransitionSmoothness
	}
	if overrides.Seed != nil {
		intent.Seed = *overrides.Seed
	}
	if overrides.ExcludeSeedArtists != nil {
		setHardConstraint(&intent, "exclude_reference_artists", *overrides.ExcludeSeedArtists)
	}
	return intent
}

func setHardConstraint(intent *core.MusicIntent, kind string, enabled bool) {
	filtered := make([]core.HardConstraint, 0, len(intent.HardConstraints))
	for _, constraint := range intent.HardConstraints {
		if constraint.Kind != kind {
			filtered = append(filtered, constraint)
		}
	}
	intent.HardConstraints = filtered
	if enabled {
		intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{
			Kind: kind, Value: "true", Supported: true,
		})
	}
}
