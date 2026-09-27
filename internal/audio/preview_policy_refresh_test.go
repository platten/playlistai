package audio

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestFreshRefreshOmitsOldPreviewSearchButPreservesSavedSnapshot(t *testing.T) {
	for _, current := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "current"}[current], func(t *testing.T) {
			row := storedRepresentation()
			if !current {
				row.Identity.PolicyVersion, row.ID = "", ""
				row.ID = Fingerprint(row)
			}
			previous, err := core.NewEnhancedAudioSnapshot(core.EnhancedAudioInput{
				PreviewIdentityPolicy: core.PreviewIdentityPolicyVersion,
				CatalogVersion:        "catalog", Model: row.Model,
				MERTSearch: &core.MERTSimilaritySearch{Recorded: true, CatalogVersion: "catalog", Model: row.Model,
					Hits: []core.MERTSimilarityHit{{TrackID: row.TrackID, Representation: row}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(previous)
			got, err := PrepareEnhancedEvidence(context.Background(), &Service{}, nil, "catalog", row.Model, nil, false, previous)
			if err != nil || got == nil || (got.Input().MERTSearch != nil) != current {
				t.Fatalf("fresh refresh current=%v snapshot=%+v err=%v", current, got, err)
			}
			after, _ := json.Marshal(previous)
			if string(before) != string(after) || previous.Input().MERTSearch == nil {
				t.Fatal("saved historical evidence changed during refresh")
			}
		})
	}
}

func TestFreshRefreshDoesNotPromoteLegacyCentroids(t *testing.T) {
	for _, current := range []bool{false, true} {
		input := core.EnhancedAudioInput{CatalogVersion: "catalog", PositiveCentroid: []float32{1, 0}, NegativeCentroid: []float32{0, 1}}
		if current {
			input.PreviewIdentityPolicy = core.PreviewIdentityPolicyVersion
		}
		previous, err := core.NewEnhancedAudioSnapshot(input)
		if err != nil {
			t.Fatal(err)
		}
		before, _ := json.Marshal(previous)
		got, err := PrepareEnhancedEvidence(context.Background(), &Service{}, nil, "catalog", input.Model, nil, false, previous)
		if err != nil || got == nil {
			t.Fatalf("refresh: %v", err)
		}
		fresh := got.Input()
		if fresh.PreviewIdentityPolicy != core.PreviewIdentityPolicyVersion || (len(fresh.PositiveCentroid) > 0) != current || (len(fresh.NegativeCentroid) > 0) != current {
			t.Fatalf("centroid provenance current=%v: %+v", current, fresh)
		}
		after, _ := json.Marshal(previous)
		if string(before) != string(after) {
			t.Fatal("refresh changed frozen taste input")
		}
	}
}
