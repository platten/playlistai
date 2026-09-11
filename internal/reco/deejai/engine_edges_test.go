package deejai

import (
	"context"
	"errors"
	"math/rand"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func edgeCatalog() *fakes.Catalog {
	return fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "one", Display: "A - One", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "copy", Display: "A - One", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "two", Display: "B - Two", Audio: []float32{0, 1}, Track: []float32{0, 1}},
		fakes.CatalogTrack{ID: "three", Display: "C - Three", Audio: []float32{.7, .7}, Track: []float32{.7, .7}},
	)
}

func TestFallbackReferenceResolutionKeepsIdentityAndInfluence(t *testing.T) {
	cat := edgeCatalog()
	engine := New(cat, fakes.NewSimilarityEngine(cat), nil)
	got := engine.resolve(core.IntentSeeds{TrackIDs: []string{"one", "one", "copy", "missing"}, Queries: []string{"B - Two"}})
	if len(got) != 2 || got[0].ID != "one" || got[1].ID != "two" {
		t.Fatalf("legacy resolution lost identity deduplication: %+v", got)
	}
	refs := engine.journeyReferences(core.MusicIntent{References: []core.IntentReference{
		{Kind: core.ReferenceTrack, TrackID: "one", Influence: core.InfluencePositive},
		{Kind: core.ReferenceTrack, Query: "B - Two", Influence: core.InfluencePositive},
		{Kind: core.ReferenceTrack, TrackID: "three", Influence: core.InfluenceNegative},
	}})
	if !reflect.DeepEqual(refs, got) {
		t.Fatalf("journey fallback dropped a query or included a negative reference: %+v", refs)
	}
	if engine.AlgorithmVersion() != "deejai/v5" || OnlyAlgorithmVersion != AlgorithmVersion+"+engine-only/v2" {
		t.Fatal("baseline behavior change lacks a replay version")
	}
}

type edgeSimilarity struct {
	length  int
	failure error
}

func (s edgeSimilarity) Len() int { return s.length }
func (s edgeSimilarity) Search(context.Context, ports.SimilarityQuery) ([]ports.Match, error) {
	return nil, s.failure
}

func TestBaselineSearchErrorsAndEmptyIndexAreNotRecommendations(t *testing.T) {
	cat := edgeCatalog()
	failure := errors.New("synthetic exact-search error")
	for _, mode := range []core.Mode{core.ModeSimilar, core.ModeJourney} {
		intent := core.MusicIntent{Version: core.CurrentIntentVersion, Mode: mode, Seed: "1", Controls: core.IntentControls{TotalTrackCount: 4, AudioWeight: .5, CooccurrenceWeight: .5},
			RequiredTracks: []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "one"}, {Kind: core.ReferenceTrack, TrackID: "two"}},
		}
		if _, err := New(cat, edgeSimilarity{length: 4, failure: failure}).Build(context.Background(), intent); !errors.Is(err, failure) {
			t.Fatalf("%s hid similarity failure: %v", mode, err)
		}
		intent.Mode = core.ModeSimilar
		playlist, err := BuildOnly(context.Background(), New(cat, edgeSimilarity{}), intent)
		if err != nil || len(playlist.Tracks) != 2 || playlist.Outcome.State != core.OutcomePartial {
			t.Fatalf("empty index invented candidates: %+v %v", playlist, err)
		}
	}
	// Separator retrieval obeys the same failure path as ordinary picks.
	intent := core.MusicIntent{Count: 3, Lookback: 1, Constraints: core.IntentConstraints{NoRepeatArtistBackToBack: true}}
	meta, _ := cat.Meta("one")
	other := core.TrackRef{ID: "required-other", Artist: meta.Ref.Artist, Title: "Other"}
	engine := New(cat, edgeSimilarity{length: 4, failure: failure})
	f := newFilter(intent, nil, []core.TrackRef{meta.Ref, other})
	if _, err := engine.similar(context.Background(), nil, []core.TrackRef{meta.Ref, other}, [2]float32{.5, .5}, intent, rand.New(rand.NewSource(1)), f); !errors.Is(err, failure) {
		t.Fatalf("separator error hidden: %v", err)
	}
}

func TestEngineOnlyRejectsUnavailableAndUnsupportedContracts(t *testing.T) {
	cat := edgeCatalog()
	engine := New(cat, fakes.NewSimilarityEngine(cat))
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Seed: "1", References: []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "one", Influence: core.InfluencePositive}}, Controls: core.IntentControls{TotalTrackCount: 1}}
	if _, err := BuildOnly(context.Background(), nil, intent); err == nil {
		t.Fatal("missing engine accepted")
	}
	invalid := intent
	invalid.Seed = "not-a-seed"
	if _, err := BuildOnly(context.Background(), engine, invalid); err == nil {
		t.Fatal("invalid seed accepted")
	}
	for _, change := range []func(*core.MusicIntent){
		func(i *core.MusicIntent) {
			i.Temporal = []core.TemporalRequirement{{Basis: "original_release", StartYear: 1990, EndYear: 2000}}
		},
		func(i *core.MusicIntent) {
			i.Destination = &core.IntentReference{Kind: core.ReferenceTrack, TrackID: "two"}
		},
	} {
		unsupported := intent
		change(&unsupported)
		got, err := BuildOnly(context.Background(), engine, unsupported)
		if err != nil || len(got.Tracks) != 0 || got.Outcome.State != core.OutcomeUnsupported {
			t.Fatalf("unsupported contract became fulfilled: %+v %v", got, err)
		}
	}
	intent.References = append(intent.References, core.IntentReference{Kind: core.ReferenceTrack, TrackID: "two", Influence: core.InfluenceNegative})
	got, err := BuildOnly(context.Background(), engine, intent)
	if err != nil || got.Outcome.State != core.OutcomePartial || len(got.Outcome.Reasons) == 0 || got.Outcome.Reasons[0].Code != "engine_only_negative_reference" {
		t.Fatalf("negative influence was falsely verified: %+v %v", got, err)
	}
}

func TestBaselineVectorAndFilterMissingDataBehavior(t *testing.T) {
	zero := []float32{0, 0}
	if got := l2normalized(zero); !reflect.DeepEqual(got, zero) {
		t.Fatalf("zero normalization invented vector evidence: %+v", got)
	}
	dst := []float32{1, 2}
	addNormalizedWeighted(dst, zero, 10)
	if !reflect.DeepEqual(dst, []float32{1, 2}) || containsID(nil, "missing") {
		t.Fatal("missing evidence changed vector/context")
	}
	cat := edgeCatalog()
	f := newFilter(core.MusicIntent{}, nil, nil)
	track, _, ok, err := f.pick(context.Background(), cat, []ports.Match{{ID: "missing"}, {ID: "one"}}, "", "")
	if err != nil || !ok || track.ID != "one" {
		t.Fatalf("missing catalog metadata was emitted: %+v %v", track, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := f.pick(ctx, cat, []ports.Match{{ID: "one"}}, "", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("filter cancellation ignored: %v", err)
	}
	if _, _, _, err := f.pick(ctx, cat, nil, "", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("empty filter cancellation ignored: %v", err)
	}
}
