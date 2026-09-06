package multichannel

import (
	"context"
	"fmt"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const minimumFacetConfidence = 0.6

func filterSemanticConstraints(ctx context.Context, store ports.FeatureStore, candidates []core.Candidate, constraints []core.HardConstraint) ([]core.Candidate, int, error) {
	active := activeSemanticConstraints(store.Info(), constraints)
	if len(active) == 0 {
		return candidates, 0, nil
	}
	result := make([]core.Candidate, 0, len(candidates))
	for index, candidate := range candidates {
		if index&127 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
		}
		features, ok, err := store.Features(ctx, candidate.Track.ID)
		if err != nil {
			return nil, 0, err
		}
		if !ok || !constraintsSatisfied(features, active) {
			continue
		}
		result = append(result, candidate)
	}
	return result, len(active), nil
}

func validateRequiredSemanticConstraints(ctx context.Context, store ports.FeatureStore, required []core.TrackRef, constraints []core.HardConstraint, criteria []core.MusicalCriterion) error {
	active := activeSemanticConstraints(store.Info(), constraints)
	for _, track := range required {
		features, ok, err := store.Features(ctx, track.ID)
		if err != nil {
			return err
		}
		if len(active) > 0 && (!ok || !constraintsSatisfied(features, active)) {
			return fmt.Errorf("%w: %q lacks affirmative evidence for a semantic hard constraint", core.ErrRequiredTrackConflict, track.Display())
		}
		if ok {
			for _, criterion := range criteria {
				if criterion.Scope == "playlist" && criterionState(features, criterion) != core.EvidenceMatch {
					return fmt.Errorf("%w: %q does not have affirmative evidence for essential %s %q", core.ErrRequiredTrackConflict, track.Display(), criterion.Kind, criterion.Value)
				}
			}
		} else if len(criteria) > 0 {
			return fmt.Errorf("%w: %q has no evidence for an essential musical criterion", core.ErrRequiredTrackConflict, track.Display())
		}
	}
	return nil
}

func activeSemanticConstraints(info core.FeatureStoreInfo, constraints []core.HardConstraint) []core.HardConstraint {
	var result []core.HardConstraint
	for _, constraint := range constraints {
		switch constraint.Kind {
		case "exclude_style", "require_style":
			if supportsAnyFacet(info, "styles", "tags") {
				result = append(result, constraint)
			}
		case "exclude_vocals", "require_instrumental", "require_vocals":
			if supportsAnyFacet(info, "vocal_evidence") {
				result = append(result, constraint)
			}
		}
	}
	return result
}

func criterionSupported(info core.FeatureStoreInfo, criterion core.MusicalCriterion) bool {
	switch criterion.Kind {
	case "style":
		return supportsAnyFacet(info, "styles", "tags")
	case "mood":
		return supportsAnyFacet(info, "moods", "tags")
	case "instrumentation":
		return supportsAnyFacet(info, "instrumentation", "tags")
	case "vocal":
		return supportsAnyFacet(info, "vocal_evidence")
	default:
		return false
	}
}

func supportsAnyFacet(info core.FeatureStoreInfo, wanted ...string) bool {
	for _, facet := range info.SupportedFacets {
		for _, want := range wanted {
			if facet == want {
				return true
			}
		}
	}
	return false
}

func constraintsSatisfied(features core.TrackFeatures, constraints []core.HardConstraint) bool {
	for _, constraint := range constraints {
		state := constraintState(features, constraint)
		// Strict constraints require affirmative evidence. For an exclusion,
		// only a measured mismatch (from a complete facet) establishes absence.
		switch constraint.Kind {
		case "exclude_style", "exclude_vocals":
			if state != core.EvidenceMismatch {
				return false
			}
		default:
			if state != core.EvidenceMatch {
				return false
			}
		}
	}
	return true
}

func constraintState(features core.TrackFeatures, constraint core.HardConstraint) core.EvidenceState {
	switch constraint.Kind {
	case "exclude_style", "require_style":
		return styleState(features, constraint.Value)
	case "exclude_vocals":
		return vocalState(features, "vocal")
	case "require_instrumental":
		return vocalState(features, "instrumental")
	case "require_vocals":
		return vocalState(features, "vocal")
	default:
		return core.EvidenceUnsupported
	}
}

func criterionState(features core.TrackFeatures, criterion core.MusicalCriterion) core.EvidenceState {
	switch criterion.Kind {
	case "style":
		return styleState(features, criterion.Value)
	case "mood":
		return valueState(criterion.Value, facetComplete(features, "moods"), features.Moods, features.Tags)
	case "instrumentation":
		return valueState(criterion.Value, facetComplete(features, "instrumentation"), features.Instrumentation, features.Tags)
	case "vocal":
		return vocalState(features, criterion.Value)
	default:
		return core.EvidenceUnsupported
	}
}

func criterionValues(features core.TrackFeatures, criterion core.MusicalCriterion) []core.FeatureValue {
	switch criterion.Kind {
	case "style":
		return append(append([]core.FeatureValue(nil), features.Styles...), features.Tags...)
	case "mood":
		return append(append([]core.FeatureValue(nil), features.Moods...), features.Tags...)
	case "instrumentation":
		return append(append([]core.FeatureValue(nil), features.Instrumentation...), features.Tags...)
	case "vocal":
		return []core.FeatureValue{features.VocalEvidence}
	default:
		return nil
	}
}

