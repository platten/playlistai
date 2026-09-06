// Package evaluation provides offline, repeatable recommendation evaluation.
// Synthetic datasets exercise the harness but are never quality evidence.
package evaluation

import (
	"time"

	"github.com/platten/playlistai/internal/core"
)

const ContractVersion = 1

type EvidenceLevel string

const (
	EvidenceSynthetic EvidenceLevel = "synthetic"
	EvidenceUnlabeled EvidenceLevel = "unlabeled_workflow"
	EvidenceJudged    EvidenceLevel = "human_judged"
	EvidenceObserved  EvidenceLevel = "observed_interactions"
)

type Dataset struct {
	Version             int                  `json:"version"`
	Name                string               `json:"name"`
	Evidence            EvidenceLevel        `json:"evidence"`
	CatalogVersion      string               `json:"catalogVersion"`
	CreatedAt           time.Time            `json:"createdAt"`
	IntentCases         []IntentCase         `json:"intentCases"`
	ResolutionCases     []ResolutionCase     `json:"resolutionCases"`
	RecommendationCases []RecommendationCase `json:"recommendationCases"`
	Interactions        []InteractionRecord  `json:"interactions"`
	TuningGrid          []ParameterSet       `json:"tuningGrid"`
}

type IntentCase struct {
	ID           string          `json:"id"`
	Prompt       string          `json:"prompt"`
	NowPlaying   *core.TrackRef  `json:"nowPlaying,omitempty"`
	RecentTracks []core.TrackRef `json:"recentTracks,omitempty"`
	Locale       string          `json:"locale,omitempty"`
	Expected     IntentLabels    `json:"expected"`
	Tags         []string        `json:"tags"`
}

type IntentLabels struct {
	Mode                core.Mode `json:"mode"`
	TotalTrackCount     *int      `json:"totalTrackCount,omitempty"`
	PositiveReferences  []string  `json:"positiveReferences"`
	NegativeReferences  []string  `json:"negativeReferences"`
	PositivePreferences []string  `json:"positivePreferences"`
	NegativePreferences []string  `json:"negativePreferences"`
	HardConstraints     []string  `json:"hardConstraints"`
	TypedReferences     []string  `json:"typedReferences"`
	RequiredTracks      []string  `json:"requiredTracks"`
	JourneyWaypoints    []string  `json:"journeyWaypoints"`
	Unsupported         []string  `json:"unsupported"`
	EvidenceSpans       []string  `json:"evidenceSpans"`
}

type ResolutionCase struct {
	ID                  string                `json:"id"`
	Reference           core.IntentReference  `json:"reference"`
	ExpectedStatus      core.ResolutionStatus `json:"expectedStatus"`
	AcceptableEntityIDs []string              `json:"acceptableEntityIds"`
	Tags                []string              `json:"tags"`
}

type RecommendationCase struct {
	ID              string             `json:"id"`
	RequestID       string             `json:"requestId"`
	ListenerID      string             `json:"listenerId"`
	OccurredAt      time.Time          `json:"occurredAt"`
	Prompt          string             `json:"prompt"`
	Intent          core.MusicIntent   `json:"intent"`
	Relevance       map[string]float64 `json:"relevance"` // 0..3 graded judgment
	RecentExposures []string           `json:"recentExposures"`
	Tags            []string           `json:"tags"`
}

type InteractionRecord struct {
	ListenerID string             `json:"listenerId"`
	Event      core.FeedbackEvent `json:"event"`
}

// ParameterSet contains only interpretable knobs eligible for development-set
// tuning. The test split never participates in selection.
type ParameterSet struct {
	Name                      string  `json:"name"`
	ListenerWeight            float64 `json:"listenerWeight"`
	NegativePenalty           float64 `json:"negativePenalty"`
	SemanticWeight            float64 `json:"semanticWeight"`
	SemanticNegativePenalty   float64 `json:"semanticNegativePenalty"`
	MMRMinimumLambda          float64 `json:"mmrMinimumLambda"`
	SelectionRelevanceWindow  float64 `json:"selectionRelevanceWindow"`
	TransitionRelevanceWeight float64 `json:"transitionRelevanceWeight"`
}

type Split string

const (
	SplitTrain       Split = "train"
	SplitDevelopment Split = "development"
	SplitTest        Split = "test"
)

type TemporalSplit struct {
	TrainEnd       time.Time        `json:"trainEnd"`
	DevelopmentEnd time.Time        `json:"developmentEnd"`
	Assignments    map[string]Split `json:"assignments"`
}

