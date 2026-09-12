package multichannel

import (
	"context"
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
var acousticClasses = map[string]map[string]string{
	"genre_dortmund":   {"alternative": "alternative", "blues": "blues", "electronic": "electronic", "jazz": "jazz", "pop": "pop", "rock": "rock"},
	"genre_rosamerica": {"classical": "cla", "hip hop": "hip", "jazz": "jaz", "pop": "pop", "rock": "roc"},
	"genre_tzanetakis": {"blues": "blu", "classical": "cla", "country": "cou", "disco": "dis", "hip hop": "hip", "jazz": "jaz", "metal": "met", "pop": "pop", "reggae": "reg", "rock": "roc"},
}

func acousticClass(kind, text, model string) string {
	// Only exact reviewed aliases enter classifier vocabularies. Conditional
	// electronic subgenre models still require a separate applicability policy.
	if concept, ok := musicconcepts.Find(kind, text); ok && model != "genre_electronic" {
		if label := concept.Providers.AcousticBrainz[model]; label != "" {
			return label
		}
		text = concept.Value
	}
	text = core.NormalizeIdentityPart(text)
	if kind == "genre" || kind == "style" {
		return acousticClasses[model][text]
	}
	if kind == "vocal" && model == "voice_instrumental" {
		switch text {
		case "vocal", "vocals", "voice", "singing":
			return "voice"
		case "instrumental":
			return "instrumental"
		}
	}
	if kind == "mood" {
		if text == "relaxing" {
			text = "relaxed"
		}
		if model == "mood_"+text {
			return text
		}
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
	total, count, available := 0.0, 0, false
	stages := map[string]float64{}
	stageCounts := map[string]int{}
	for _, c := range comparisons {
		score := 0.0
		if c.AcousticScore != nil {
			score = *c.AcousticScore
			available = true
		}
		if strings.HasPrefix(c.Clause.Scope, "journey_") {
			stages[c.Clause.Scope] += score
			stageCounts[c.Clause.Scope]++
		} else {
			total += score
			count++
		}
	}
	if len(stages) > 0 {
		best := -1.0
		for stage, score := range stages {
			best = max(best, score/float64(stageCounts[stage]))
		}
		total += best
		count++
	}
	if count == 0 {
		return 0, false
	}
	return total / float64(count), available
}

// Conservative screening, not proof of a category: strong opposition or model
// disagreement cannot support an essential/strict clause. Unknowns still need
// the ordinary eligibility policy; positive predictions never grant eligibility.
func acousticCompatible(comparisons []core.IntentComparison) bool {
	stages, opposedStages := map[string]bool{}, map[string]bool{}
	for _, c := range comparisons {
		if !c.Clause.Essential && !c.Clause.Strict {
			continue
		}
		opposed := c.AcousticState == "opposing" || c.AcousticState == "conflicting"
		if strings.HasPrefix(c.Clause.Scope, "journey_") {
			stages[c.Clause.Scope] = true
			opposedStages[c.Clause.Scope] = opposedStages[c.Clause.Scope] || opposed
		} else if opposed {
			return false
		}
	}
	for stage := range stages {
		if !opposedStages[stage] {
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
		if checks[i].Clause.Group != "" {
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
	if o.audioSession != nil {
		request.PreviewAssessments = make(map[string]core.AudioAssessment, len(candidates))
		for _, candidate := range candidates {
			if a, ok := o.audioSession.Assessment(candidate.Track.ID); ok {
				request.PreviewAssessments[candidate.Track.ID] = a
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
		}
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

func (o *Orchestrator) annotateIntentComparisons(playlist *core.Playlist) {
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
		metadata, _ := o.knowledgeTrack(track.ID)
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