func criterionConfidence(features core.TrackFeatures, criterion core.MusicalCriterion) float64 {
	best := 0.0
	for _, value := range criterionValues(features, criterion) {
		if reliableKnown(value) && criterionValueMatches(criterion, value) {
			best = maxFloat(best, value.Confidence)
		}
	}
	return best
}

func criterionProvenance(features core.TrackFeatures, criterion core.MusicalCriterion) []core.FeatureProvenance {
	var result []core.FeatureProvenance
	for _, value := range criterionValues(features, criterion) {
		if reliableKnown(value) && criterionValueMatches(criterion, value) {
			result = appendUniqueProvenance(result, value.Provenance...)
		}
	}
	return result
}

func criterionValueMatches(criterion core.MusicalCriterion, value core.FeatureValue) bool {
	switch criterion.Kind {
	case "style":
		return styleMatches(canonicalStyle(criterion.Value), canonicalStyle(value.Value))
	case "vocal":
		actual, want := strings.ToLower(value.Value), strings.ToLower(criterion.Value)
		return actual == want || want == "vocal" && actual == "mixed"
	default:
		return core.NormalizeIdentityPart(value.Value) == core.NormalizeIdentityPart(criterion.Value)
	}
}

func styleState(features core.TrackFeatures, want string) core.EvidenceState {
	want = canonicalStyle(want)
	for _, group := range [][]core.FeatureValue{features.Styles, features.Tags} {
		for _, value := range group {
			if reliableKnown(value) && styleMatches(want, canonicalStyle(value.Value)) {
				return core.EvidenceMatch
			}
		}
	}
	if facetComplete(features, "styles") || facetComplete(features, "tags") {
		return core.EvidenceMismatch
	}
	return core.EvidenceUnknown
}

func valueState(want string, complete bool, groups ...[]core.FeatureValue) core.EvidenceState {
	want = core.NormalizeIdentityPart(want)
	for _, group := range groups {
		for _, value := range group {
			if reliableKnown(value) && core.NormalizeIdentityPart(value.Value) == want {
				return core.EvidenceMatch
			}
		}
	}
	if complete {
		return core.EvidenceMismatch
	}
	return core.EvidenceUnknown
}

func vocalState(features core.TrackFeatures, want string) core.EvidenceState {
	if !reliableKnown(features.VocalEvidence) {
		return core.EvidenceUnknown
	}
	actual := strings.ToLower(features.VocalEvidence.Value)
	want = strings.ToLower(want)
	match := actual == want || (want == "vocal" && (actual == "vocal" || actual == "mixed"))
	if match {
		return core.EvidenceMatch
	}
	return core.EvidenceMismatch
}

func reliableKnown(value core.FeatureValue) bool {
	return value.Missingness == core.FeatureKnown && value.Confidence >= minimumFacetConfidence && len(value.Provenance) > 0
}

func facetComplete(features core.TrackFeatures, facet string) bool {
	for _, covered := range features.FacetCoverage {
		if covered == facet || covered == "all" || (covered == "styles_and_tags" && (facet == "styles" || facet == "tags")) {
			return true
		}
	}
	return false
}

func canonicalStyle(value string) string {
	value = core.NormalizeIdentityPart(value)
	switch value {
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

func styleMatches(want, actual string) bool {
	if want == actual {
		return true
	}
	for current := actual; current != ""; current = styleParents[current] {
		if current == want {
			return true
		}
	}
	return false
}

func criterionKey(criterion core.MusicalCriterion) string {
	return criterion.Scope + ":" + criterion.Kind + ":" + criterion.Value
}

func hasSemanticCandidates(candidates []core.Candidate) bool {
	for _, candidate := range candidates {
		if candidate.Available.SemanticMatch {
			return true
		}
	}
	return false
}

func markSemanticConstraintsEnforced(intent *core.MusicIntent, info core.FeatureStoreInfo) {
	active := activeSemanticConstraints(info, intent.HardConstraints)
	for index := range intent.HardConstraints {
		for _, constraint := range active {
			if intent.HardConstraints[index].Kind == constraint.Kind && intent.HardConstraints[index].Value == constraint.Value {
				intent.HardConstraints[index].RuntimeEnforced = true
			}
		}
	}
}

func setSemanticCapability(intent *core.MusicIntent, matched, enforced bool) {
	status, detail := "unsupported", "preserved; no compatible grounded semantic matches were available"
	if matched {
		status, detail = "limited", "grounded sidecar text scoring is active for indexed tracks"
	}
	for index := range intent.Capabilities {
		if intent.Capabilities[index].Name == "semantic_preferences" {
			intent.Capabilities[index].Status, intent.Capabilities[index].Detail = status, detail
		}
	}
	constraintStatus := core.CapabilityStatus{Name: "semantic_hard_constraints", Status: "unsupported", Detail: "requires declared grounded facet coverage"}
	if enforced {
		constraintStatus.Status, constraintStatus.Detail = "limited", "declared complete style/vocal facets enforced; unknown evidence is ineligible"
	}
	intent.Capabilities = append(intent.Capabilities, constraintStatus)
}