type Latency struct {
	ParseMicros      int64 `json:"parseMicros"`
	RetrievalMicros  int64 `json:"retrievalMicros"`
	RankingMicros    int64 `json:"rankingMicros"`
	SequencingMicros int64 `json:"sequencingMicros"`
	TotalMicros      int64 `json:"totalMicros"`
}

type CaseMetrics struct {
	CaseID                   string           `json:"caseId"`
	Generation               GenerationRecord `json:"generation"`
	RecallAtK                *float64         `json:"recallAtK,omitempty"`
	NDCGAtK                  *float64         `json:"ndcgAtK,omitempty"`
	HardConstraintViolations int              `json:"hardConstraintViolations"`
	RecordingDuplicates      int              `json:"recordingDuplicates"`
	ArtistDiversity          float64          `json:"artistDiversity"`
	MaxArtistShare           float64          `json:"maxArtistShare"`
	CatalogCoverage          float64          `json:"catalogCoverage"`
	RecentExposureRepetition float64          `json:"recentExposureRepetition"`
	TransitionQuality        *float64         `json:"transitionQuality,omitempty"`
	Latency                  Latency          `json:"latency"`
	Error                    string           `json:"error,omitempty"`
}

type GenerationRecord struct {
	TrackIDs           []string     `json:"trackIds"`
	CatalogVersion     string       `json:"catalogVersion"`
	AlgorithmVersion   string       `json:"algorithmVersion"`
	IntentFingerprint  string       `json:"intentFingerprint"`
	ContextFingerprint string       `json:"contextFingerprint"`
	IntentVersion      int          `json:"intentVersion"`
	ProfileVersion     string       `json:"profileVersion"`
	ProfileSnapshot    string       `json:"profileSnapshot"`
	RNGSeed            core.RNGSeed `json:"rngSeed"`
}

type AggregateMetrics struct {
	Cases                    int      `json:"cases"`
	SuccessfulCases          int      `json:"successfulCases"`
	RecallAtK                *float64 `json:"recallAtK,omitempty"`
	NDCGAtK                  *float64 `json:"ndcgAtK,omitempty"`
	HardConstraintViolations int      `json:"hardConstraintViolations"`
	RecordingDuplicates      int      `json:"recordingDuplicates"`
	ArtistDiversity          float64  `json:"artistDiversity"`
	MaxArtistShare           float64  `json:"maxArtistShare"`
	CatalogCoverage          float64  `json:"catalogCoverage"`
	RecentExposureRepetition float64  `json:"recentExposureRepetition"`
	TransitionQuality        *float64 `json:"transitionQuality,omitempty"`
	Latency                  Latency  `json:"latency"`
}

type VariantResult struct {
	Name             string              `json:"name"`
	AlgorithmVersion string              `json:"algorithmVersion"`
	Split            Split               `json:"split"`
	Parameters       ParameterSet        `json:"parameters"`
	Cases            []CaseMetrics       `json:"cases"`
	Aggregate        AggregateMetrics    `json:"aggregate"`
	Uncertainty      map[string]Interval `json:"uncertainty"`
}

type Interval struct {
	Mean   float64 `json:"mean"`
	Low95  float64 `json:"low95"`
	High95 float64 `json:"high95"`
	Cases  int     `json:"cases"`
}

type ExtractionResult struct {
	Cases         int      `json:"cases"`
	LabeledFields int      `json:"labeledFields"`
	CorrectFields int      `json:"correctFields"`
	Accuracy      *float64 `json:"accuracy,omitempty"`
}

type ResolutionResult struct {
	Cases    int      `json:"cases"`
	Correct  int      `json:"correct"`
	Accuracy *float64 `json:"accuracy,omitempty"`
}

type Report struct {
	Version            int                    `json:"version"`
	DatasetName        string                 `json:"datasetName"`
	Evidence           EvidenceLevel          `json:"evidence"`
	CatalogVersion     string                 `json:"catalogVersion"`
	ParserVersion      string                 `json:"parserVersion"`
	SemanticInfo       *core.FeatureStoreInfo `json:"semanticInfo,omitempty"`
	GeneratedAt        time.Time              `json:"generatedAt"`
	K                  int                    `json:"k"`
	TemporalSplit      TemporalSplit          `json:"temporalSplit"`
	Intent             ExtractionResult       `json:"intent"`
	Resolution         ResolutionResult       `json:"resolution"`
	Development        []VariantResult        `json:"development"`
	TuningCandidates   []VariantResult        `json:"tuningCandidates,omitempty"`
	SelectedParameters *ParameterSet          `json:"selectedParameters,omitempty"`
	HeldOutTest        []VariantResult        `json:"heldOutTest"`
	Limitations        []string               `json:"limitations"`
	Cohorts            map[string]int         `json:"cohorts"`
}
