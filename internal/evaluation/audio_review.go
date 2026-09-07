package evaluation

import (
	"fmt"
	"sort"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
)

// AudioReviewDataset is a listening-review interchange format. It carries
// measured runs from all three ablations; it never generates human judgments.
// Preview judgments must be made blind to variant and cosine score.
type AudioReviewDataset struct {
	Version        int                     `json:"version"`
	ID             string                  `json:"id"`
	Evidence       EvidenceLevel           `json:"evidence"`
	Model          core.AudioModelIdentity `json:"model"`
	PolicyVersion  string                  `json:"policyVersion"`
	DevelopmentSet string                  `json:"developmentSet"`
	Runs           []AudioReviewRun        `json:"runs"`
}

type ReviewedRecording struct {
	RecordingID string   `json:"recordingId"`
	ArtistIDs   []string `json:"artistIds"`
}

type AudioTrackReview struct {
	ReviewedRecording
	// Nil is unreviewed/unknown, not a negative judgment. Stage identifies the
	// intended journey stage; the reviewer judges fit to that stage.
	Stage          string `json:"stage,omitempty"`
	MusicalFit     *bool  `json:"musicalFit"`
	Violation      *bool  `json:"violation"`
	PreviewCovered bool   `json:"previewCovered"`
}

type AudioReviewRun struct {
	CaseID                  string                      `json:"caseId"`
	Prompt                  string                      `json:"prompt"`
	Split                   string                      `json:"split"`   // development or heldout; assigned before tuning
	Variant                 string                      `json:"variant"` // existing, seed_verified, candidate_verified
	Requested               int                         `json:"requested"`
	Outcome                 core.GenerationOutcomeState `json:"outcome"`
	References              []ReviewedRecording         `json:"references"` // explicit and proposed anchors
	Tracks                  []AudioTrackReview          `json:"tracks"`
	FirstResultMilliseconds *int64                      `json:"firstResultMilliseconds"`
	TotalMilliseconds       int64                       `json:"totalMilliseconds"`
	PeakMemoryBytes         int64                       `json:"peakMemoryBytes"`
	BytesFetched            int64                       `json:"bytesFetched"`
	CacheHits               int                         `json:"cacheHits"`
	NewAnalyses             int                         `json:"newAnalyses"`
}

type AudioReviewMetrics struct {
	Split                       string                              `json:"split"`
	Variant                     string                              `json:"variant"`
	Runs                        int                                 `json:"runs"`
	Returned                    int                                 `json:"returned"`
	FitJudged                   int                                 `json:"fitJudged"`
	FitMatches                  int                                 `json:"fitMatches"`
	ViolationJudged             int                                 `json:"violationJudged"`
	Violations                  int                                 `json:"violations"`
	PreviewCovered              int                                 `json:"previewCovered"`
	Abstentions                 int                                 `json:"abstentions"`
	Outcomes                    map[core.GenerationOutcomeState]int `json:"outcomes"`
	FitRate                     *float64                            `json:"fitRate"`
	ViolationRate               *float64                            `json:"violationRate"`
	PreviewCoverage             *float64                            `json:"previewCoverage"`
	MeanFirstResultMilliseconds *float64                            `json:"meanFirstResultMilliseconds"`
	MeanTotalMilliseconds       float64                             `json:"meanTotalMilliseconds"`
	MaximumMemoryBytes          int64                               `json:"maximumMemoryBytes"`
	BytesFetched                int64                               `json:"bytesFetched"`
	CacheHits                   int                                 `json:"cacheHits"`
	NewAnalyses                 int                                 `json:"newAnalyses"`
	CacheReuse                  *float64                            `json:"cacheReuse"`
	firstCount                  int
	firstTotal                  int64
}

type AudioReviewReport struct {
	DatasetID          string               `json:"datasetId"`
	DatasetFingerprint string               `json:"datasetFingerprint"`
	Evidence           EvidenceLevel        `json:"evidence"`
	HumanReviewed      bool                 `json:"humanReviewed"`
	Metrics            []AudioReviewMetrics `json:"metrics"`
	Limitations        []string             `json:"limitations"`
}

