package multichannel

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/musicconcepts"
	"github.com/platten/playlistai/internal/ports"
)

// Archived classifier margins lead descriptive ranking, ahead of the default
// .35 semantic weight. This is an explicit source preference, not a calibrated
// probability or a default tuned on held-out listener judgments.
const acousticIntentWeight = .55

func acousticWeight(intent core.MusicIntent) float64 {
	if intent.Controls.RecommendationMode == core.CLAPFirst {
		return .15
	}
	return acousticIntentWeight
}

// Scored CLAP clauses lead in CLAP-first mode, including uncalibrated soft
// comparisons. Suppress only overlapping archive ranking contributions, never
// the original evidence used for strict screening and disagreement reporting.
func preferCLAPRanking(comparisons []core.IntentComparison, preview core.AudioAssessment) []core.IntentComparison {
	covered := map[core.AudioClause]bool{}
	for _, a := range preview.Clauses {
		if a.ScoreAvailable {
			covered[a.Clause] = true
		}
	}
	out := append([]core.IntentComparison(nil), comparisons...)
	for i := range out {
		if covered[out[i].Clause] {
			out[i].AcousticScore = nil
		}
	}
	return out
}

// Map only class meanings supported by the source ontology. In particular,
// mood_electronic is not a genre and genre_electronic is a conditional subgenre
// classifier: neither establishes membership in electronic music. Unmapped
// phrases (e.g. microdetail, sleepy, hybrids) remain unknown, not approximated.
func acousticClass(kind, text, model string) string {
	// The schema-validated thesaurus is the sole mapping authority. Constructing
	// classifier names from arbitrary prompt words would bypass sense review.
	if concept, ok := musicconcepts.Find(kind, text); ok {
		return concept.Providers.AcousticBrainz[model]
	}
	return ""
}

func acousticComparisons(track core.EnrichedTrack, clauses []core.AudioClause) []core.IntentComparison {
	out := make([]core.IntentComparison, len(clauses))
	a := track.Acoustic
	valid := track.Matched && track.IdentityStatus == core.ResolutionResolved && a != nil && a.SchemaVersion == 1 && a.HighStatus == "available" && a.Source != "" && a.RecordingID != "" && strings.EqualFold(a.RecordingID, track.RecordingID)
	for i, clause := range clauses {
		c := core.IntentComparison{Clause: clause, AcousticState: "unknown", PreviewState: core.EvidenceUnknown}
		var sum float64
		positive, negative := false, false
		if valid {
			models := make([]string, 0, len(a.Predictions))
			for model := range a.Predictions {
				models = append(models, model)
			}
			sort.Strings(models) // floating point accumulation and history are deterministic
			for _, model := range models {
				p := a.Predictions[model]
				label := acousticClass(clause.Kind, clause.Text, model)
				value, ok := p.Classes[label]
				if label == "" || !ok || len(p.Version) == 0 || len(p.Classes) < 2 {
					continue
				}
				good, total, competing := true, 0.0, 0.0
				for class, score := range p.Classes {
					good = good && !math.IsNaN(score) && !math.IsInf(score, 0) && score >= 0 && score <= 1
					total += score
					if class != label {
						competing = max(competing, score)
					}
				}
				if !good || math.Abs(total-1) > .02 {
					continue
				}
				margin := value - competing
				if clause.Negative {
					margin = -margin
				}
				sum += margin
				c.Models = append(c.Models, model)
				positive = positive || margin >= .5
				negative = negative || margin <= -.5
			}
		}
		if len(c.Models) > 0 {
			score := sum / float64(len(c.Models))
			c.AcousticScore = &score
			switch {
			case positive && negative:
				c.AcousticState = "conflicting"
			case score >= .5:
				c.AcousticState = "supporting"
			case score <= -.5:
				c.AcousticState = "opposing"
			}
		}
		out[i] = c
	}
	return out
}

