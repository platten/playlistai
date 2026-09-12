package bridge

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/logging"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/deejai"
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
	EnhancedAudio    *core.EnhancedAudioInput `json:"enhancedAudio,omitempty"`
	GenerationID     string                   `json:"generationId"`
	Version          int                      `json:"version"`
	Intent           core.MusicIntent         `json:"intent"`
	Overrides        ControlOverrides         `json:"overrides"`
	Reproducibility  Reproducibility          `json:"reproducibility"`
	SessionID        string                   `json:"sessionId"`
	RequestID        string                   `json:"requestId"`
	RecentSelections []core.TrackRef          `json:"recentSelections,omitempty"`

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
	EnhancedAudio *core.EnhancedAudioInput `json:"enhancedAudio,omitempty"`
	// PresentationID identifies this delivery, not the deterministic generation.
	// Exposure is recorded only after the frontend acknowledges displaying it.
	PresentationID  string                      `json:"presentationId"`
	Assessments     []core.TrackAssessment      `json:"assessments"`
	GenerationID    string                      `json:"generationId"`
	AudioEvidence   *core.AudioEvidenceSnapshot `json:"audioEvidence,omitempty"`
	Tracks          []PlaylistTrack             `json:"tracks"`
	Mode            string                      `json:"mode"`
	Seed            core.RNGSeed                `json:"seed"`
	Notices         []PlaylistNotice            `json:"notices"`
	Intent          core.MusicIntent            `json:"intent"`
	Status          GenerationStatus            `json:"status"`
	Outcome         core.GenerationOutcome      `json:"outcome"`
	Reproducibility Reproducibility             `json:"reproducibility"`
}

type PlaylistNotice struct {
	Code      string `json:"code"`
	Detail    string `json:"detail"`
	Requested int    `json:"requested"`
	Actual    int    `json:"actual"`
}

func (a *API) BuildPlaylist(ctx context.Context, req BuildPlaylistRequest) (PlaylistResult, error) {
	ctx, release := a.app.OperationContext(ctx)
	defer release()
	ctx = a.diagnosticContext(ctx)
	a.operations.cancel("intent-preview")
	ctx, current, finish := a.operations.begin(ctx, generationOperation)
	defer finish()
	ctx, finishGeneration := a.beginGeneration(ctx, req.GenerationID)
	defer finishGeneration()
	result, err := a.runBuild(ctx, req)
	if err != nil {
		logging.Diagnostic(ctx, "generation.error", err.Error())
	}
	if err == nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return PlaylistResult{}, contextErr
		}
		if !current() {
			return PlaylistResult{}, context.Canceled
		}
		a.preparePresentation(req, &result)
	}
	return result, err
}

