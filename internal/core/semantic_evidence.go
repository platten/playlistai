package core

import (
	"math"
	"sort"
	"strings"
)

// ReliableFeature defines the common runtime/evaluation evidence threshold.
// Confidence describes supplied evidence, not a calibrated genre probability.
func ReliableFeature(value FeatureValue) bool {
	return value.Missingness == FeatureKnown && !math.IsNaN(value.Confidence) && value.Confidence >= .6 && value.Confidence <= 1 && len(value.Provenance) > 0
}

func FacetComplete(features TrackFeatures, facet string) bool {
	for _, covered := range features.FacetCoverage {
		if covered == facet || covered == "all" || (covered == "styles_and_tags" && (facet == "styles" || facet == "tags")) {
			return true
		}
	}
	return false
}

func CanonicalStyle(value string) string {
	value = NormalizeIdentityPart(value)
	switch value {
	case "ambient electronica":
		return "ambient electronic"
	case "rock and roll", "rock n roll", "rock roll":
		return "rock & roll"
	case "electronica":
		return "electronic"
	default:
		return value
	}
}

var styleParents = map[string]string{
	"ambient electronic": "electronic", "techno": "electronic", "house": "electronic",
	"electro": "electronic", "idm": "electronic", "synthpop": "electronic",
	"drum and bass": "electronic", "downtempo": "electronic", "trance": "electronic",
	"rock & roll": "rock", "alternative rock": "rock", "indie rock": "rock", "hard rock": "rock", "dance-rock": "rock",
}

// StyleMatches is directional: techno is electronic, but electronic does not
// prove techno; hybrid genres only inherit explicitly reviewed relationships.
func StyleMatches(want, actual string) bool {
	want, actual = CanonicalStyle(want), CanonicalStyle(actual)
	for current := actual; current != ""; current = styleParents[current] {
		if current == want {
			return true
		}
	}
	return false
}

func StyleEvidence(features TrackFeatures, want string) EvidenceState {
	return facetEvidence(func(value string) bool { return StyleMatches(want, value) },
		FacetComplete(features, "styles") || FacetComplete(features, "tags"), features.Styles, features.Tags)
}

func facetEvidence(matches func(string) bool, complete bool, groups ...[]FeatureValue) EvidenceState {
	uncertain := false
	for _, group := range groups {
		for _, value := range group {
			if !matches(value.Value) {
				continue
			}
			if ReliableFeature(value) {
				return EvidenceMatch
			}
			// A comprehensive review containing uncertain evidence of this facet
			// cannot establish its absence. Do not discard uncertainty as false.
			uncertain = true
		}
	}
	if complete && !uncertain {
		return EvidenceMismatch
	}
	return EvidenceUnknown
}

func VocalEvidence(features TrackFeatures, want string) EvidenceState {
	if !ReliableFeature(features.VocalEvidence) {
		return EvidenceUnknown
	}
	actual := strings.ToLower(features.VocalEvidence.Value)
	want = strings.ToLower(want)
	if actual != "vocal" && actual != "mixed" && actual != "instrumental" {
		return EvidenceUnknown
	}
	if actual == want || want == "vocal" && actual == "mixed" {
		return EvidenceMatch
	}
	return EvidenceMismatch
}

func CriterionEvidence(features TrackFeatures, criterion MusicalCriterion) EvidenceState {
	matches := func(value string) bool { return NormalizeIdentityPart(value) == NormalizeIdentityPart(criterion.Value) }
	switch criterion.Kind {
	case "style":
		return StyleEvidence(features, criterion.Value)
	case "mood":
		return facetEvidence(matches, FacetComplete(features, "moods"), features.Moods, features.Tags)
	case "instrumentation":
		return facetEvidence(matches, FacetComplete(features, "instrumentation"), features.Instrumentation, features.Tags)
	case "vocal":
		return VocalEvidence(features, criterion.Value)
	default:
		return EvidenceUnsupported
	}
}

func SemanticConstraintSatisfied(features TrackFeatures, constraint HardConstraint) bool {
	switch constraint.Kind {
	case "exclude_style":
		return StyleEvidence(features, constraint.Value) == EvidenceMismatch
	case "require_style":
		return StyleEvidence(features, constraint.Value) == EvidenceMatch
	case "exclude_vocals":
		return VocalEvidence(features, "vocal") == EvidenceMismatch
	case "require_instrumental":
		return VocalEvidence(features, "instrumental") == EvidenceMatch
	case "require_vocals":
		return VocalEvidence(features, "vocal") == EvidenceMatch
	default:
		return false
	}
}

// JourneyCriteria orders stages by meaning, independent of JSON field order.
// Via criteria retain their specified relative order.
func JourneyCriteria(criteria []MusicalCriterion) []MusicalCriterion {
	order := map[string]int{"journey_start": 0, "journey_via": 1, "journey_end": 2}
	var result []MusicalCriterion
	for _, criterion := range criteria {
		if _, ok := order[criterion.Scope]; ok {
			result = append(result, criterion)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return order[result[i].Scope] < order[result[j].Scope] })
	return result
}

// JourneySequenceViolations counts the minimum mismatching tracks and omitted
// stages in an ordered journey. Each stage needs at least one distinct track;
// a hybrid may occupy any matching stage, but cannot make time run backwards.
// states is track-major, with columns in JourneyCriteria order.
func JourneySequenceViolations(states [][]EvidenceState, stageCount int) int {
	if stageCount == 0 {
		return 0
	}
	if len(states) == 0 {
		return stageCount
	}
	dp := make([]int, stageCount)
	for stage := range dp {
		dp[stage] = stage // missing initial stages
	}
	for track, row := range states {
		next := make([]int, stageCount)
		bestAdvance := len(states) + stageCount
		for stage := range next {
			next[stage] = dp[stage]
			if track > 0 {
				if stage > 0 {
					bestAdvance = min(bestAdvance, dp[stage-1]-(stage-1))
				}
				next[stage] = min(next[stage], bestAdvance+stage-1)
			}
			if stage >= len(row) || row[stage] != EvidenceMatch {
				next[stage]++
			}
		}
		dp = next
	}
	best := len(states) + stageCount
	for stage, cost := range dp {
		best = min(best, cost+stageCount-stage-1)
	}
	return best
}