// Fixed clause denominators keep missing evidence from improving a candidate's
// score. Journey stages are alternatives, not simultaneous playlist demands.
func acousticIntentScore(comparisons []core.IntentComparison) (float64, bool) {
	// Margins already express signed clause satisfaction. Reuse the semantic
	// grouping rules with polarity cleared so it is never applied twice.
	assessment := core.AudioAssessment{PolicyVersion: audio.QueryPolicyVersion}
	available := false
	for _, c := range comparisons {
		clause := c.Clause
		clause.Negative = false
		value := 0.0
		if c.AcousticScore != nil {
			value, available = *c.AcousticScore, true
		}
		assessment.Clauses = append(assessment.Clauses, core.AudioClauseAssessment{
			Clause: clause, Score: value, ScoreAvailable: true,
		})
	}
	var scored core.Candidate
	audio.ApplyScores(&scored, assessment)
	return scored.Scores.SemanticMatch, available
}

// Conservative screening, not proof of a category: strong opposition or model
// disagreement cannot support an essential/strict clause. Unknowns still need
// the ordinary eligibility policy; positive predictions never grant eligibility.
func acousticCompatible(comparisons []core.IntentComparison) bool {
	type group struct {
		scope   string
		opposed bool
	}
	groups := map[string]group{}
	for i, c := range comparisons {
		if !c.Clause.Essential && !c.Clause.Strict {
			continue
		}
		key := c.Clause.Scope + "\x00" + c.Clause.Group
		if c.Clause.CoverageGroup != "" && !c.Clause.Negative {
			key = c.Clause.Scope + "\x00coverage:" + c.Clause.CoverageGroup
		} else if c.Clause.Group == "" {
			key += fmt.Sprint("\x00", i)
		}
		prior, exists := groups[key]
		opposed := c.AcousticState == "opposing" || c.AcousticState == "conflicting"
		if exists {
			opposed = opposed && prior.opposed
		}
		groups[key] = group{c.Clause.Scope, opposed}
	}
	stages := map[string]bool{}
	for _, g := range groups {
		if strings.HasPrefix(g.scope, "journey_") {
			stages[g.scope] = stages[g.scope] || g.opposed
		} else if g.opposed {
			return false
		}
	}
	for _, opposed := range stages {
		if !opposed {
			return true
		}
	}
	return len(stages) == 0
}

func acousticCompatibleFor(intent core.MusicIntent, comparisons []core.IntentComparison) bool {
	if intent.Controls.RecommendationMode != core.EnhancedHybrid || intent.VerificationPolicy != core.BestAvailable {
		return acousticCompatible(comparisons)
	}
	// An uncalibrated archive disagreement is a visible close-match warning,
	// not a veto of a defining/soft request before fresh audio can be heard.
	// Explicit strict clauses retain exactly the conservative screening rule.
	checks := append([]core.IntentComparison(nil), comparisons...)
	for i := range checks {
		if !checks[i].Clause.Strict {
			checks[i].Clause.Essential = strings.HasPrefix(checks[i].Clause.Scope, "journey_")
			checks[i].AcousticState = "unknown"
		}
		if checks[i].Clause.Group != "" || checks[i].Clause.CoverageGroup != "" && !checks[i].Clause.Negative {
			// Required OR groups are proved collectively by essential checking;
			// opposition to one alternative does not oppose the entire group.
			checks[i].Clause.Strict, checks[i].Clause.Essential = false, false
		}
	}
	return acousticCompatible(checks)
}

// Both iterative stopping decisions and final selection use the newest
// request-owned evidence snapshot, never stale preparation-only metadata.
func (o *Orchestrator) rankCandidates(ctx context.Context, candidates []core.Candidate, request ports.RankRequest) ([]core.Candidate, error) {
	request.Intent.Knowledge = o.knowledge
	request.PreviewAssessments = make(map[string]core.AudioAssessment, len(candidates))
	for _, candidate := range candidates {
		if a, ok, err := o.packedAssessment(ctx, candidate.Track.ID); err != nil {
			return nil, err
		} else if ok {
			request.PreviewAssessments[candidate.Track.ID] = a
		}
	}
	if o.audioSession != nil {
		for _, candidate := range candidates {
			if a, ok := o.audioSession.Assessment(candidate.Track.ID); ok {
				if packed, exists := request.PreviewAssessments[candidate.Track.ID]; exists && o.enhanced {
					request.PreviewAssessments[candidate.Track.ID] = combineSemanticObservations(packed, a)
				} else if !exists || a.Eligible {
					request.PreviewAssessments[candidate.Track.ID] = a
				}
			}
		}
	}
	ranked, err := o.ranker.Rank(ctx, candidates, request)
	if err != nil {
		return nil, err
	}
	if o.enhanced && o.bestAvailable {
		for i := range ranked {
			ranked[i].FitTier, ranked[i].MatchDetail = o.enhancedTier(ctx, ranked[i], request.Intent)
			ranked[i].Criteria = o.candidateCriteria(ctx, ranked[i].Track.ID, request.Intent)
		}
		// Journey reservations consume this order before the selector runs.
		// Preserve request-fit ordering inside each evidence tier.
		sort.SliceStable(ranked, func(i, j int) bool {
			return ranked[i].FitTier == fitStrong && ranked[j].FitTier != fitStrong
		})
	}
	return ranked, nil
}

