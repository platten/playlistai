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
		if !ok || !featuresSatisfy(features, active) {
			continue
		}
		result = append(result, candidate)
	}
	return result, len(active), nil
}

func validateRequiredSemanticConstraints(ctx context.Context, store ports.FeatureStore, required []core.TrackRef, constraints []core.HardConstraint) error {
	active := activeSemanticConstraints(store.Info(), constraints)
	for _, track := range required {
		features, ok, err := store.Features(ctx, track.ID)
		if err != nil {
			return err
		}
		if len(active) > 0 && (!ok || !featuresSatisfy(features, active)) {
			return fmt.Errorf("%w: %q lacks affirmative evidence for a semantic hard constraint", core.ErrRequiredTrackConflict, track.Display())
		}
	}
	return nil
}

func activeSemanticConstraints(info core.FeatureStoreInfo, constraints []core.HardConstraint) []core.HardConstraint {
	facets := map[string]bool{}
	for _, facet := range info.SupportedFacets {
		facets[facet] = true
	}
	var result []core.HardConstraint
	for _, constraint := range constraints {
		switch constraint.Kind {
		case "exclude_style", "require_style":
			if facets["styles"] || facets["tags"] {
				result = append(result, constraint)
			}
		case "exclude_vocals", "require_instrumental", "require_vocals":
			if facets["vocal_evidence"] {
				result = append(result, constraint)
			}
		}
	}
	return result
}

func featuresSatisfy(features core.TrackFeatures, constraints []core.HardConstraint) bool {
	for _, constraint := range constraints {
		switch constraint.Kind {
		case "exclude_style":
			if !knownFacet(features.Styles, features.Tags) || containsFacet(constraint.Value, features.Styles, features.Tags) {
				return false
			}
		case "require_style":
			if !containsFacet(constraint.Value, features.Styles, features.Tags) {
				return false
			}
		case "exclude_vocals", "require_instrumental":
			if features.VocalEvidence.Missingness != core.FeatureKnown || !strings.EqualFold(features.VocalEvidence.Value, "instrumental") {
				return false
			}
		case "require_vocals":
			if features.VocalEvidence.Missingness != core.FeatureKnown || strings.EqualFold(features.VocalEvidence.Value, "instrumental") {
				return false
			}
		}
	}
	return true
}

func knownFacet(groups ...[]core.FeatureValue) bool {
	for _, group := range groups {
		for _, value := range group {
			if value.Missingness == core.FeatureKnown {
				return true
			}
		}
	}
	return false
}

func containsFacet(want string, groups ...[]core.FeatureValue) bool {
	want = core.NormalizeIdentityPart(want)
	for _, group := range groups {
		for _, value := range group {
			if value.Missingness == core.FeatureKnown && core.NormalizeIdentityPart(value.Value) == want {
				return true
			}
		}
	}
	return false
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
		status, detail = "limited", "grounded sidecar text retrieval and transparent soft ranking are active for indexed tracks"
	}
	for index := range intent.Capabilities {
		if intent.Capabilities[index].Name == "semantic_preferences" {
			intent.Capabilities[index].Status, intent.Capabilities[index].Detail = status, detail
		}
	}
	constraintStatus := core.CapabilityStatus{Name: "semantic_hard_constraints", Status: "unsupported", Detail: "requires declared grounded facet coverage"}
	if enforced {
		constraintStatus.Status, constraintStatus.Detail = "limited", "declared style/vocal facets enforced; unknown evidence is ineligible"
	}
	intent.Capabilities = append(intent.Capabilities, constraintStatus)
}
