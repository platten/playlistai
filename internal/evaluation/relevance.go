package evaluation

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// RelevanceRun is a measured desktop run, not a musical judgment. The full
// generation contract is retained by musiccheck beside this listening view.
type RelevanceRun struct {
	GenerationLimitMilliseconds int64        `json:"generationLimitMilliseconds,omitempty"`
	FamilyID                    string       `json:"familyId"`
	Prompt                      string       `json:"prompt"`
	Split                       string       `json:"split"`
	Variant                     string       `json:"variant"`
	InputMode                   string       `json:"inputMode"`
	CacheCondition              string       `json:"cacheCondition"`
	Requested                   int          `json:"requested"`
	Tracks                      []BlindTrack `json:"tracks"`
	Candidates                  []BlindTrack `json:"candidates"`
	Milliseconds                int64        `json:"milliseconds"`
	BytesFetched                *int64       `json:"bytesFetched"`
	NewAnalyses                 *int         `json:"newAnalyses"`
	ConstraintViolations        *int         `json:"constraintViolations"`
	Error                       string       `json:"error,omitempty"`
}

type RelevanceGrade struct {
	RequestFit          *float64 `json:"requestFit"` // 0..3; null remains unknown
	ReferenceSimilarity *float64 `json:"referenceSimilarity"`
}

type RelevancePlaylist struct {
	Label       string   `json:"label"`
	TrackIDs    []string `json:"trackIds"`
	Opening     *float64 `json:"opening"`
	Transitions *float64 `json:"transitions"`
}

type RelevanceCase struct {
	ID        string                    `json:"id"`
	Prompt    string                    `json:"prompt"`
	Tracks    []BlindTrack              `json:"tracks"`
	Grades    map[string]RelevanceGrade `json:"grades"`
	Playlists []RelevancePlaylist       `json:"playlists"`
	Preferred string                    `json:"preferred"` // playlist label, tie, or empty (unknown)
}

type RelevanceBundle struct {
	Version int             `json:"version"`
	Cases   []RelevanceCase `json:"cases"`
}

type RelevanceKey struct {
	CaseID string       `json:"caseId"`
	Label  string       `json:"label"`
	Run    RelevanceRun `json:"run"`
}

type RelevanceKeys struct {
	Version           int            `json:"version"`
	PacketFingerprint string         `json:"packetFingerprint"`
	Keys              []RelevanceKey `json:"keys"`
}

