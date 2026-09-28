package multichannel

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// Matching is always against a recording-scoped supplied value, never an
// artist reputation or a word in a recording title.
func automaticValueState(kind, value string, clause core.AudioClause) core.EvidenceState {
	state := claimValueState(core.RecordingClaim{Kind: kind, Value: value, State: core.EvidenceMatch}, clause, core.GenreGraph{})
	if clause.Negative {
		state = reverseEvidence(state)
	}
	return state
}

func automaticFamily(source string) string {
	source = strings.ToLower(source)
	switch {
	case strings.Contains(source, "acousticbrainz") || strings.HasPrefix(source, "ab:") || source == "classifier_output":
		return "acousticbrainz"
	case strings.Contains(source, "clap"):
		return "clap"
	case strings.Contains(source, "musicbrainz"):
		return "musicbrainz"
	case strings.Contains(source, "wikidata"):
		return "wikidata"
	case strings.Contains(source, "apple") || strings.Contains(source, "itunes"):
		return "apple"
	default:
		return "imported_metadata" // copies/sidecars with unknown lineage count once
	}
}

func automaticClauseFit(b *automaticBatch, id string, clause core.AudioClause, minimum float64) core.AutomaticClauseFit {
	out := core.AutomaticClauseFit{Clause: clause, State: core.AutomaticUnknown}
	subjective := clause.Kind == "texture" || clause.Kind == "mood" || clause.Kind == "instrumentation"
	validatedAudio := false
	minimum = max(DefaultConfig().SemanticMinimumScore, minimum)
	type evidence struct {
		positive, negative, decisive, audio bool
		signal                              core.AutomaticFitSignal
	}
	families := map[string]evidence{}
	add := func(family string, state core.EvidenceState, decisive, isAudio bool, detail string, coverage *core.PreviewCoverage, library *core.LibraryCLAPCoverage) {
		if state != core.EvidenceMatch && state != core.EvidenceMismatch {
			return
		}
		e := families[family]
		e.positive = e.positive || state == core.EvidenceMatch
		e.negative = e.negative || state == core.EvidenceMismatch
		e.decisive = e.decisive || decisive
		e.audio = e.audio || isAudio
		e.signal = core.AutomaticFitSignal{Family: family, State: state, Detail: detail, Coverage: coverage, LibraryCoverage: library}
		families[family] = e
	}
	meta := b.meta[id]
	recording := b.recordings[id]
	for _, a := range meta.Annotations {
		family := automaticFamily(a.Origin + " " + a.SourceKey)
		// A flattened classifier label lacks its negative classes/model coverage.
		// Preserve it as a single weak hint, never an accepted audio observation.
		if a.Origin == "classifier_output" {
			family = "acousticbrainz"
		}
		state := automaticValueState(a.Kind, a.Value, clause)
		add(family, state, false, false, "Supplied recording metadata; estimate, not a verified musical fact.", nil, nil)
	}
	if recording.IdentityStatus == core.ResolutionResolved {
		for _, claim := range recording.Claims {
			if !attributedRecordingClaim(claim, recording) {
				continue
			}
			state := claimValueState(claim, clause, core.GenreGraph{})
			if clause.Negative {
				state = reverseEvidence(state)
			}
			if state == core.EvidenceUnknown && publisherGenreLabelMatches(recording, claim, clause) {
				state = claim.State
				if clause.Negative {
					state = reverseEvidence(state)
				}
			}
			decisive := decisiveClaimMethod(recording, claim, clause)
			if !decisive {
				continue
			} // ungrounded quote/community votes do not become agreement
			add(automaticFamily(claim.Source.Provider), state, decisive, false, "Applicable recording source: "+claim.Source.URL, nil, nil)
		}
	}
	criterion := core.MusicalCriterion{Kind: clause.Kind, Value: clause.Text}
	seenClassifiers := map[string]bool{}
	for _, prediction := range b.classifiers[id] {
		family := prediction.EvidenceFamily()
		if score, ok := prediction.EstimatedScore(criterion); ok && !seenClassifiers[family] {
			seenClassifiers[family] = true
			coverage := prediction.Coverage
			out.Signals = append(out.Signals, core.AutomaticFitSignal{Family: family, ModelFingerprint: prediction.Fingerprint(),
				Score: score, ScoreAvailable: true, State: core.EvidenceUnknown, LibraryCoverage: &coverage,
				Detail: "Specialist classifier estimate from sampled audio; shared encoder heads count as one source, not independent corroboration."})
		}
	}
	for _, value := range criterionValues(b.features[id], criterion) {
		if !core.ReliableFeature(value) {
			continue
		}
		// A feature score and its copied native metadata are one family unless
		// explicit provenance identifies an independent measured source.
		for _, p := range value.Provenance {
			family := automaticFamily(p.Source)
			state := automaticValueState(clause.Kind, value.Value, clause)
			add(family, state, false, family == "acousticbrainz" || family == "clap", "Supplied feature with source/version provenance.", nil, nil)
		}
	}
	for _, comparison := range acousticComparisons(recording, []core.AudioClause{clause}) {
		state := core.EvidenceUnknown
		switch comparison.AcousticState {
		case "supporting":
			state = core.EvidenceMatch
		case "opposing", "conflicting":
			state = core.EvidenceMismatch
		}
		add("acousticbrainz", state, false, true, "Archived recording classifier margin; categorical estimate, not a probability.", nil, nil)
	}
	if assessment, ok := b.assessments[id]; ok && assessment.AnalysisID != "" && (assessment.LibraryCoverage != nil && assessment.LibraryCoverage.CoveredSeconds > 0 || assessment.Coverage != nil && assessment.Coverage.Available && assessment.Coverage.CoveredSeconds > 0) {
		for _, a := range assessment.Clauses {
			if a.Clause.Kind != clause.Kind || a.Clause.Text != clause.Text || a.Clause.Scope != clause.Scope || a.Clause.Negative != clause.Negative || a.Clause.Degree != clause.Degree {
				continue
			}
			if a.ScoreAvailable && finite(a.Score) && assessment.ModelFingerprint != "" {
				out.Signals = append(out.Signals, core.AutomaticFitSignal{Family: "clap", State: core.EvidenceUnknown,
					Score: a.Score, ScoreAvailable: true, ModelFingerprint: assessment.ModelFingerprint,
					Detail: "Uncalibrated similarity to the complete description; used only for estimated ranking.", Coverage: assessment.Coverage, LibraryCoverage: assessment.LibraryCoverage})
			}
			state := a.State
			if subjective {
				// Legacy categorical states and engineering cosine cutoffs are not
				// listening validation for a literal subjective description.
				for _, calibration := range b.calibrations {
					if !a.ScoreAvailable || !calibration.Supports(a.Score, assessment.ModelFingerprint, clause.Kind, clause.Text) {
						continue
					}
					state = core.EvidenceMatch
					if clause.Negative {
						state = core.EvidenceMismatch
					}
					validatedAudio = true
					add("clap", state, false, true, "Validated estimate for the requested description on sampled audio.", assessment.Coverage, assessment.LibraryCoverage)
					e := families["clap"]
					e.signal.Calibration = &calibration
					families["clap"] = e
					break
				}
				continue
			}
			// An absolute similarity to "instrumental" or "vocals" is not a
			// vocal-presence contrast. Only supplied signed vocal assessments
			// may corroborate metadata or veto a no-vocals requirement.
			if clause.Kind != "vocal" && state != core.EvidenceMatch && state != core.EvidenceMismatch && a.ScoreAvailable && finite(a.Score) {
				// This is an explicit engineering cut on a similarity scale, not a
				// probability calibration. It can corroborate metadata, never stand alone.
				if a.Score >= minimum {
					state = core.EvidenceMatch
				}
				if a.Score <= -minimum {
					state = core.EvidenceMismatch
				}
				// Raw scores describe the positive concept. A supplied known
				// State already describes satisfaction of the signed clause.
				if clause.Negative {
					state = reverseEvidence(state)
				}
			}
			add("clap", state, false, true, "Compatible sampled-audio similarity; uncalibrated estimated agreement.", assessment.Coverage, assessment.LibraryCoverage)
		}
	}
	keys := make([]string, 0, len(families))
	for k := range families {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	positive, metadata, audioSupport, decisive, negative := 0, false, false, false, false
	for _, key := range keys {
		e := families[key]
		if e.positive && e.negative {
			e.signal.State = core.EvidenceMismatch
			e.signal.Detail += " Conflicting values in this source family."
		}
		merged := false
		for i, signal := range out.Signals {
			if signal.Family == e.signal.Family {
				e.signal.Score, e.signal.ScoreAvailable, e.signal.ModelFingerprint = signal.Score, signal.ScoreAvailable, signal.ModelFingerprint
				out.Signals[i] = e.signal
				merged = true
				break
			}
		}
		if !merged {
			out.Signals = append(out.Signals, e.signal)
		}
		negative = negative || e.negative
		if e.positive && !e.negative {
			positive++
			metadata = metadata || !e.audio && key != "acousticbrainz"
			audioSupport = audioSupport || e.audio
			decisive = decisive || e.decisive
		}
	}
	switch {
	case negative:
		out.State = core.AutomaticConflicting
	case decisive || validatedAudio || !subjective && positive >= 2 && metadata && audioSupport:
		out.State = core.AutomaticStrong
	case positive > 0:
		out.State = core.AutomaticPlausible
	}
	return out
}

// OR is evaluated before strictness. Coverage groups are per-track alternatives
// whose separate playlist obligations are checked after selection.
func automaticGroups(fit core.AutomaticFitAssessment, scope string, requiredOnly bool) [][]core.AutomaticClauseFit {
	var groups [][]core.AutomaticClauseFit
	index := map[string]int{}
	for i, c := range fit.Clauses {
		s := c.Clause.Scope
		if s == "" {
			s = "playlist"
		}
		if s != scope {
			continue
		}
		key := c.Clause.Group
		if c.Clause.CoverageGroup != "" && !c.Clause.Negative {
			key = "coverage:" + c.Clause.CoverageGroup
		}
		if key == "" {
			key = fmt.Sprint("clause:", i)
		}
		if j, ok := index[key]; ok {
			groups[j] = append(groups[j], c)
		} else {
			index[key] = len(groups)
			groups = append(groups, []core.AutomaticClauseFit{c})
		}
	}
	if !requiredOnly {
		return groups
	}
	var result [][]core.AutomaticClauseFit
	for _, g := range groups {
		required := false
		for _, c := range g {
			required = required || automaticStrictClause(c.Clause)
		}
		if required {
			result = append(result, g)
		}
	}
	return result
}

func automaticStrictClause(c core.AudioClause) bool {
	return c.Strict || c.Essential && !core.AutomaticEstimatedClause(c)
}
func automaticStateValue(state core.AutomaticFitState) int {
	switch state {
	case core.AutomaticStrong:
		return 3
	case core.AutomaticPlausible:
		return 2
	case core.AutomaticUnknown:
		return 1
	}
	return 0
}
func automaticGroupState(group []core.AutomaticClauseFit) core.AutomaticFitState {
	best := core.AutomaticConflicting
	for _, c := range group {
		if automaticStateValue(c.State) > automaticStateValue(best) {
			best = c.State
		}
	}
	return best
}
func automaticScopeFits(fit core.AutomaticFitAssessment, scope string) bool {
	for _, g := range automaticGroups(fit, scope, true) {
		if automaticGroupState(g) != core.AutomaticStrong {
			return false
		}
	}
	return true
}
func automaticScopeScore(fit core.AutomaticFitAssessment, scope string) (float64, bool) {
	groups := automaticGroups(fit, scope, false)
	if len(groups) == 0 {
		return 0, false
	}
	score := 0.0
	known := false
	for _, g := range groups {
		state := automaticGroupState(g)
		known = known || state != core.AutomaticUnknown
		estimate, estimated := 0.0, false
		for _, c := range g {
			if c.EstimateAvailable {
				estimate = max(estimate, c.EstimateScore)
				estimated = true
			}
		}
		known = known || estimated
		switch state {
		case core.AutomaticStrong:
			score += 1
		case core.AutomaticPlausible:
			score += max(.5, estimate)
		case core.AutomaticConflicting:
			score -= 1
		default:
			score += estimate
		}
	}
	return score / float64(len(groups)), known
}
func automaticAssessment(b *automaticBatch, id string, clauses []core.AudioClause, minimum float64) core.AutomaticFitAssessment {
	fit := core.AutomaticFitAssessment{PolicyVersion: core.AutomaticFitPolicyVersion, State: core.AutomaticUnknown, Detail: "Estimated musical fit from prepared recording evidence; not a calibrated probability or full-recording verification."}
	for _, c := range clauses {
		fit.Clauses = append(fit.Clauses, automaticClauseFit(b, id, c, minimum))
	}
	fit.State = automaticOverall(automaticGroups(fit, "playlist", false))
	return fit
}

func automaticOverall(groups [][]core.AutomaticClauseFit) core.AutomaticFitState {
	if len(groups) == 0 {
		return core.AutomaticUnknown
	}
	allStrong, positive, opposed := true, false, false
	for _, g := range groups {
		state := automaticGroupState(g)
		allStrong = allStrong && state == core.AutomaticStrong
		positive = positive || state == core.AutomaticStrong || state == core.AutomaticPlausible
		opposed = opposed || state == core.AutomaticConflicting
	}
	switch {
	case allStrong:
		return core.AutomaticStrong
	case positive:
		return core.AutomaticPlausible
	case opposed:
		return core.AutomaticConflicting
	default:
		return core.AutomaticUnknown
	}
}