// Prefer decisive, identity-grounded archived predictions for overlapping
// concepts. Preview evidence is retained for explanations and strict checks;
// only its duplicate ranking contribution is removed. Missing/weak/conflicting
// archive evidence leaves CLAP in charge of that clause. Original clause counts
// remain denominators, so suppressing a clause never boosts the remaining ones.
func preferAcousticRanking(candidate *core.Candidate, comparisons []core.IntentComparison, preview core.AudioAssessment) {
	covered := map[core.AudioClause]bool{}
	for _, c := range comparisons {
		if c.AcousticState == "supporting" || c.AcousticState == "opposing" {
			covered[c.Clause] = true
		}
	}
	if len(covered) == 0 {
		return
	}
	if strings.Contains(preview.PolicyVersion, audio.QueryPolicyVersion) {
		masked := preview
		masked.Clauses = append([]core.AudioClauseAssessment(nil), preview.Clauses...)
		positiveAvailable, negativeAvailable := false, false
		for i := range masked.Clauses {
			a := &masked.Clauses[i]
			if a.Clause.Negative {
				negativeAvailable = negativeAvailable || a.ScoreAvailable
			} else {
				positiveAvailable = positiveAvailable || a.ScoreAvailable
			}
			if !a.ScoreAvailable || covered[a.Clause] {
				a.Score = 0
			}
			// Retain the fixed denominator when suppressing duplicate evidence.
			a.ScoreAvailable = true
		}
		var scored core.Candidate
		audio.ApplyScores(&scored, masked)
		if positiveAvailable {
			candidate.Scores.SemanticMatch = scored.Scores.SemanticMatch
		}
		if negativeAvailable {
			candidate.Scores.SemanticNegativeMatch = scored.Scores.SemanticNegativeMatch
		}
		return
	}
	var positive, negative float64
	var positives, negatives int
	positiveAvailable, negativeAvailable := false, false
	stages := map[string]float64{}
	stageCounts := map[string]int{}
	for _, a := range preview.Clauses {
		score := 0.0
		if a.ScoreAvailable && !covered[a.Clause] {
			score = a.Score
		}
		if a.Clause.Negative {
			negatives++
			negative += score
			negativeAvailable = negativeAvailable || a.ScoreAvailable
		} else {
			positiveAvailable = positiveAvailable || a.ScoreAvailable
			if strings.HasPrefix(a.Clause.Scope, "journey_") {
				stages[a.Clause.Scope] += score
				stageCounts[a.Clause.Scope]++
			} else {
				positives++
				positive += score
			}
		}
	}
	if len(stages) > 0 {
		best := -1.0
		for scope, score := range stages {
			best = max(best, score/float64(stageCounts[scope]))
		}
		positive += best
		positives++
	}
	if positiveAvailable {
		candidate.Scores.SemanticMatch = positive / float64(max(1, positives))
	}
	if negativeAvailable {
		candidate.Scores.SemanticNegativeMatch = negative / float64(max(1, negatives))
	}
}

