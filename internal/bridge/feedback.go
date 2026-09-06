package bridge

import (
	"context"
	"errors"
	"fmt"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/taste"
)

type RecordFeedbackRequest struct {
	Type      core.FeedbackType    `json:"type"`
	Scope     core.FeedbackScope   `json:"scope"`
	TrackID   string               `json:"trackId"`
	RequestID string               `json:"requestId"`
	SessionID string               `json:"sessionId"`
	Context   core.FeedbackContext `json:"context"`
}

type TasteProfileSummary struct {
	Version          int    `json:"version"`
	AlgorithmVersion string `json:"algorithmVersion"`
	SnapshotID       string `json:"snapshotId"`
	CatalogVersion   string `json:"catalogVersion"`
	ColdStart        bool   `json:"coldStart"`
	PositiveEvidence int    `json:"positiveEvidence"`
	NegativeEvidence int    `json:"negativeEvidence"`
	RequestEvidence  int    `json:"requestEvidence"`
	ExposureCount    int    `json:"exposureCount"`
	ClusterCount     int    `json:"clusterCount"`
}

type FeedbackReceipt struct {
	EventID string              `json:"eventId"`
	Profile TasteProfileSummary `json:"profile"`
}

type RecordAcceptanceRequest struct {
	TrackIDs  []string `json:"trackIds"`
	RequestID string   `json:"requestId"`
	SessionID string   `json:"sessionId"`
}

type FeedbackBatchReceipt struct {
	EventCount int                 `json:"eventCount"`
	Profile    TasteProfileSummary `json:"profile"`
}

func (a *API) RecordFeedback(ctx context.Context, request RecordFeedbackRequest) (FeedbackReceipt, error) {
	if a.app.Feedback == nil {
		return FeedbackReceipt{}, errors.New("local feedback storage is unavailable")
	}
	if a.app.Catalog == nil {
		return FeedbackReceipt{}, errors.New("catalog not loaded")
	}
	if _, ok := a.app.Catalog.Meta(request.TrackID); !ok {
		return FeedbackReceipt{}, fmt.Errorf("feedback: unknown catalog track %q", request.TrackID)
	}
	if request.Type == core.FeedbackExposure {
		return FeedbackReceipt{}, errors.New("feedback: exposures are recorded only by playlist generation")
	}
	if request.Scope == "" {
		request.Scope = defaultFeedbackScope(request.Type)
	}
	event, err := a.app.Feedback.RecordFeedback(ctx, core.FeedbackEvent{
		Type: request.Type, Scope: request.Scope, TrackID: request.TrackID,
		RequestID: request.RequestID, SessionID: request.SessionID, Context: request.Context,
		Versions: a.feedbackVersions(),
	})
	if err != nil {
		return FeedbackReceipt{}, err
	}
	profile, err := a.tasteProfile(ctx, request.SessionID, request.RequestID)
	if err != nil {
		return FeedbackReceipt{}, err
	}
	return FeedbackReceipt{EventID: event.ID, Profile: profileSummary(profile)}, nil
}

func defaultFeedbackScope(kind core.FeedbackType) core.FeedbackScope {
	switch kind {
	case core.FeedbackLike, core.FeedbackDislike:
		return core.FeedbackScopeDurable
	default:
		return core.FeedbackScopeRequest
	}
}

// RecordTrackAcceptance records only tracks included when the user explicitly
// confirms an export. Opening or generating a playlist never calls this API.
func (a *API) RecordTrackAcceptance(ctx context.Context, request RecordAcceptanceRequest) (FeedbackBatchReceipt, error) {
	if a.app.Feedback == nil {
		return FeedbackBatchReceipt{}, errors.New("local feedback storage is unavailable")
	}
	if a.app.Catalog == nil {
		return FeedbackBatchReceipt{}, errors.New("catalog not loaded")
	}
	seen := make(map[string]struct{}, len(request.TrackIDs))
	events := make([]core.FeedbackEvent, 0, len(request.TrackIDs))
	versions := a.feedbackVersions()
	for position, trackID := range request.TrackIDs {
		if _, duplicate := seen[trackID]; duplicate {
			continue
		}
		seen[trackID] = struct{}{}
		if _, ok := a.app.Catalog.Meta(trackID); !ok {
			return FeedbackBatchReceipt{}, fmt.Errorf("feedback: unknown catalog track %q", trackID)
		}
		events = append(events, core.FeedbackEvent{
			Type: core.FeedbackAccepted, Scope: core.FeedbackScopeRequest, TrackID: trackID,
			RequestID: request.RequestID, SessionID: request.SessionID,
			Context: core.FeedbackContext{Surface: "export", Position: position}, Versions: versions,
		})
	}
	if err := a.app.Feedback.RecordFeedbackBatch(ctx, events); err != nil {
		return FeedbackBatchReceipt{}, err
	}
	profile, err := a.tasteProfile(ctx, request.SessionID, request.RequestID)
	if err != nil {
		return FeedbackBatchReceipt{}, err
	}
	return FeedbackBatchReceipt{EventCount: len(events), Profile: profileSummary(profile)}, nil
}

