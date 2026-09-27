package multichannel

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func mertSequenceFixture(t *testing.T, packed bool) (libraryEvidenceFixture, core.EnhancedAudioInput, []core.TrackRef, Config) {
	t.Helper()
	base, input, a := searchRepresentationFixture(t)
	seed, _ := base.Meta("seed")
	b, _ := base.Meta("b")
	seedRep := input.Representations["seed"]
	seedRep.ID = "seed-observation"
	seedRep.Coverage = core.PreviewCoverage{Available: true, CoveredSeconds: 30}
	aRep := input.MERTSearch.Hits[0].Representation
	aRep.Coverage = seedRep.Coverage
	bRep := input.Representations["b"]
	bRep.ID, bRep.Pooled, bRep.Coverage = "b-observation", []float32{0, 1}, seedRep.Coverage
	input.Representations = map[string]core.AudioRepresentation{"seed": seedRep}
	input.MERTSearch.Hits[0].Representation = aRep
	input.MERTSearch.Hits = append(input.MERTSearch.Hits, core.MERTSimilarityHit{GroupID: "reference", QueryTrackID: "seed", TrackID: "b", Rank: 2, Score: 0, QueryWeight: 1, Representation: bRep})
	vectors := map[string]core.LibraryVector{}
	if packed {
		vectors["seed"] = core.LibraryVector{Source: core.LibraryEvidenceSource{SpaceID: "pack-space"}, Values: []float32{1, 0}}
		vectors["a"] = core.LibraryVector{Source: core.LibraryEvidenceSource{SpaceID: "pack-space"}, Values: []float32{1, 0}}
	}
	cfg := DefaultConfig()
	cfg.LibraryEvidenceEnabled, cfg.LocalImprovementPasses = packed, 0
	return libraryEvidenceFixture{Catalog: base, vectors: vectors}, input, []core.TrackRef{seed.Ref, a, b.Ref}, cfg
}

func TestMERTSearchSequencingLocationPreservesTransitionsAndOrder(t *testing.T) {
	for _, test := range []struct {
		name    string
		packed  bool
		allHits bool
	}{
		{name: "no packed"},
		{name: "competing packed", packed: true},
		{name: "all observations in search hits", allHits: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cat, input, tracks, cfg := mertSequenceFixture(t, test.packed)
			if test.allHits {
				input.MERTSearch.Hits = append(input.MERTSearch.Hits, core.MERTSimilarityHit{GroupID: "reference", QueryTrackID: "seed", TrackID: "seed", Rank: 3, Score: 1, QueryWeight: 1, Representation: input.Representations["seed"]})
				delete(input.Representations, "seed")
			}
			intent := enhancedIntent(3)
			intent.Controls.ArtistDiversity, intent.Controls.TransitionSmoothness = 0, 1
			evaluate := func() (float64, []string) {
				t.Helper()
				snapshot := freezeEnhanced(t, input)
				fingerprint := snapshot.Fingerprint()
				s := NewSequencer(cat, cfg)
				s.enhancedInput, s.libraryVectors = snapshot.Input(), cat.vectors
				score, known := s.trackSimilarity(tracks[0], tracks[1], intent)
				weight := enhancedWeight(cfg.EnhancedMERTWeight)
				if want := (1 - weight) / (1 + weight); !known || math.Abs(score-want) > 1e-9 {
					t.Fatalf("preview pair was omitted or lost arbitration: score=%g known=%t want=%g", score, known, want)
				}
				got, err := NewSequencer(cat, cfg).Sequence(context.Background(), ports.SequenceRequest{Intent: intent, Required: tracks[:1], Candidates: candidatesForTracks(tracks[1:]), EnhancedAudio: snapshot})
				if err != nil {
					t.Fatal(err)
				}
				var order []string
				for _, track := range got.Tracks {
					order = append(order, track.ID)
				}
				if !reflect.DeepEqual(order, []string{"seed", "b", "a"}) {
					t.Fatalf("saved preview evidence did not guide full sequence: %v", order)
				}
				if snapshot.Fingerprint() != fingerprint {
					t.Fatal("sequencing mutated frozen evidence")
				}
				return score, order
			}
			hitScore, hitOrder := evaluate()
			for _, hit := range input.MERTSearch.Hits {
				input.Representations[hit.TrackID] = hit.Representation
			}
			mapScore, mapOrder := evaluate()
			if hitScore != mapScore || !reflect.DeepEqual(hitOrder, mapOrder) {
				t.Fatal("same saved observations changed transition or full sequence across snapshot locations")
			}
		})
	}
}

