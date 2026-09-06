package core

import (
	"fmt"
	"strings"
	"time"
)

const FeedbackEventVersion = 1

type FeedbackType string

const (
	FeedbackLike     FeedbackType = "like"
	FeedbackDislike  FeedbackType = "dislike"
	FeedbackMoreLike FeedbackType = "more_like"
	FeedbackLessLike FeedbackType = "less_like"
	FeedbackAccepted FeedbackType = "accepted"
	FeedbackRemoved  FeedbackType = "removed"
	FeedbackExposure FeedbackType = "exposure"
)

type FeedbackScope string

const (
	FeedbackScopeDurable FeedbackScope = "durable"
	FeedbackScopeRequest FeedbackScope = "request"
)

// FeedbackContext records where an explicit interaction occurred without
// retaining prompt text or preview-playback telemetry.
type FeedbackContext struct {
	Surface       string `json:"surface"`
	Position      int    `json:"position"`
	RationaleKind string `json:"rationaleKind"`
}

type FeedbackVersions struct {
	Catalog        string `json:"catalog"`
	Recommendation string `json:"recommendation"`
	IntentSchema   int    `json:"intentSchema"`
	Profile        string `json:"profile"`
}

// FeedbackEvent is an append-only local record. Exposure is deliberately a
// separate type and never implies positive preference.
type FeedbackEvent struct {
	Version    int              `json:"version"`
	ID         string           `json:"id"`
	OccurredAt time.Time        `json:"occurredAt"`
	Type       FeedbackType     `json:"type"`
	Scope      FeedbackScope    `json:"scope"`
	TrackID    string           `json:"trackId"`
	RequestID  string           `json:"requestId"`
	SessionID  string           `json:"sessionId"`
	Context    FeedbackContext  `json:"context"`
	Versions   FeedbackVersions `json:"versions"`
}

func (e FeedbackEvent) Validate() error {
	if e.Version != FeedbackEventVersion {
		return fmt.Errorf("feedback: unsupported event version %d", e.Version)
	}
	if strings.TrimSpace(e.ID) == "" || e.OccurredAt.IsZero() {
		return fmt.Errorf("feedback: event id and timestamp are required")
	}
	if strings.TrimSpace(e.TrackID) == "" {
		return fmt.Errorf("feedback: track id is required")
	}
	switch e.Type {
	case FeedbackLike, FeedbackDislike, FeedbackMoreLike, FeedbackLessLike,
		FeedbackAccepted, FeedbackRemoved, FeedbackExposure:
	default:
		return fmt.Errorf("feedback: unknown type %q", e.Type)
	}
	if e.Scope != FeedbackScopeDurable && e.Scope != FeedbackScopeRequest {
		return fmt.Errorf("feedback: unknown scope %q", e.Scope)
	}
	if e.Type == FeedbackExposure && e.Scope != FeedbackScopeRequest {
		return fmt.Errorf("feedback: exposures must be request-scoped")
	}
	if e.Scope == FeedbackScopeRequest && e.RequestID == "" && e.SessionID == "" {
		return fmt.Errorf("feedback: request-scoped events need a request or session id")
	}
	return nil
}

type EmbeddingAffinity struct {
	Audio        []float32 `json:"audio"`
	Cooccurrence []float32 `json:"cooccurrence"`
}

type TasteCluster struct {
	ID               string            `json:"id"`
	Weight           float64           `json:"weight"`
	Affinity         EmbeddingAffinity `json:"affinity"`
	EvidenceTrackIDs []string          `json:"evidenceTrackIds"`
}

// TasteProfile is a deterministic projection of the contributing feedback
// events for one catalog and optional request context.
type TasteProfile struct {
	Version          int               `json:"version"`
	AlgorithmVersion string            `json:"algorithmVersion"`
	SnapshotID       string            `json:"snapshotId"`
	CatalogVersion   string            `json:"catalogVersion"`
	AsOf             time.Time         `json:"asOf"`
	RequestID        string            `json:"requestId"`
	SessionID        string            `json:"sessionId"`
	ColdStart        bool              `json:"coldStart"`
	Positive         EmbeddingAffinity `json:"positive"`
	Negative         EmbeddingAffinity `json:"negative"`
	RequestPositive  EmbeddingAffinity `json:"requestPositive"`
	RequestNegative  EmbeddingAffinity `json:"requestNegative"`
	Clusters         []TasteCluster    `json:"clusters"`
	PositiveEvidence int               `json:"positiveEvidence"`
	NegativeEvidence int               `json:"negativeEvidence"`
	RequestEvidence  int               `json:"requestEvidence"`
	ExposureCount    int               `json:"exposureCount"`
}
