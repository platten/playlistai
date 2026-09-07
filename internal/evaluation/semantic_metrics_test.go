package evaluation

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

type metricFeatures map[string]core.TrackFeatures

func (m metricFeatures) Info() core.FeatureStoreInfo {
	return core.FeatureStoreInfo{SupportedFacets: []string{"styles"}}
}
func (m metricFeatures) Features(_ context.Context, id string) (core.TrackFeatures, bool, error) {
	f, ok := m[id]
	return f, ok, nil
}

func metricStyle(style string) core.TrackFeatures {
	return core.TrackFeatures{Styles: []core.FeatureValue{{Value: style, Missingness: core.FeatureKnown, Confidence: .9, Provenance: []core.FeatureProvenance{{Source: "review"}}}}, FacetCoverage: []string{"styles"}}
}

func TestSemanticMetricsUseRuntimeHierarchyAndJourneyScopes(t *testing.T) {
	features := metricFeatures{"e": metricStyle("techno"), "r": metricStyle("rock and roll")}
	playlist := core.Playlist{Tracks: []core.TrackRef{{ID: "e"}, {ID: "r"}}, Intent: core.MusicIntent{EssentialCriteria: []core.MusicalCriterion{
		{Kind: "style", Value: "rock", Scope: "journey_end"}, {Kind: "style", Value: "electronic", Scope: "journey_start"},
	}}}
	if got := EssentialCriterionViolations(context.Background(), playlist, features); got != 0 {
		t.Fatalf("valid forward subgenre journey miscounted: %d", got)
	}
	playlist.Tracks[0], playlist.Tracks[1] = playlist.Tracks[1], playlist.Tracks[0]
	if got := EssentialCriterionViolations(context.Background(), playlist, features); got == 0 {
		t.Fatal("reverse journey counted as compliant")
	}
	playlist.Tracks = []core.TrackRef{{ID: "e"}}
	playlist.Intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "electronic", Scope: "playlist"}}
	if got := EssentialCriterionViolations(context.Background(), playlist, features); got != 0 {
		t.Fatalf("techno was not recognized as electronic: %d", got)
	}
}

func TestSemanticMetricsCountUnknownUnreliableAndUnenforcedRequirements(t *testing.T) {
	playlist := core.Playlist{Tracks: []core.TrackRef{{ID: "e"}}, Intent: core.MusicIntent{HardConstraints: []core.HardConstraint{{Kind: "exclude_style", Value: "rock"}}}}
	feature := metricStyle("electronic")
	feature.FacetCoverage = nil
	features := metricFeatures{"e": feature}
	if got := HardConstraintViolations(context.Background(), playlist, features); got != 1 {
		t.Fatalf("incomplete tags counted as verified no-rock: %d", got)
	}
	if got := HardConstraintViolations(context.Background(), playlist, nil); got != 1 {
		t.Fatalf("missing store escaped strict evaluation: %d", got)
	}
	feature.FacetCoverage = []string{"styles"}
	rock := metricStyle("rock").Styles[0]
	rock.Confidence = .5
	feature.Styles = append(feature.Styles, rock)
	features["e"] = feature
	if got := HardConstraintViolations(context.Background(), playlist, features); got != 1 {
		t.Fatalf("uncertain rock counted as absent: %d", got)
	}
	playlist.Intent.HardConstraints[0] = core.HardConstraint{Kind: "require_style", Value: "rock", RuntimeEnforced: true}
	playlist.Intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "rock", Scope: "playlist"}}
	if HardConstraintViolations(context.Background(), playlist, features) != 1 || EssentialCriterionViolations(context.Background(), playlist, features) != 1 {
		t.Fatal("unreliable positive evidence escaped evaluation")
	}
	feature = metricStyle("rock")
	feature.Styles[0].Provenance = nil
	features["e"] = feature
	if EssentialCriterionViolations(context.Background(), playlist, features) != 1 {
		t.Fatal("unattributed evidence escaped evaluation")
	}
}
