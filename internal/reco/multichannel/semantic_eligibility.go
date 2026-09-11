package multichannel

import (
	"context"
	"fmt"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

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
	case "style", "genre":
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
		if !core.SemanticConstraintSatisfied(features, constraint) {
			return false
		}
	}
	return true
}

func criterionState(features core.TrackFeatures, criterion core.MusicalCriterion) core.EvidenceState {
	return core.CriterionEvidence(features, criterion)
}

func criterionValues(features core.TrackFeatures, criterion core.MusicalCriterion) []core.FeatureValue {
	switch criterion.Kind {
	case "style", "genre":
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
	case "style", "genre":
		return styleMatches(canonicalStyle(criterion.Value), canonicalStyle(value.Value))
	case "vocal":
		actual, want := strings.ToLower(value.Value), strings.ToLower(criterion.Value)
		return actual == want || want == "vocal" && actual == "mixed"
	default:
		return core.NormalizeIdentityPart(value.Value) == core.NormalizeIdentityPart(criterion.Value)
	}
}

func styleState(features core.TrackFeatures, want string) core.EvidenceState {
	return core.StyleEvidence(features, want)
}

func reliableKnown(value core.FeatureValue) bool {
	return core.ReliableFeature(value)
}

func canonicalStyle(value string) string {
	return core.CanonicalStyle(value)
}

func styleMatches(want, actual string) bool {
	return core.StyleMatches(want, actual)
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