// PoolRelevanceRuns pools all assessed candidates, removes scores and variant
// identities, and deterministically shuffles the playlists and candidate pool.
func PoolRelevanceRuns(runs []RelevanceRun, seed string) (RelevanceBundle, RelevanceKeys, error) {
	bundle, keys := RelevanceBundle{Version: 1}, RelevanceKeys{Version: 1}
	groups := map[string][]RelevanceRun{}
	familySplits := map[string]string{}
	for _, run := range runs {
		if run.InputMode != runs[0].InputMode {
			return bundle, keys, fmt.Errorf("relevance: pool raw prompts and frozen intents separately")
		}
		if run.FamilyID == "" || run.Prompt == "" || run.Variant == "" || (run.Split != "development" && run.Split != "heldout") || (run.InputMode != "raw" && run.InputMode != "frozen") || (run.CacheCondition != "cold" && run.CacheCondition != "warm" && run.CacheCondition != "unknown") || run.Milliseconds < 0 || run.Requested < 1 {
			return bundle, keys, fmt.Errorf("relevance: invalid run identity or measurements")
		}
		if previous, ok := familySplits[run.FamilyID]; ok && previous != run.Split {
			return bundle, keys, fmt.Errorf("relevance: family leaks across development and heldout")
		}
		familySplits[run.FamilyID] = run.Split
		id := run.FamilyID + "\x00" + run.InputMode + "\x00" + run.CacheCondition
		groups[id] = append(groups[id], run)
	}
	groupIDs := make([]string, 0, len(groups))
	for id := range groups {
		groupIDs = append(groupIDs, id)
	}
	sort.Strings(groupIDs)
	for _, id := range groupIDs {
		items := groups[id]
		sort.Slice(items, func(i, j int) bool {
			return blindHash(seed, id, items[i].Variant) < blindHash(seed, id, items[j].Variant)
		})
		entry := RelevanceCase{ID: blindHash(seed, id), Prompt: items[0].Prompt, Grades: map[string]RelevanceGrade{}, Tracks: []BlindTrack{}, Playlists: []RelevancePlaylist{}}
		seen, tracks := map[string]bool{}, map[string]BlindTrack{}
		for index, run := range items {
			if seen[run.Variant] || run.Prompt != items[0].Prompt || run.Split != items[0].Split || run.Requested != items[0].Requested {
				return bundle, keys, fmt.Errorf("relevance: duplicate variant or mismatched request in family %s", run.FamilyID)
			}
			seen[run.Variant] = true
			label := fmt.Sprintf("P%d", index+1)
			playlist := RelevancePlaylist{Label: label, TrackIDs: []string{}}
			outputIDs := map[string]bool{}
			for _, track := range run.Tracks {
				if track.ID == "" || outputIDs[track.ID] {
					return bundle, keys, fmt.Errorf("relevance: invalid or duplicate output track")
				}
				outputIDs[track.ID] = true
				playlist.TrackIDs = append(playlist.TrackIDs, track.ID)
			}
			for _, track := range append(append([]BlindTrack(nil), run.Candidates...), run.Tracks...) {
				if track.ID == "" {
					return bundle, keys, fmt.Errorf("relevance: missing candidate identity")
				}
				tracks[track.ID] = track
			}
			entry.Playlists = append(entry.Playlists, playlist)
			keys.Keys = append(keys.Keys, RelevanceKey{CaseID: entry.ID, Label: label, Run: run})
		}
		for _, track := range tracks {
			entry.Tracks = append(entry.Tracks, track)
			entry.Grades[track.ID] = RelevanceGrade{}
		}
		sort.Slice(entry.Tracks, func(i, j int) bool {
			return blindHash(seed, id, entry.Tracks[i].ID) < blindHash(seed, id, entry.Tracks[j].ID)
		})
		bundle.Cases = append(bundle.Cases, entry)
	}
	if len(bundle.Cases) == 0 {
		return bundle, keys, fmt.Errorf("relevance: no runs")
	}
	keys.PacketFingerprint = relevancePacketFingerprint(bundle)
	return bundle, keys, nil
}

func blindHash(parts ...string) string {
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum[:12])
}

func relevancePacketFingerprint(bundle RelevanceBundle) string {
	// Deep copy before clearing only fields that reviewers may edit.
	raw, _ := json.Marshal(bundle)
	var copy RelevanceBundle
	_ = json.Unmarshal(raw, &copy)
	for index := range copy.Cases {
		entry := &copy.Cases[index]
		entry.Preferred = ""
		for id := range entry.Grades {
			entry.Grades[id] = RelevanceGrade{}
		}
		for index := range entry.Playlists {
			entry.Playlists[index].Opening, entry.Playlists[index].Transitions = nil, nil
		}
	}
	return fingerprintJSON(copy)
}

type RelevanceRunMetrics struct {
	FamilyID                  string   `json:"familyId"`
	Split                     string   `json:"split"`
	Variant                   string   `json:"variant"`
	InputMode                 string   `json:"inputMode"`
	CacheCondition            string   `json:"cacheCondition"`
	Returned                  int      `json:"returned"`
	Judged                    int      `json:"judged"`
	PoolJudged                int      `json:"poolJudged"`
	PoolSize                  int      `json:"poolSize"`
	JudgedPrecision           *float64 `json:"judgedPrecision"`
	RequestFit                *float64 `json:"requestFit"`
	ReferenceSimilarity       *float64 `json:"referenceSimilarity"`
	NDCG                      *float64 `json:"ndcg"`
	RelevantCandidateCoverage *float64 `json:"relevantCandidateCoverage"`
	UsefulPartial             *bool    `json:"usefulPartial"`
	Opening                   *float64 `json:"opening"`
	Transitions               *float64 `json:"transitions"`
	Preference                *float64 `json:"preference"`
	Milliseconds              int64    `json:"milliseconds"`
	BytesFetched              *int64   `json:"bytesFetched"`
	NewAnalyses               *int     `json:"newAnalyses"`
}