func TestMERTSearchSequencingRejectsUnknownAndMismatchedHits(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*core.EnhancedAudioInput)
	}{
		{"missing hit", func(i *core.EnhancedAudioInput) { i.MERTSearch.Hits = nil }},
		{"unrecorded", func(i *core.EnhancedAudioInput) { i.MERTSearch.Recorded = false }},
		{"wrong recording", func(i *core.EnhancedAudioInput) { i.MERTSearch.Hits[0].Representation.TrackKey = "other-recording" }},
		{"wrong model", func(i *core.EnhancedAudioInput) { i.MERTSearch.Hits[0].Representation.Model.Revision = "other" }},
		{"wrong catalog", func(i *core.EnhancedAudioInput) { i.MERTSearch.CatalogVersion = "other" }},
		{"unlinked query", func(i *core.EnhancedAudioInput) { i.MERTSearch.Queries = nil }},
		{"missing vector", func(i *core.EnhancedAudioInput) { i.MERTSearch.Hits[0].Representation.Pooled = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cat, input, tracks, cfg := mertSequenceFixture(t, false)
			test.change(&input)
			s := NewSequencer(cat, cfg)
			s.enhancedInput = input
			if score, known := s.trackSimilarity(tracks[0], tracks[1], enhancedIntent(3)); !known || score != 1 {
				t.Fatalf("unverified hit enabled or scored preview evidence: %g %t", score, known)
			}
		})
	}
}

func TestMERTSearchSequencingKeepsFamilyDenominatorModesAndCancellation(t *testing.T) {
	cat, input, tracks, cfg := mertSequenceFixture(t, true)
	intent := enhancedIntent(3)
	// An enabled family keeps its denominator even when neither observation
	// can compare this pair; incompatible pack spaces must never be combined.
	input.MERTSearch.Hits[0].Representation.TrackKey = "other-recording"
	packed := cat.vectors["a"]
	packed.Source.SpaceID = "incompatible-space"
	cat.vectors["a"] = packed
	s := NewSequencer(cat, cfg)
	s.enhancedInput, s.libraryVectors = input, cat.vectors
	if score, known := s.trackSimilarity(tracks[0], tracks[1], intent); !known || math.Abs(score-1/(1+enhancedWeight(cfg.EnhancedMERTWeight))) > 1e-9 {
		t.Fatalf("missing family comparison changed denominator or mixed spaces: %g %t", score, known)
	}
	request := ports.SequenceRequest{Intent: intent, Required: tracks[:1], Candidates: candidatesForTracks(tracks[1:]), EnhancedAudio: freezeEnhanced(t, input)}
	for _, mode := range []core.RecommendationMode{core.AcousticBrainzFirst, core.CLAPFirst, core.DeejAIOnly} {
		request.Intent.Controls.RecommendationMode = mode
		without := request
		without.EnhancedAudio = nil
		left, leftErr := NewSequencer(cat, cfg).Sequence(context.Background(), request)
		right, rightErr := NewSequencer(cat, cfg).Sequence(context.Background(), without)
		if leftErr != nil || rightErr != nil || !reflect.DeepEqual(left, right) {
			t.Fatalf("%s sequencing changed: errors=%v/%v", mode, leftErr, rightErr)
		}
	}
	request.Intent.Controls.RecommendationMode = core.EnhancedHybrid
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewSequencer(cat, cfg).Sequence(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}