func (a *API) GetTasteProfile(ctx context.Context, sessionID, requestID string) (TasteProfileSummary, error) {
	profile, err := a.tasteProfile(ctx, sessionID, requestID)
	if err != nil {
		return TasteProfileSummary{}, err
	}
	return profileSummary(profile), nil
}

func (a *API) ClearTasteData(ctx context.Context) error {
	if a.app.Feedback != nil {
		if err := a.app.Feedback.ClearFeedback(ctx); err != nil {
			return err
		}
	}
	if a.app.Profiles != nil {
		if err := a.app.Profiles.ClearProfiles(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) tasteProfile(ctx context.Context, sessionID, requestID string) (core.TasteProfile, error) {
	return a.buildTasteProfile(ctx, sessionID, requestID, false)
}

func (a *API) buildTasteProfile(ctx context.Context, sessionID, requestID string, includeAllExposures bool) (core.TasteProfile, error) {
	var events []core.FeedbackEvent
	if a.app.Feedback != nil {
		var err error
		events, err = a.app.Feedback.ListFeedback(ctx, ports.FeedbackQuery{
			RequestID: requestID, SessionID: sessionID, IncludeExposures: includeAllExposures,
		})
		if err != nil {
			return core.TasteProfile{}, err
		}
	}
	profile, err := taste.BuildProfile(ctx, a.app.Catalog, events, taste.ProfileOptions{
		RequestID: requestID, SessionID: sessionID, IncludeAllExposures: includeAllExposures,
	})
	if err != nil {
		return core.TasteProfile{}, err
	}
	if a.app.Profiles != nil {
		if err := a.app.Profiles.SaveProfile(ctx, profile); err != nil {
			a.log.Warn("could not persist taste profile snapshot", "err", err)
		}
	}
	return profile, nil
}

// generationTasteProfile keeps the existing non-personalized recommender
// available if optional local profile storage becomes unreadable. Explicit
// feedback APIs still surface storage errors to the user.
func (a *API) generationTasteProfile(ctx context.Context, sessionID, requestID string) (core.TasteProfile, error) {
	profile, err := a.buildTasteProfile(ctx, sessionID, requestID, true)
	if err == nil {
		return profile, nil
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return core.TasteProfile{}, contextErr
	}
	a.log.Warn("taste profile unavailable for generation", "err", err)
	return taste.BuildProfile(ctx, a.app.Catalog, nil, taste.ProfileOptions{
		RequestID: requestID, SessionID: sessionID,
	})
}

func (a *API) recordExposures(ctx context.Context, request BuildPlaylistRequest, result PlaylistResult) {
	if a.app.Feedback == nil || len(result.Tracks) == 0 || ctx.Err() != nil {
		return
	}
	versions := a.feedbackVersions()
	events := make([]core.FeedbackEvent, 0, len(result.Tracks))
	for position, track := range result.Tracks {
		requestID := request.RequestID
		if requestID == "" {
			requestID = result.Reproducibility.ID
		}
		events = append(events, core.FeedbackEvent{
			Type: core.FeedbackExposure, Scope: core.FeedbackScopeRequest, TrackID: track.ID,
			RequestID: requestID, SessionID: request.SessionID,
			Context: core.FeedbackContext{
				Surface: "generation", Position: position, RationaleKind: track.Kind,
			},
			Versions: versions,
		})
	}
	if err := a.app.Feedback.RecordFeedbackBatch(ctx, events); err != nil {
		a.log.Warn("could not persist recommendation exposures", "err", err, "count", len(events))
	}
}

func (a *API) feedbackVersions() core.FeedbackVersions {
	return core.FeedbackVersions{
		Catalog: a.catalogVersion(), Recommendation: a.recommendationVersion(),
		IntentSchema: schema.Version, Profile: taste.ProfileAlgorithmVersion,
	}
}

func (a *API) catalogVersion() string {
	if a.app.Resolver == nil {
		return "unknown"
	}
	return a.app.Resolver.CatalogVersion()
}

func (a *API) recommendationVersion() string {
	if versioned, ok := a.app.Reco.(ports.VersionedRecommendationEngine); ok {
		return versioned.AlgorithmVersion()
	}
	return defaultRecommendationAlgorithmVersion
}

func profileSummary(profile core.TasteProfile) TasteProfileSummary {
	return TasteProfileSummary{
		Version: profile.Version, AlgorithmVersion: profile.AlgorithmVersion, SnapshotID: profile.SnapshotID,
		CatalogVersion: profile.CatalogVersion, ColdStart: profile.ColdStart,
		PositiveEvidence: profile.PositiveEvidence, NegativeEvidence: profile.NegativeEvidence,
		RequestEvidence: profile.RequestEvidence, ExposureCount: profile.ExposureCount,
		ClusterCount: len(profile.Clusters),
	}
}