type RelevanceReport struct {
	Metrics           []RelevanceRunMetrics `json:"metrics"`
	PairedRequestFit  *Interval             `json:"pairedRequestFit"`
	PromotionEligible bool                  `json:"promotionEligible"`
	PromotionReasons  []string              `json:"promotionReasons"`
	Limitations       []string              `json:"limitations"`
}

// EvaluateRelevance keeps missing judgments unknown. Promotion requires paired
// family means (not individual tracks), complete held-out request-fit judgments,
// independently checked constraints and measured runs within their recorded
// budget. Legacy reports retain their original five-minute limit.
func EvaluateRelevance(bundle RelevanceBundle, keys RelevanceKeys, baseline, treatment string, policyFrozen bool) (RelevanceReport, error) {
	report := RelevanceReport{Metrics: []RelevanceRunMetrics{}, PromotionReasons: []string{}, Limitations: []string{"Human grades are required; generator scores never supply judgments.", "NDCG is unknown until every output and pooled candidate has a request-fit grade.", "Relevant-candidate coverage is relative to the judged common pool, not the universe of music.", "The paired 95% interval uses independent family means and Student's t; fewer than two families is insufficient."}}
	if bundle.Version != 1 || keys.Version != 1 || baseline == "" || treatment == "" || baseline == treatment || relevancePacketFingerprint(bundle) != keys.PacketFingerprint {
		return report, fmt.Errorf("relevance: invalid comparison or listener packet was modified outside judgment fields")
	}
	lookup := map[string]RelevanceRun{}
	for _, key := range keys.Keys {
		id := key.CaseID + "/" + key.Label
		if _, ok := lookup[id]; ok {
			return report, fmt.Errorf("relevance: duplicate key")
		}
		lookup[id] = key.Run
	}
	type paired struct {
		family        string
		before, after *float64
	}
	pairs := map[string]paired{}
	complete, constraints, bounded := true, true, true
	for _, entry := range bundle.Cases {
		grades := map[string]float64{}
		for id, grade := range entry.Grades {
			for _, value := range []*float64{grade.RequestFit, grade.ReferenceSimilarity} {
				if value != nil && !validGrade(*value) {
					return report, fmt.Errorf("relevance: grades must be null or 0..3")
				}
			}
			if grade.RequestFit != nil {
				grades[id] = *grade.RequestFit
			}
		}
		validPreferred := entry.Preferred == "" || entry.Preferred == "tie"
		for _, playlist := range entry.Playlists {
			if entry.Preferred == playlist.Label {
				validPreferred = true
			}
		}
		if !validPreferred {
			return report, fmt.Errorf("relevance: preferred playlist label does not exist")
		}
		for _, playlist := range entry.Playlists {
			run, ok := lookup[entry.ID+"/"+playlist.Label]
			if !ok {
				return report, fmt.Errorf("relevance: missing identity key")
			}
			for _, value := range []*float64{playlist.Opening, playlist.Transitions} {
				if value != nil && !validGrade(*value) {
					return report, fmt.Errorf("relevance: playlist grades must be null or 0..3")
				}
			}
			m := RelevanceRunMetrics{FamilyID: run.FamilyID, Split: run.Split, Variant: run.Variant, InputMode: run.InputMode, CacheCondition: run.CacheCondition, Returned: len(run.Tracks), PoolJudged: len(grades), PoolSize: len(entry.Tracks), Opening: playlist.Opening, Transitions: playlist.Transitions, Milliseconds: run.Milliseconds, BytesFetched: run.BytesFetched, NewAnalyses: run.NewAnalyses}
			var fit, reference float64
			matches, referenceCount := 0, 0
			for _, id := range playlist.TrackIDs {
				grade := entry.Grades[id]
				if grade.RequestFit != nil {
					m.Judged++
					fit += *grade.RequestFit
					if *grade.RequestFit >= 2 {
						matches++
					}
				}
				if grade.ReferenceSimilarity != nil {
					reference += *grade.ReferenceSimilarity
					referenceCount++
				}
			}
			m.JudgedPrecision = audioRatio(matches, m.Judged)
			if m.Judged > 0 {
				value := fit / float64(m.Judged)
				m.RequestFit = &value
			}
			if referenceCount > 0 {
				value := reference / float64(referenceCount)
				m.ReferenceSimilarity = &value
			}
			if m.Judged == m.Returned && m.Returned > 0 && len(grades) == len(entry.Tracks) {
				if value, known := NDCGAtK(playlist.TrackIDs, grades, len(playlist.TrackIDs)); known {
					m.NDCG = &value
				}
			}
			relevant, found := 0, 0
			candidateIDs := map[string]bool{}
			for _, track := range run.Candidates {
				candidateIDs[track.ID] = true
			}
			for id, grade := range grades {
				if grade >= 2 {
					relevant++
					if candidateIDs[id] {
						found++
					}
				}
			}
			if run.Candidates != nil {
				m.RelevantCandidateCoverage = audioRatio(found, relevant)
			}
			if m.Returned > 0 && m.Returned < run.Requested && m.Judged == m.Returned {
				value := matches == m.Returned
				m.UsefulPartial = &value
			}
			if entry.Preferred != "" {
				value := 0.0
				switch entry.Preferred {
				case "tie":
					value = 0.5
				case playlist.Label:
					value = 1
				}
				m.Preference = &value
			}
			report.Metrics = append(report.Metrics, m)
			if run.Split == "heldout" && (run.Variant == baseline || run.Variant == treatment) {
				id := run.FamilyID + "/" + run.InputMode + "/" + run.CacheCondition
				pair := pairs[id]
				pair.family = run.FamilyID
				if run.Variant == baseline {
					pair.before = m.RequestFit
				} else {
					pair.after = m.RequestFit
				}
				pairs[id] = pair
				complete = complete && m.Judged == m.Returned && m.Returned > 0 && run.Error == ""
				constraints = constraints && run.ConstraintViolations != nil && *run.ConstraintViolations == 0
				limit := run.GenerationLimitMilliseconds
				if limit == 0 {
					limit = 300000
				}
				bounded = bounded && limit > 0 && limit <= 600000 && run.Milliseconds <= limit
			}
		}
	}
	// Average repeats/conditions within a family before estimating uncertainty.
	familyDiffs := map[string][]float64{}
	for _, pair := range pairs {
		if pair.before == nil || pair.after == nil {
			complete = false
			continue
		}
		familyDiffs[pair.family] = append(familyDiffs[pair.family], *pair.after-*pair.before)
	}
	var differences []float64
	for _, values := range familyDiffs {
		var sum float64
		for _, value := range values {
			sum += value
		}
		differences = append(differences, sum/float64(len(values)))
	}
	sort.Float64s(differences)
	if len(differences) >= 2 {
		interval := meanInterval(differences)
		// Two-sided 95% Student t critical values, df 1..30; 1.96 is
		// approached only for large samples, so use df=30 beyond this table.
		critical := []float64{12.706, 4.303, 3.182, 2.776, 2.571, 2.447, 2.365, 2.306, 2.262, 2.228, 2.201, 2.179, 2.160, 2.145, 2.131, 2.120, 2.110, 2.101, 2.093, 2.086, 2.080, 2.074, 2.069, 2.064, 2.060, 2.056, 2.052, 2.048, 2.045, 2.042}
		margin := (interval.High95 - interval.Mean) / 1.96 * critical[min(len(differences)-2, len(critical)-1)]
		interval.Low95, interval.High95 = interval.Mean-margin, interval.Mean+margin
		if !math.IsNaN(interval.Mean) {
			report.PairedRequestFit = &interval
		}
	}
	if !policyFrozen {
		report.PromotionReasons = append(report.PromotionReasons, "policy not declared frozen before held-out evaluation")
	}
	if len(familyDiffs) < 20 {
		report.PromotionReasons = append(report.PromotionReasons, "fewer than 20 independently paired held-out families")
	}
	if !complete || len(pairs) == 0 {
		report.PromotionReasons = append(report.PromotionReasons, "held-out comparisons or request-fit judgments incomplete")
	}
	if !constraints {
		report.PromotionReasons = append(report.PromotionReasons, "constraint regression checks missing or failing")
	}
	if !bounded {
		report.PromotionReasons = append(report.PromotionReasons, "measured generation exceeded its recorded time budget")
	}
	if report.PairedRequestFit == nil || report.PairedRequestFit.Low95 <= 0 {
		report.PromotionReasons = append(report.PromotionReasons, "paired 95% relevance improvement interval does not exclude zero")
	}
	report.PromotionEligible = len(report.PromotionReasons) == 0
	return report, nil
}
