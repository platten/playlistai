package multichannel

import (
	"context"
	"fmt"
	"math"
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
		metadataScore := 0.0
		metadata, metadataAvailable := o.knowledgeTrack(candidate.Track.ID)
		stages, failedStages, matchedStages := map[string]bool{}, map[string]bool{}, map[string]bool{}
		for _, group := range groups {
			state := o.criterionGroupState(ctx, candidate.Track.ID, group)
			required := false
			metadataGroupScore := 0.0
			for _, c := range group {
				required = required || requiredCriterion(c)
				if metadataAvailable && o.recordingTagCriterion(metadata, c) == core.EvidenceMatch {
					metadataGroupScore = 1
				} else if metadataGroupScore < .75 && o.compoundGenreSupport(ctx, candidate.Track.ID, c) {
					// Two sourced component tags are useful direct request
					// evidence, but remain weaker than an exact category tag.
					metadataGroupScore = .75
				}
				if o.bestCriterion(ctx, candidate.Track.ID, c) == core.EvidenceMatch {
					report.Matched[criterionKey(c)]++
					report.Scores[candidate.Track.ID] = 1
				}
			}
			metadataScore += metadataGroupScore
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
		if metadataScore > 0 && len(groups) > 0 {
			candidate.Available.LibraryMetadata = true
			candidate.Scores.LibraryMetadata = max(candidate.Scores.LibraryMetadata, metadataScore/float64(len(groups)))
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
	if o.packedSupport(ctx, candidate.Track.ID) {
		return true
	}
	for _, c := range criteria {
		if o.bestCriterion(ctx, candidate.Track.ID, c) == core.EvidenceMatch {
			return true
		}
		if o.compoundGenreSupport(ctx, candidate.Track.ID, c) {
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

func (o *Orchestrator) compoundGenreSupport(ctx context.Context, id string, criterion core.MusicalCriterion) bool {
	if support, ok := o.cat.(interface {
		CompoundGenreSupport(context.Context, string, core.MusicalCriterion) bool
	}); ok {
		return support.CompoundGenreSupport(ctx, id, criterion)
	}
	return false
}

// Direct request comparisons, without taste, exposure, retrieval frequency or
// optional DSP/MERT bonuses. The existing selection-floor parameters are an
// engineering ranking guard, never a calibrated musical-fit claim.
func enhancedRequestRelevance(candidate core.Candidate, intent core.MusicIntent) (float64, bool) {
	best, available := 0.0, false
	if candidate.Available.LibraryMetadata {
		best, available = candidate.Scores.LibraryMetadata, true
	}
	references := intent.References
	hasPositive := false
	for _, ref := range references {
		if ref.Influence == core.InfluenceNegative {
			continue
		}
		hasPositive = hasPositive || ref.TrackID != ""
		if ref.Resolution != nil && ref.Resolution.Selected != nil {
			for _, rep := range ref.Resolution.Selected.Representatives {
				hasPositive = hasPositive || rep.TrackID != "" && rep.Weight > 0
			}
		}
	}
	if !hasPositive {
		references = intent.RequiredTracks
	}
	// A local MERT retrieval comparison is direct reference evidence, not
	// a taste/ownership bonus or categorical musical proof. Keep its native
	// cosine scale and require provenance plus an explicit positive anchor.
	for _, source := range candidate.Sources {
		liveMERT := source.Channel == ChannelMERTAudio
		libraryMERT := source.Channel == "library_mert" && source.LibrarySource != nil && source.LibrarySource.SpaceID != ""
		if (!liveMERT && !libraryMERT) || math.IsNaN(source.Score) || math.IsInf(source.Score, 0) {
			continue
		}
		for _, ref := range references {
			if ref.Influence == core.InfluenceNegative {
				continue
			}
			matchesQuery := func(trackID string) bool {
				return trackID != "" && (trackID == source.QueryID || liveMERT && strings.HasSuffix(source.QueryID, ":"+trackID))
			}
			matches := matchesQuery(ref.TrackID)
			if ref.Resolution != nil && ref.Resolution.Selected != nil {
				for _, rep := range ref.Resolution.Selected.Representatives {
					matches = matches || rep.Weight > 0 && matchesQuery(rep.TrackID)
				}
			}
			if matches && (!available || source.Score > best) {
				best, available = clamp(source.Score, -1, 1), true
			}
		}
	}
	if candidate.Available.SemanticMatch {
		score := candidate.Scores.SemanticMatch - max(0, candidate.Scores.SemanticNegativeMatch)
		if !available || score > best {
			best = score
		}
		available = true
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

// CLAP bundles expose cosine comparisons, not probabilities or a globally
// calibrated relevance scale. Preserve their direct-evidence ordering by
// expressing positive comparisons relative to the best observed comparison in
// the request pool. Non-positive and unsupported candidates stay below the
// unchanged floor; stronger metadata and reference scores keep native values.
func enhancedRequestRelevances(candidates []core.Candidate, intent core.MusicIntent) map[int]struct {
	value float64
	ok    bool
} {
	type relevance struct {
		value float64
		ok    bool
	}
	values := make([]relevance, len(candidates))
	bestSemantic := 0.0
	for i, candidate := range candidates {
		values[i].value, values[i].ok = enhancedRequestRelevance(candidate, intent)
		if candidate.Available.SemanticMatch {
			direct := candidate.Scores.SemanticMatch - max(0, candidate.Scores.SemanticNegativeMatch)
			bestSemantic = max(bestSemantic, direct)
		}
	}
	out := make(map[int]struct {
		value float64
		ok    bool
	}, len(values))
	for i, item := range values {
		if bestSemantic > 0 && candidates[i].Available.SemanticMatch {
			direct := candidates[i].Scores.SemanticMatch - max(0, candidates[i].Scores.SemanticNegativeMatch)
			if direct > 0 {
				item.value = max(item.value, direct/bestSemantic)
				item.ok = true
			}
		}
		out[i] = struct {
			value float64
			ok    bool
		}{item.value, item.ok}
	}
	return out
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
		if !clause.Negative && !clause.Strict && o.compoundGenreSupport(ctx, candidate.Track.ID, core.MusicalCriterion{Kind: clause.Kind, Value: clause.Text}) {
			anyMatch = true
		}
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
	return anyMatch || o.packedSupport(ctx, candidate.Track.ID)
}
func (o *Orchestrator) clauseFitState(ctx context.Context, id string, clause core.AudioClause, preview core.AudioAssessment) core.EvidenceState {
	for _, p := range preview.Clauses {
		if p.Clause == clause && p.State != core.EvidenceUnknown && p.State != "" {
			return p.State // preview assessments already incorporate polarity
		}
	}
	if clause.Kind == "instrumentation" && (clause.Degree == "mostly" || clause.Degree == "reduced") {
		// Credits establish presence, not how prominent an instrument sounds.
		return core.EvidenceUnknown
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
	if preview.AnalysisID != "" {
		return fitStrong, "Grounded evidence supports the requested musical characteristics; preview evidence covers only the sampled audio."
	}
	return fitStrong, "Grounded catalog evidence supports the requested musical characteristics."
}

// previewNeededForEnhanced avoids remote preview acquisition only when the
// request-owned catalog evidence already proves every applicable facet. Close
// matches and strict vocal exclusions still require the normal preview path.
func (o *Orchestrator) previewNeededForEnhanced(ctx context.Context, candidate core.Candidate, intent core.MusicIntent) bool {
	if !o.enhanced || !o.bestAvailable {
		return true
	}
	// Recording metadata may corroborate an instrumental request, but the
	// available preview must still be screened so detected vocals take
	// precedence over that tag.
	if core.WantsInstrumental(intent) {
		return true
	}
	// Conjunctive recording tags can admit a labeled close fit without
	// exhausting the prompt budget on preview acquisition. Strict and
	// negative clauses retain their ordinary verification path.
	if o.enhancedMetadataFallback(ctx, candidate, intent) {
		for _, clause := range audio.Clauses(intent) {
			if clause.Negative || clause.Strict {
				return true
			}
			if o.compoundGenreSupport(ctx, candidate.Track.ID, core.MusicalCriterion{Kind: clause.Kind, Value: clause.Text}) {
				return false
			}
		}
	}
	tier, _ := o.enhancedTier(ctx, candidate, intent)
	return tier != fitStrong
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