func mergePreviewComparisons(comparisons []core.IntentComparison, preview []core.AudioClauseAssessment) {
	for i := range comparisons {
		c := &comparisons[i]
		for _, p := range preview {
			if p.Clause != c.Clause {
				continue
			}
			c.PreviewState = p.State
			if p.ScoreAvailable {
				score := p.Score
				c.PreviewScore = &score
			}
		}
		c.Conflict = c.AcousticState == "conflicting" || c.PreviewState == core.EvidenceMatch && c.AcousticState == "opposing" || c.PreviewState == core.EvidenceMismatch && c.AcousticState == "supporting"
	}
}

func (o *Orchestrator) annotateIntentComparisonsContext(ctx context.Context, playlist *core.Playlist) {
	clauses := audio.Clauses(playlist.Intent)
	if len(clauses) == 0 {
		return
	}
	preview := map[string]core.AudioAssessment{}
	anyConflict := false
	if o.audioSession != nil {
		for _, a := range o.audioSession.Snapshot().Assessments {
			preview[a.TrackID] = a
		}
	}
	for _, track := range playlist.Tracks {
		metadata, _ := o.knowledgeTrackContext(ctx, track.ID)
		comparisons := acousticComparisons(metadata, clauses)
		mergePreviewComparisons(comparisons, preview[track.ID].Clauses)
		conflict := false
		for _, c := range comparisons {
			conflict = conflict || c.Conflict || c.AcousticState == "opposing" && !strings.HasPrefix(c.Clause.Scope, "journey_")
		}
		index := -1
		for i := range playlist.Assessments {
			if playlist.Assessments[i].TrackID == track.ID {
				index = i
				break
			}
		}
		if index < 0 {
			playlist.Assessments = append(playlist.Assessments, core.TrackAssessment{TrackID: track.ID, State: core.EvidenceUnknown})
			index = len(playlist.Assessments) - 1
		}
		playlist.Assessments[index].Comparisons = comparisons
		if conflict {
			anyConflict = true
			playlist.Assessments[index].State = core.EvidenceUnknown
			playlist.Assessments[index].Reasons = append(playlist.Assessments[index].Reasons, "Acoustic predictions oppose part of the request or disagree with preview analysis; musical fit is uncertain.")
			if playlist.Outcome.State == core.OutcomeFulfilled {
				playlist.Outcome.State = core.OutcomePartial
			}
		}
	}
	if anyConflict {
		playlist.Outcome.Reasons = append(playlist.Outcome.Reasons, core.OutcomeReason{Code: "intent_analysis_disagreement", Detail: "Some selected tracks have archived predictions that oppose the request or disagree with preview analysis.", Action: "review the per-track intent comparisons, replace uncertain tracks, or refine the request"})
	}
}

func (o *Orchestrator) annotateIntentComparisons(playlist *core.Playlist) {
	ctx := o.requestContext
	if ctx == nil {
		ctx = context.Background()
	}
	o.annotateIntentComparisonsContext(ctx, playlist)
}

// Retain the source snapshots for replay; merge only ranking observations. Each
// clause contributes once, preferring coverage without consulting its score.
func combineSemanticObservations(left, right core.AudioAssessment) core.AudioAssessment {
	observation := func(a core.AudioAssessment) core.AudioObservation {
		out := core.AudioObservation{Fingerprint: audio.Fingerprint(a)}
		if a.Coverage != nil {
			out.Coverage = *a.Coverage
		}
		if a.LibraryCoverage != nil {
			out.Coverage = core.PreviewCoverage{Available: true, CoveredSeconds: a.LibraryCoverage.CoveredSeconds}
		}
		return out
	}
	if preferObservation(observation(right), observation(left)) {
		left, right = right, left
	}
	result := left
	result.Clauses = append([]core.AudioClauseAssessment(nil), left.Clauses...)
	indices := map[core.AudioClause]int{}
	for i, a := range result.Clauses {
		indices[a.Clause] = i
	}
	for _, a := range right.Clauses {
		if index, exists := indices[a.Clause]; exists {
			if !result.Clauses[index].ScoreAvailable && a.ScoreAvailable {
				result.Clauses[index] = a
			}
		} else {
			indices[a.Clause] = len(result.Clauses)
			result.Clauses = append(result.Clauses, a)
		}
	}
	result.PolicyVersion = audio.QueryPolicyVersion
	return result
}