func (a *API) runBuild(ctx context.Context, req BuildPlaylistRequest) (PlaylistResult, error) {
	if err := ctx.Err(); err != nil {
		return PlaylistResult{}, err
	}
	if a.runtime().Reco == nil {
		return PlaylistResult{}, errors.New("recommendation engine not ready — load the catalog first")
	}
	intent := req.resolvedIntent()
	intent = applyOverrides(intent, req.Overrides)
	if intent.Controls.RecommendationMode == "" {
		intent.Controls.RecommendationMode = a.app.RecommendationMode()
	}
	if err := intent.Validate(); err != nil {
		return PlaylistResult{}, err
	}
	intent = intent.Normalized()
	logging.Diagnostic(ctx, "recommendation.request", intent)
	if len(req.RecentSelections) > 0 {
		logging.Diagnostic(ctx, "recommendation.recent_selections", req.RecentSelections)
	}

	profileStarted := time.Now()
	profile, err := a.profileForBuild(ctx, req, intent)
	if err != nil {
		return PlaylistResult{}, err
	}
	profileTiming := StageTiming{Stage: "profile", Milliseconds: time.Since(profileStarted).Milliseconds()}
	recentSelections := resolveRecentSelections(a.runtime().Catalog, req.RecentSelections)
	started := time.Now()
	var playlist core.Playlist
	progress := generationProgress(ctx)
	var stop <-chan struct{}
	if g := generationFromContext(ctx); g != nil {
		stop = g.stop
	}
	if intent.Controls.RecommendationMode == core.DeejAIOnly {
		playlist, err = deejai.BuildOnly(ctx, a.runtime().BaselineReco, intent)
	} else if contextual, ok := a.runtime().Reco.(ports.ContextualRecommendationEngine); ok {
		var enhanced *core.EnhancedAudioSnapshot
		if req.EnhancedAudio != nil && intent.Controls.RecommendationMode == core.EnhancedHybrid {
			if req.EnhancedAudio.CatalogVersion != "" && a.runtime().Resolver != nil && req.EnhancedAudio.CatalogVersion != a.runtime().Resolver.CatalogVersion() {
				return PlaylistResult{}, fmt.Errorf("saved enhanced evidence belongs to another catalog; start a new generation")
			}
			enhanced, err = core.NewEnhancedAudioSnapshot(*req.EnhancedAudio)
			if err != nil {
				return PlaylistResult{}, err
			}
		}
		playlist, err = contextual.BuildRecommendation(ctx, ports.RecommendationRequest{
			EnhancedAudio: enhanced,
			StopChecking:  stop, OnChecked: progress.Checked, OnSuggested: progress.Suggested, Progress: progress,
			Intent: intent, Profile: profile, RecentSelections: recentSelections,
		})
	} else if personalized, ok := a.runtime().Reco.(ports.PersonalizedRecommendationEngine); ok {
		playlist, err = personalized.BuildWithProfile(ctx, intent, profile)
	} else {
		playlist, err = a.runtime().Reco.Build(ctx, intent)
	}
	if err != nil {
		return PlaylistResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return PlaylistResult{}, err
	}
	out := PlaylistResult{
		GenerationID: progress.generationID, AudioEvidence: playlist.AudioEvidence, Assessments: playlist.Assessments,
		Mode: string(playlist.Mode), Seed: playlist.Seed, Intent: playlist.Intent,
		Outcome: playlist.Outcome,
		Tracks:  make([]PlaylistTrack, 0, len(playlist.Tracks)),
		Notices: make([]PlaylistNotice, 0, len(playlist.Notices)),
	}
	for _, notice := range playlist.Notices {
		out.Notices = append(out.Notices, PlaylistNotice{
			Code: notice.Code, Detail: notice.Detail, Requested: notice.Requested, Actual: notice.Actual,
		})
	}
	if playlist.EnhancedAudio != nil {
		input := playlist.EnhancedAudio.Input()
		out.EnhancedAudio = &input
	}
	if playlist.Intent.Knowledge != nil {
		for i, detail := range playlist.Intent.Knowledge.Notices {
			out.Notices = append(out.Notices, PlaylistNotice{Code: fmt.Sprintf("music_lookup_%d", i), Detail: detail})
		}
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
	out.Outcome = core.ReconcileOutcome(out.Outcome, out.Intent, len(out.Tracks))
	a.presentPlaylistNotices(&out)
	out.Status = GenerationStatus{
		State: string(out.Outcome.State), Reasons: append([]core.OutcomeReason(nil), out.Outcome.Reasons...), PartialReasons: []PlaylistNotice{},
		Timings: []StageTiming{profileTiming, {
			Stage: "recommend", Milliseconds: time.Since(started).Milliseconds(),
		}},
	}
	if out.Outcome.State == core.OutcomePartial {
		out.Status.PartialReasons = append(out.Status.PartialReasons, out.Notices...)
		if len(out.Status.PartialReasons) == 0 {
			reason := partialResultNotice(out)
			out.Notices = append(out.Notices, reason)
			out.Status.PartialReasons = append(out.Status.PartialReasons, reason)
		}
	}
	catalogVersion := "unknown"
	if a.runtime().Resolver != nil {
		catalogVersion = a.runtime().Resolver.CatalogVersion()
	}
	out.Reproducibility, err = generationIdentity(out.Intent, catalogVersion, a.recommendationVersionFor(intent), profile.AlgorithmVersion, profile.SnapshotID, recentSelections)
	if err != nil {
		return PlaylistResult{}, err
	}
	withEvidenceIdentity(&out.Reproducibility, out.AudioEvidence)
	if playlist.EnhancedAudio != nil {
		out.Reproducibility.ID = audioIdentity(out.Reproducibility.ID, playlist.EnhancedAudio.Fingerprint())
		out.Reproducibility.EnhancedEvidenceSnapshot = playlist.EnhancedAudio.Fingerprint()
	}
	logRecommendationDiagnostics(ctx, out)
	a.log.Info("playlist generation completed", "state", out.Status.State, "tracks", len(out.Tracks),
		"profile_ms", profileTiming.Milliseconds, "recommend_ms", out.Status.Timings[1].Milliseconds)
	return out, nil
}

func logRecommendationDiagnostics(ctx context.Context, result PlaylistResult) {
	logging.Diagnostic(ctx, "recommendation.result", result.Outcome)
	logging.Diagnostic(ctx, "recommendation.reproducibility", result.Reproducibility)
	for _, track := range result.Tracks {
		logging.Diagnostic(ctx, "recommendation.pick", track)
	}
	for _, assessment := range result.Assessments {
		logging.Diagnostic(ctx, "analysis.acousticbrainz_comparison", assessment)
	}
	if result.Intent.Knowledge != nil {
		for _, track := range result.Intent.Knowledge.Tracks {
			if track.Acoustic != nil {
				logging.Diagnostic(ctx, "analysis.acousticbrainz_features", track)
			}
		}
	}
	if result.AudioEvidence != nil {
		for _, assessment := range result.AudioEvidence.Assessments {
			logging.Diagnostic(ctx, "analysis.clap", assessment)
		}
		logging.Diagnostic(ctx, "analysis.clap_summary", result.AudioEvidence)
	}
}

func resolveRecentSelections(catalog ports.Catalog, tracks []core.TrackRef) []core.TrackRef {
	result := make([]core.TrackRef, 0, len(tracks))
	seen := map[string]struct{}{}
	for _, track := range tracks {
		if catalog != nil {
			if meta, ok := catalog.Meta(track.ID); ok {
				track = meta.Ref
			}
		}
		key := core.ProvisionalRecordingKey(track)
		if key == "\x00" {
			key = "id\x00" + track.ID
		}
		if track.ID == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, track)
	}
	return result
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

func (a *API) profileForBuild(ctx context.Context, req BuildPlaylistRequest, intent core.MusicIntent) (core.TasteProfile, error) {
	if intent.Controls.RecommendationMode == core.DeejAIOnly {
		return core.TasteProfile{}, nil
	}
	identity := req.Reproducibility
	fingerprint, err := generationIdentity(intent, identity.CatalogVersion, identity.AlgorithmVersion, identity.ProfileVersion, identity.ProfileSnapshot, resolveRecentSelections(a.runtime().Catalog, req.RecentSelections))
	if err != nil {
		return core.TasteProfile{}, err
	}
	if identity.ProfileSnapshot == "" || fingerprint.IntentFingerprint != identity.IntentFingerprint || fingerprint.ContextFingerprint != identity.ContextFingerprint {
		return a.generationTasteProfile(ctx, req.SessionID, req.RequestID)
	}
	if identity.CatalogVersion != a.catalogVersion() || identity.AlgorithmVersion != a.recommendationVersionFor(intent) {
		return core.TasteProfile{}, errors.New("saved generation uses a different catalog or recommendation version; regenerate to use the current versions")
	}
	if a.app.Profiles == nil {
		return core.TasteProfile{}, errors.New("saved taste snapshot is unavailable; regenerate to use current preferences")
	}
	profile, found, err := a.app.Profiles.ProfileByID(ctx, identity.ProfileSnapshot)
	if err != nil {
		return core.TasteProfile{}, err
	}
	if !found || profile.CatalogVersion != identity.CatalogVersion || profile.AlgorithmVersion != identity.ProfileVersion {
		return core.TasteProfile{}, errors.New("saved taste snapshot is missing or incompatible; regenerate to use current preferences")
	}
	return profile, nil
}

func applyOverrides(intent core.MusicIntent, overrides ControlOverrides) core.MusicIntent {
	original := intent.Normalized()
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
	// Legacy discovery snapshots lack an input key. Explicit control changes
	// still invalidate their sampled sequence, while keeping cached identities.
	if intent.Knowledge != nil && !reflect.DeepEqual(original, intent.Normalized()) {
		snapshot := *intent.Knowledge
		snapshot.DiscoveryRecorded = false
		snapshot.Discovery = nil
		snapshot.DiscoveryKey = ""
		snapshot.DiscoveryEvidence = nil
		intent.Knowledge = &snapshot
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