// EvaluateAudioReviews rejects recording AND artist leakage across splits and
// mismatched ablation cases. This reporting path has no threshold fitting API;
// a frozen development-set policy is supplied separately from held-out reviews.
func EvaluateAudioReviews(data AudioReviewDataset) (AudioReviewReport, error) {
	if data.Version != 1 || data.ID == "" || (data.Evidence != EvidenceJudged && data.Evidence != EvidenceSynthetic) || data.PolicyVersion == "" || data.DevelopmentSet == "" || len(data.Runs) == 0 {
		return AudioReviewReport{}, fmt.Errorf("audio evaluation: version, evidence, frozen policy, development set and runs required")
	}
	groups := map[string]string{}
	variants := map[string]map[string]bool{}
	cases := map[string]string{}
	metrics := map[string]*AudioReviewMetrics{}
	for _, run := range data.Runs {
		if err := validateAudioReviewRun(run); err != nil {
			return AudioReviewReport{}, err
		}
		key := run.Split + "/" + run.Variant
		caseKey := fmt.Sprintf("%s\x00%s\x00%d", run.Split, run.Prompt, run.Requested)
		if previous, ok := cases[run.CaseID]; ok && previous != caseKey {
			return AudioReviewReport{}, fmt.Errorf("audio evaluation: ablation request mismatch for %s", run.CaseID)
		}
		cases[run.CaseID] = caseKey
		if variants[run.CaseID] == nil {
			variants[run.CaseID] = map[string]bool{}
		}
		if variants[run.CaseID][run.Variant] {
			return AudioReviewReport{}, fmt.Errorf("audio evaluation: duplicate case/variant")
		}
		variants[run.CaseID][run.Variant] = true
		identities := append([]ReviewedRecording(nil), run.References...)
		for _, track := range run.Tracks {
			identities = append(identities, track.ReviewedRecording)
		}
		for _, identity := range identities {
			if identity.RecordingID == "" || len(identity.ArtistIDs) == 0 {
				return AudioReviewReport{}, fmt.Errorf("audio evaluation: recording and credited artist IDs required for split audit")
			}
			keys := []string{"recording:" + identity.RecordingID}
			for _, artist := range identity.ArtistIDs {
				if artist == "" {
					return AudioReviewReport{}, fmt.Errorf("audio evaluation: missing artist ID")
				}
				keys = append(keys, "artist:"+artist)
			}
			for _, group := range keys {
				if split, ok := groups[group]; ok && split != run.Split {
					return AudioReviewReport{}, fmt.Errorf("audio evaluation: %s leaks across development and heldout", group)
				}
				groups[group] = run.Split
			}
		}
		m := metrics[key]
		if m == nil {
			m = &AudioReviewMetrics{Split: run.Split, Variant: run.Variant, Outcomes: map[core.GenerationOutcomeState]int{}}
			metrics[key] = m
		}
		m.Runs++
		m.Outcomes[run.Outcome]++
		m.Returned += len(run.Tracks)
		if len(run.Tracks) == 0 {
			m.Abstentions++
		}
		for _, track := range run.Tracks {
			if track.MusicalFit != nil {
				m.FitJudged++
				if *track.MusicalFit {
					m.FitMatches++
				}
			}
			if track.Violation != nil {
				m.ViolationJudged++
				if *track.Violation {
					m.Violations++
				}
			}
			if track.PreviewCovered {
				m.PreviewCovered++
			}
		}
		if run.FirstResultMilliseconds != nil {
			m.firstCount++
			m.firstTotal += *run.FirstResultMilliseconds
		}
		m.MeanTotalMilliseconds += float64(run.TotalMilliseconds)
		m.MaximumMemoryBytes = max(m.MaximumMemoryBytes, run.PeakMemoryBytes)
		m.BytesFetched += run.BytesFetched
		m.CacheHits += run.CacheHits
		m.NewAnalyses += run.NewAnalyses
	}
	for id, seen := range variants {
		if len(seen) != 3 {
			return AudioReviewReport{}, fmt.Errorf("audio evaluation: %s needs all three ablations", id)
		}
	}
	report := AudioReviewReport{DatasetID: data.ID, DatasetFingerprint: audio.Fingerprint(data), Evidence: data.Evidence, HumanReviewed: data.Evidence == EvidenceJudged, Limitations: []string{"Judgments cover reviewed previews and intended journey stages only.", "Unknown judgments are excluded from rates and retained in denominators as explicit counts.", "This report does not fit thresholds or establish universal genre recognition."}}
	for _, m := range metrics {
		m.FitRate = audioRatio(m.FitMatches, m.FitJudged)
		m.ViolationRate = audioRatio(m.Violations, m.ViolationJudged)
		m.PreviewCoverage = audioRatio(m.PreviewCovered, m.Returned)
		m.MeanFirstResultMilliseconds = audioRatio(int(m.firstTotal), m.firstCount)
		m.MeanTotalMilliseconds /= float64(m.Runs)
		m.CacheReuse = audioRatio(m.CacheHits, m.CacheHits+m.NewAnalyses)
		report.Metrics = append(report.Metrics, *m)
	}
	sort.Slice(report.Metrics, func(i, j int) bool {
		return report.Metrics[i].Split+report.Metrics[i].Variant < report.Metrics[j].Split+report.Metrics[j].Variant
	})
	return report, nil
}

func validateAudioReviewRun(run AudioReviewRun) error {
	validOutcome := run.Outcome == core.OutcomeFulfilled || run.Outcome == core.OutcomePartial || run.Outcome == core.OutcomeUnsupported || run.Outcome == core.OutcomeNeedsClarification
	if run.CaseID == "" || run.Prompt == "" || run.Requested < 1 || len(run.Tracks) > run.Requested || (run.Split != "development" && run.Split != "heldout") || (run.Variant != "existing" && run.Variant != "seed_verified" && run.Variant != "candidate_verified") || !validOutcome || run.TotalMilliseconds < 0 || run.PeakMemoryBytes <= 0 || run.BytesFetched < 0 || run.CacheHits < 0 || run.NewAnalyses < 0 {
		return fmt.Errorf("audio evaluation: invalid run metadata or measurements")
	}
	if run.FirstResultMilliseconds != nil && (*run.FirstResultMilliseconds < 0 || *run.FirstResultMilliseconds > run.TotalMilliseconds || len(run.Tracks) == 0) {
		return fmt.Errorf("audio evaluation: invalid first-result latency")
	}
	if len(run.Tracks) > 0 && run.FirstResultMilliseconds == nil {
		return fmt.Errorf("audio evaluation: missing first-result latency")
	}
	return nil
}

func audioRatio(numerator, denominator int) *float64 {
	if denominator == 0 {
		return nil
	}
	value := float64(numerator) / float64(denominator)
	return &value
}
