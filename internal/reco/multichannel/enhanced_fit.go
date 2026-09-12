package multichannel

import (
	"context"
	"fmt"
	"strings"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/musicconcepts"
)

const fitStrong = "strong"
const fitClose = "close"

func requiredCriterion(c core.MusicalCriterion) bool { return c.Strength == "required" }

// Criterion groups are OR alternatives within one scope. Separate groups are
// independent demands; journey scopes are alternatives until stage placement.
func criterionGroups(criteria []core.MusicalCriterion) [][]core.MusicalCriterion {
	var out [][]core.MusicalCriterion
	positions := map[string]int{}
	for i, c := range criteria {
		key := c.Scope + "\x00" + c.Group
		if c.Group == "" {
			key += fmt.Sprint("\x00", i)
		}
		if index, ok := positions[key]; ok {
			out[index] = append(out[index], c)
		} else {
			positions[key] = len(out)
			out = append(out, []core.MusicalCriterion{c})
		}
	}
	return out
}

func (o *Orchestrator) criterionGroupState(ctx context.Context, id string, group []core.MusicalCriterion) core.EvidenceState {
	unknown := false
	for _, c := range group {
		switch o.bestCriterion(ctx, id, c) {
		case core.EvidenceMatch:
			return core.EvidenceMatch
		case core.EvidenceMismatch:
		default:
			unknown = true
		}
	}
	if unknown {
		return core.EvidenceUnknown
	}
	return core.EvidenceMismatch
}

func (o *Orchestrator) filterEnhancedEssential(ctx context.Context, candidates []core.Candidate, criteria []core.MusicalCriterion) ([]core.Candidate, essentialEvidenceReport, error) {
	report := essentialEvidenceReport{Eligible: map[string]bool{}, Matched: map[string]int{}, Scores: map[string]float64{}, Sources: map[string][]core.FeatureProvenance{}}
	out := make([]core.Candidate, 0, len(candidates))
	groups := criterionGroups(criteria)
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, report, err
		}
		eligible, allMatched := true, len(groups) > 0
		stages, failedStages, matchedStages := map[string]bool{}, map[string]bool{}, map[string]bool{}
		for _, group := range groups {
			state := o.criterionGroupState(ctx, candidate.Track.ID, group)
			required := false
			for _, c := range group {
				required = required || requiredCriterion(c)
				if o.bestCriterion(ctx, candidate.Track.ID, c) == core.EvidenceMatch {
					report.Matched[criterionKey(c)]++
					report.Scores[candidate.Track.ID] = 1
				}
			}
			fails := state == core.EvidenceMismatch || required && state != core.EvidenceMatch
			scope := group[0].Scope
			if strings.HasPrefix(scope, "journey_") {
				stages[scope] = true
				failedStages[scope] = failedStages[scope] || fails
				if _, exists := matchedStages[scope]; !exists {
					matchedStages[scope] = true
				}
				matchedStages[scope] = matchedStages[scope] && state == core.EvidenceMatch
			} else {
				eligible = eligible && !fails
				allMatched = allMatched && state == core.EvidenceMatch
			}
		}
		if len(stages) > 0 {
			possible, matched := false, false
			for stage := range stages {
				possible = possible || !failedStages[stage]
				matched = matched || matchedStages[stage]
			}
			eligible = eligible && possible
			allMatched = allMatched && matched
		}
		// Missing evidence is not a wrong category, but is not a reason to pad
		// from arbitrary catalog entries. Require a requested comparison or
		// affirmative requested metadata before admitting an unknown close fit.
		if eligible && len(groups) > 0 && !allMatched && !o.enhancedSupport(ctx, candidate, criteria) {
			eligible = false
		}
		if !eligible {
			continue
		}
		candidate.MusicalFit, candidate.FitTier = core.EvidenceUnknown, fitClose
		candidate.MatchDetail = "Close suggestion; some requested characteristics have incomplete evidence."
		if allMatched {
			candidate.MusicalFit, candidate.FitTier = core.EvidenceMatch, fitStrong
			candidate.MatchDetail = "Grounded evidence supports the requested defining characteristics."
		}
		report.Eligible[candidate.Track.ID] = true
		out = append(out, candidate)
	}
	return out, report, ctx.Err()
}

func (o *Orchestrator) enhancedSupport(ctx context.Context, candidate core.Candidate, criteria []core.MusicalCriterion) bool {
	for _, c := range criteria {
		if o.bestCriterion(ctx, candidate.Track.ID, c) == core.EvidenceMatch {
			return true
		}
	}
	if o.audioSession != nil {
		if a, ok := o.audioSession.Assessment(candidate.Track.ID); ok && a.AnalysisID != "" {
			var compared core.Candidate
			audio.ApplyScores(&compared, a)
			return compared.Available.SemanticMatch && compared.Scores.SemanticMatch > max(0, compared.Scores.SemanticNegativeMatch)
		}
	}
	// A grounded semantic index supplies an explicit request comparison. The
	// existing relevance floor still controls final selection; this is not a
	// calibrated musical-fit threshold and cannot grant a strong tier.
	return candidate.Available.SemanticMatch && candidate.Scores.SemanticMatch > max(0, candidate.Scores.SemanticNegativeMatch)
}

// Direct request comparisons, without taste, exposure, retrieval frequency or
// optional DSP/MERT bonuses. The existing selection-floor parameters are an
// engineering ranking guard, never a calibrated musical-fit claim.
func enhancedRequestRelevance(candidate core.Candidate, intent core.MusicIntent) (float64, bool) {
	best, available := 0.0, false
	if candidate.Available.SemanticMatch {
		best, available = candidate.Scores.SemanticMatch-max(0, candidate.Scores.SemanticNegativeMatch), true
	}
	var affinity, weight float64
	if candidate.Available.AudioSeedAffinity {
		affinity += intent.Controls.AudioWeight * candidate.Scores.AudioSeedAffinity
		weight += intent.Controls.AudioWeight
	}
	if candidate.Available.CooccurrenceAffinity {
		affinity += intent.Controls.CooccurrenceWeight * candidate.Scores.CooccurrenceAffinity
		weight += intent.Controls.CooccurrenceWeight
	}
	if weight > 0 {
		if !available || affinity/weight > best {
			best = affinity / weight
		}
		available = true
	}
	if candidate.Available.AcousticIntent {
		if !available || candidate.Scores.AcousticIntent > best {
			best = candidate.Scores.AcousticIntent
		}
		available = true
	}
	return best, available
}

func enhancedCategoryMatches(want, actual string, graph core.GenreGraph) bool {
	want = musicconcepts.Canonical("genre", want)
	actual = musicconcepts.Canonical("genre", actual)
	if strings.EqualFold(want, actual) || graph.Matches(want, actual) {
		return true
	}
	w, wok := musicconcepts.Find("genre", want)
	a, aok := musicconcepts.Find("genre", actual)
	if !wok || !aok {
		return false
	}
	pending, seen := append([]string(nil), a.Parents...), map[string]bool{}
	for len(pending) > 0 {
		id := pending[0]
		pending = pending[1:]
		if id == w.ID {
			return true
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if parent, ok := musicconcepts.FindID(id); ok {
			pending = append(pending, parent.Parents...)
		}
	}
	return false
}

// A missing preview can be replaced only by affirmative metadata about a
// requested facet, and only after every explicit strict clause is proved.
func (o *Orchestrator) enhancedMetadataFallback(ctx context.Context, candidate core.Candidate, intent core.MusicIntent) bool {
	if !o.enhanced || !o.bestAvailable {
		return false
	}
	var preview core.AudioAssessment
	if o.audioSession != nil {
		preview, _ = o.audioSession.Assessment(candidate.Track.ID)
	}
	type requirement struct {
		scope   string
		matched bool
	}
	requirements := map[string]requirement{}
	stages := map[string]bool{}
	anyMatch := false
	for i, clause := range audio.Clauses(intent) {
		state := o.clauseFitState(ctx, candidate.Track.ID, clause, preview)
		anyMatch = anyMatch || !clause.Negative && state == core.EvidenceMatch
		if strings.HasPrefix(clause.Scope, "journey_") {
			stages[clause.Scope] = true
		}
		if !clause.Strict {
			continue
		}
		key := clause.Scope + "\x00" + clause.Group
		if clause.Group == "" {
			key += fmt.Sprint("\x00", i)
		}
		r := requirements[key]
		r.scope, r.matched = clause.Scope, r.matched || state == core.EvidenceMatch
		requirements[key] = r
	}
	for _, r := range requirements {
		if strings.HasPrefix(r.scope, "journey_") {
			stages[r.scope] = stages[r.scope] && r.matched
		} else if !r.matched {
			return false
		}
	}
	if len(stages) > 0 {
		possible := false
		for _, eligible := range stages {
			possible = possible || eligible
		}
		if !possible {
			return false
		}
	}
	return anyMatch
}
func (o *Orchestrator) clauseFitState(ctx context.Context, id string, clause core.AudioClause, preview core.AudioAssessment) core.EvidenceState {
	for _, p := range preview.Clauses {
		if p.Clause == clause && p.State != core.EvidenceUnknown && p.State != "" {
			return p.State // preview assessments already incorporate polarity
		}
	}
	state := o.bestCriterion(ctx, id, core.MusicalCriterion{Kind: clause.Kind, Value: clause.Text, Scope: clause.Scope})
	if clause.Negative {
		if state == core.EvidenceMismatch {
			return core.EvidenceMatch
		}
		if state == core.EvidenceMatch {
			return core.EvidenceMismatch
		}
	}
	return state
}

func (o *Orchestrator) enhancedStageConstraints(ctx context.Context, id, scope string, intent core.MusicIntent) bool {
	if !o.enhanced {
		return true
	}
	var preview core.AudioAssessment
	if o.audioSession != nil {
		preview, _ = o.audioSession.Assessment(id)
	}
	groups := map[string]bool{}
	for i, clause := range audio.Clauses(intent) {
		if !clause.Strict || clause.Scope != scope {
			continue
		}
		key := clause.Group
		if key == "" {
			key = fmt.Sprint("clause-", i)
		}
		groups[key] = groups[key] || o.clauseFitState(ctx, id, clause, preview) == core.EvidenceMatch
	}
	for _, matched := range groups {
		if !matched {
			return false
		}
	}
	return true
}

func (o *Orchestrator) enhancedTier(ctx context.Context, candidate core.Candidate, intent core.MusicIntent) (string, string) {
	clauses := audio.Clauses(intent)
	var gaps []string
	var preview core.AudioAssessment
	if o.audioSession != nil {
		preview, _ = o.audioSession.Assessment(candidate.Track.ID)
	}
	metadata, _ := o.knowledgeTrack(candidate.Track.ID)
	type unit struct {
		scope   string
		texts   []string
		matched bool
	}
	var units []unit
	positions := map[string]int{}
	for i, comparison := range acousticComparisons(metadata, clauses) {
		clause := comparison.Clause
		key := clause.Scope + "\x00" + clause.Group
		if clause.Group == "" {
			key += fmt.Sprint("\x00", i)
		}
		index, exists := positions[key]
		if !exists {
			index = len(units)
			positions[key] = index
			units = append(units, unit{scope: clause.Scope})
		}
		units[index].texts = appendUniqueString(units[index].texts, clause.Text)
		matched := o.clauseFitState(ctx, candidate.Track.ID, clause, preview) == core.EvidenceMatch
		matched = matched && comparison.AcousticState != "opposing" && comparison.AcousticState != "conflicting"
		units[index].matched = units[index].matched || matched
	}
	stages := map[string]bool{}
	for _, u := range units {
		if strings.HasPrefix(u.scope, "journey_") {
			if _, exists := stages[u.scope]; !exists {
				stages[u.scope] = true
			}
			stages[u.scope] = stages[u.scope] && u.matched
		} else if !u.matched {
			gaps = appendUniqueString(gaps, strings.Join(u.texts, " or "))
		}
	}
	if len(stages) > 0 {
		matched := false
		for _, value := range stages {
			matched = matched || value
		}
		if !matched {
			gaps = appendUniqueString(gaps, "journey-stage characteristics")
		}
	}
	for _, period := range intent.Temporal {
		first, last := metadata.CompositionStartYear, metadata.CompositionEndYear
		if period.Basis == "original_release" {
			first = yearFromDate(metadata.OriginalReleaseDate)
			last = first
		}
		if last == 0 {
			last = first
		}
		if first == 0 || last < period.StartYear || first > period.EndYear {
			gaps = appendUniqueString(gaps, "requested era")
		}
	}
	for _, unsupported := range intent.Unsupported {
		gaps = appendUniqueString(gaps, unsupported.Text)
	}
	if len(clauses) == 0 || len(gaps) > 0 {
		if len(gaps) == 0 {
			return fitClose, "Close suggestion based on reference similarity; musical fit is not independently verified."
		}
		return fitClose, "Close suggestion; incomplete or conflicting evidence for " + strings.Join(gaps, ", ") + "."
	}
	return fitStrong, "Grounded evidence supports the requested musical characteristics; preview evidence covers only the sampled audio."
}
func (o *Orchestrator) annotateEnhancedFit(ctx context.Context, playlist *core.Playlist) {
	closeCount := 0
	for _, track := range playlist.Tracks {
		candidate := core.Candidate{Track: track}
		if checked, _, err := o.filterEnhancedEssential(ctx, []core.Candidate{candidate}, playlist.Intent.EssentialCriteria); err == nil && len(checked) > 0 {
			candidate = checked[0]
		}
		tier, detail := o.enhancedTier(ctx, candidate, playlist.Intent)
		if tier == fitClose {
			closeCount++
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
		playlist.Assessments[index].FitTier, playlist.Assessments[index].MatchDetail = tier, detail
		if tier == fitClose {
			playlist.Assessments[index].State = core.EvidenceUnknown
			playlist.Assessments[index].Reasons = appendUniqueString(playlist.Assessments[index].Reasons, detail)
		} else {
			playlist.Assessments[index].State = core.EvidenceMatch
		}
	}
	if closeCount > 0 {
		playlist.Notices = append(playlist.Notices, core.PlaylistNotice{Code: "enhanced_close_matches", Detail: "Strong matches were preferred during selection; close suggestions are labeled with the characteristic whose evidence is incomplete or conflicting.", Requested: playlist.Intent.Count, Actual: closeCount})
		playlist.Outcome.State = core.OutcomePartial
	}
}
