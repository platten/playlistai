package multichannel

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func TestRequiredJourneyReservesScarceSeparatorsAcrossPolicies(t *testing.T) {
	for _, mode := range []core.RecommendationMode{core.AcousticBrainzFirst, core.CLAPFirst, core.EnhancedHybrid} {
		for _, artists := range [][]string{{"A", "B", "B"}, {"A", "A", "B"}, {"A", "A", "B", "B"}} {
			t.Run(string(mode)+"/"+strings.Join(artists, ""), func(t *testing.T) {
				var rows []fakes.CatalogTrack
				var required []core.IntentReference
				separators := 0
				for i, artist := range artists {
					id := "required-" + itoa(i)
					rows = append(rows, fakes.CatalogTrack{ID: id, Display: artist + " - Required " + itoa(i), Audio: []float32{1, 0}, Track: []float32{1, 0}})
					required = append(required, core.IntentReference{Kind: core.ReferenceTrack, TrackID: id, Influence: core.InfluencePositive})
					if i > 0 && artists[i-1] == artist {
						separators++
					}
				}
				for i := range separators {
					rows = append(rows, fakes.CatalogTrack{ID: "separator-" + itoa(i), Display: "C - Separator " + itoa(i), Audio: []float32{1, 0}, Track: []float32{1, 0}})
				}
				cat := fakes.NewCatalog(2, rows...)
				intent := core.MusicIntent{Mode: core.ModeJourney, Count: len(artists) + separators, Seed: "42", RequiredTracks: required, Constraints: core.IntentConstraints{NoRepeatArtistBackToBack: true}}.Normalized()
				intent.Controls.RecommendationMode = mode
				engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
				first, err := engine.Build(context.Background(), intent)
				if err != nil || len(first.Tracks) != intent.Count || first.Outcome.State == core.OutcomeNeedsClarification {
					t.Fatalf("valid journey rejected: %+v, %v", first, err)
				}
				nextRequired := 0
				for i, track := range first.Tracks {
					if i > 0 && sameArtist(track, first.Tracks[i-1]) {
						t.Fatalf("hard spacing violated: %v", first.Tracks)
					}
					if nextRequired < len(required) && track.ID == required[nextRequired].TrackID {
						nextRequired++
					}
				}
				if nextRequired != len(required) {
					t.Fatal("required waypoint order changed")
				}
				second, err := engine.Build(context.Background(), intent)
				if err != nil || !reflect.DeepEqual(first.Tracks, second.Tracks) {
					t.Fatalf("seed replay changed: %v", err)
				}
				intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{Kind: "exclude_artist", Value: "C"})
				blocked, err := engine.Build(context.Background(), intent)
				if err != nil || blocked.Outcome.State != core.OutcomeNeedsClarification || len(blocked.Tracks) > 0 {
					t.Fatalf("excluded separator bypassed the conflict: tracks=%v outcome=%v error=%v", blocked.Tracks, blocked.Outcome, err)
				}
			})
		}
	}
}

func TestJourneyPreservesSeparatorNeededOnlyByLaterGap(t *testing.T) {
	cat := fakes.NewCatalog(1,
		fakes.CatalogTrack{ID: "a1", Display: "A - First", Audio: []float32{1}, Track: []float32{1}},
		fakes.CatalogTrack{ID: "a2", Display: "A - Last", Audio: []float32{1}, Track: []float32{1}},
		fakes.CatalogTrack{ID: "b1", Display: "B - First", Audio: []float32{1}, Track: []float32{1}},
		fakes.CatalogTrack{ID: "b2", Display: "B - Last", Audio: []float32{1}, Track: []float32{1}},
		fakes.CatalogTrack{ID: "c", Display: "C - Versatile", Audio: []float32{1}, Track: []float32{1}},
		fakes.CatalogTrack{ID: "b", Display: "B - Restricted", Audio: []float32{1}, Track: []float32{1}},
	)
	intent := core.MusicIntent{Mode: core.ModeJourney, Count: 6, Constraints: core.IntentConstraints{NoRepeatArtistBackToBack: true}}.Normalized()
	out, err := NewSequencer(cat, DefaultConfig()).Sequence(context.Background(), ports.SequenceRequest{Intent: intent, Required: refs(cat, "a1", "a2", "b1", "b2"), Candidates: []core.Candidate{sequencingCandidate(cat, "c", 1), sequencingCandidate(cat, "b", .5)}})
	if err != nil || trackIDs(out.Tracks) != "a1,b,a2,b1,c,b2" {
		t.Fatalf("scarce separator spent before later gap: %s %v", trackIDs(out.Tracks), err)
	}
}

func TestJourneyReallocatesSurplusAcrossRequiredGaps(t *testing.T) {
	cat := fakes.NewCatalog(1,
		fakes.CatalogTrack{ID: "a1", Display: "A - First", Audio: []float32{1}, Track: []float32{1}},
		fakes.CatalogTrack{ID: "b1", Display: "B - First", Audio: []float32{1}, Track: []float32{1}},
		fakes.CatalogTrack{ID: "b2", Display: "B - Last", Audio: []float32{1}, Track: []float32{1}},
		fakes.CatalogTrack{ID: "c", Display: "C - Surplus", Audio: []float32{1}, Track: []float32{1}},
		fakes.CatalogTrack{ID: "a2", Display: "A - Separator", Audio: []float32{1}, Track: []float32{1}},
	)
	for _, mode := range []core.RecommendationMode{core.AcousticBrainzFirst, core.CLAPFirst, core.EnhancedHybrid} {
		intent := core.MusicIntent{Mode: core.ModeJourney, Count: 5, Constraints: core.IntentConstraints{NoRepeatArtistBackToBack: true}}.Normalized()
		intent.Controls.RecommendationMode = mode
		request := ports.SequenceRequest{Intent: intent, Required: refs(cat, "a1", "b1", "b2"), Candidates: []core.Candidate{sequencingCandidate(cat, "c", 1), sequencingCandidate(cat, "a2", .5)}}
		seq := NewSequencer(cat, DefaultConfig())
		out, err := seq.Sequence(context.Background(), request)
		if err != nil || len(out.Tracks) != 5 || trackIDs(out.Tracks) != "a1,c,b1,a2,b2" {
			t.Fatalf("surplus caused a false partial: %s %v", trackIDs(out.Tracks), err)
		}
		for _, track := range request.Required {
			intent.RequiredTracks = append(intent.RequiredTracks, core.IntentReference{Kind: core.ReferenceTrack, TrackID: track.ID, Influence: core.InfluencePositive})
		}
		engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
		built, err := engine.Build(context.Background(), intent)
		if err != nil || len(built.Tracks) != 5 || built.Outcome.State == core.OutcomeNeedsClarification {
			t.Fatalf("orchestrator lost a feasible surplus placement: %+v %v", built, err)
		}
		_, _, err = seq.repairRequiredJourney(context.Background(), request, nil, 0)
		if !errors.Is(err, errJourneySearchExhausted) || errors.Is(err, core.ErrRequiredTrackConflict) {
			t.Fatalf("search limit mislabeled as impossible: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := seq.repairRequiredJourney(ctx, request, nil, journeySearchLimit); !errors.Is(err, context.Canceled) {
			t.Fatalf("repair ignored cancellation: %v", err)
		}
	}
}

func TestCategorySearchFailureIsDistinctFromRequiredConstraintConflict(t *testing.T) {
	cat := journeyCatalog()
	request := ports.SequenceRequest{
		Intent: journeyIntent(3), Required: refs(cat, "start", "end"),
		Candidates:     []core.Candidate{sequencingCandidate(cat, "first", 1)},
		CategoryStages: []map[string]bool{{"start": true, "first": true}, {}},
	}
	out, err := NewSequencer(cat, DefaultConfig()).Sequence(context.Background(), request)
	if !errors.Is(err, errJourneySearchExhausted) || errors.Is(err, core.ErrRequiredTrackConflict) || len(out.Tracks) != 0 {
		t.Fatalf("category search asserted a proven conflict: %+v %v", out, err)
	}
	request.Intent.Constraints.ArtistsExclude = []string{request.Required[0].Artist}
	err = newEligibility(request.Intent, nil, request.Required).validateRequired(request.Required, false)
	if !errors.Is(err, core.ErrRequiredTrackConflict) || errors.Is(err, errJourneySearchExhausted) {
		t.Fatalf("explicit required/excluded conflict became search exhaustion: %v", err)
	}
}

// Enumerate small artist patterns independently of the production heuristic.
// This catches a valid interleaving lost by either separator or surplus choices.
func TestJourneyRepairMatchesSmallExhaustiveSpacingOracle(t *testing.T) {
	for pattern := range 729 { // three required and three candidate artists
		value := pattern
		var required []core.TrackRef
		var candidates []core.Candidate
		var rows []fakes.CatalogTrack
		for i := range 6 {
			artist := string(rune('A' + value%3))
			value /= 3
			id := "track-" + itoa(i)
			track := core.TrackRef{ID: id, Artist: artist, Title: id}
			rows = append(rows, fakes.CatalogTrack{ID: id, Display: track.Display(), Audio: []float32{1}, Track: []float32{1}})
			if i < 3 {
				required = append(required, track)
			} else {
				candidates = append(candidates, core.Candidate{Track: track})
			}
		}
		var possible func(core.TrackRef, int, []core.Candidate) bool
		possible = func(previous core.TrackRef, next int, remaining []core.Candidate) bool {
			if next == len(required) {
				return len(remaining) == 0
			}
			if !sameArtist(previous, required[next]) && possible(required[next], next+1, remaining) {
				return true
			}
			for i, candidate := range remaining {
				if sameArtist(previous, candidate.Track) {
					continue
				}
				tail := append([]core.Candidate(nil), remaining[:i]...)
				tail = append(tail, remaining[i+1:]...)
				if possible(candidate.Track, next, tail) {
					return true
				}
			}
			return false
		}
		if !possible(required[0], 1, candidates) {
			continue
		}
		intent := core.MusicIntent{Mode: core.ModeJourney, Count: 6, Constraints: core.IntentConstraints{NoRepeatArtistBackToBack: true}}.Normalized()
		out, err := NewSequencer(fakes.NewCatalog(1, rows...), DefaultConfig()).Sequence(context.Background(), ports.SequenceRequest{Intent: intent, Required: required, Candidates: candidates})
		if err != nil || len(out.Tracks) != 6 {
			t.Fatalf("feasible artist pattern %d rejected: %v %v", pattern, out.Tracks, err)
		}
		next := 0
		for i, track := range out.Tracks {
			if i > 0 && sameArtist(out.Tracks[i-1], track) {
				t.Fatalf("pattern %d violates adjacency", pattern)
			}
			if next < len(required) && track.ID == required[next].ID {
				next++
			}
		}
		if next != len(required) || out.Tracks[0].ID != required[0].ID || out.Tracks[5].ID != required[2].ID {
			t.Fatalf("pattern %d changed required order/endpoints", pattern)
		}
	}
}

func dynamicEvidenceFixture(t *testing.T) (*catalog.DynamicCatalog, core.EnhancedAudioInput) {
	t.Helper()
	base, input := enhancedFixture(t)
	cat, err := catalog.OpenDynamic(base, base, filepath.Join(t.TempDir(), "dynamic.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	for _, id := range []string{"deezer:positive", "deezer:negative"} {
		meta := core.TrackMeta{Ref: core.TrackRef{ID: id, Artist: id, Title: "Dynamic"}, PreviewURL: "https://example.invalid/preview"}
		if err := cat.RegisterDynamicTrack(meta); err != nil {
			t.Fatal(err)
		}
		input.Representations[id] = core.AudioRepresentation{ID: "mert-" + id, TrackID: id, TrackKey: core.ProvisionalRecordingKey(meta.Ref), CatalogVersion: input.CatalogVersion, Model: input.Model, Pooled: []float32{1, 0}}
	}
	return cat, input
}

func TestDynamicMERTReferencesKeepPositiveNegativeAndFallbackRoles(t *testing.T) {
	cat, input := dynamicEvidenceFixture(t)
	intent := testIntent(1)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	intent.References = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "deezer:positive", Influence: core.InfluencePositive}}
	if !hasExplicitRetrievalReference(cat, intent) {
		t.Fatal("dynamic Enhanced reference lost priority over inferred discovery")
	}
	denseIntent := intent
	denseIntent.Controls.RecommendationMode = core.AcousticBrainzFirst
	if hasExplicitRetrievalReference(cat, denseIntent) {
		t.Fatal("dynamic MERT identity leaked into dense retrieval policy")
	}
	queries := mertSimilarityQueries(cat, intent)
	if len(queries) != 1 || queries[0].Track.ID != "deezer:positive" || queries[0].Weight != 1 {
		t.Fatalf("dynamic reference disappeared from MERT search: %+v", queries)
	}
	if len(positiveReferenceVectors(cat, intent)) != 0 {
		t.Fatal("dynamic identities acquired incompatible dense vectors")
	}
	snapshot := freezeEnhanced(t, input)
	for _, negative := range []bool{false, true} {
		if negative {
			intent.References = append(intent.References, core.IntentReference{Kind: core.ReferenceTrack, TrackID: "deezer:negative", Influence: core.InfluenceNegative})
		}
		ranked, err := NewRanker(cat, DefaultConfig()).Rank(context.Background(), []core.Candidate{sequencingCandidate(cat, "b", 0)}, ports.RankRequest{Intent: intent, EnhancedAudio: snapshot})
		want := 1.0
		if negative {
			want = 0
		}
		if err != nil || !ranked[0].Available.EnhancedMERT || ranked[0].Scores.EnhancedMERT != want {
			t.Fatalf("dynamic MERT ranking negative=%v: %+v %v", negative, ranked, err)
		}
	}
	intent.RequiredTracks, intent.References = intent.References[:1], nil
	if queries = mertSimilarityQueries(cat, intent); len(queries) != 1 || queries[0].Track.ID != "deezer:positive" {
		t.Fatalf("required dynamic fallback lost: %+v", queries)
	}
	intent.References, intent.RequiredTracks = intent.RequiredTracks, nil
	r := input.Representations["deezer:positive"]
	r.Model.Revision = "incompatible"
	input.Representations[r.TrackID] = r
	ranked, err := NewRanker(cat, DefaultConfig()).Rank(context.Background(), []core.Candidate{sequencingCandidate(cat, "b", 0)}, ports.RankRequest{Intent: intent, EnhancedAudio: freezeEnhanced(t, input)})
	if err != nil || ranked[0].Available.EnhancedMERT {
		t.Fatalf("incompatible dynamic representation used: %+v %v", ranked, err)
	}
}

func TestDynamicExposurePenaltyDoesNotRequireDenseVectors(t *testing.T) {
	cat, _ := dynamicEvidenceFixture(t)
	candidates := []core.Candidate{sequencingCandidate(cat, "deezer:positive", 1), sequencingCandidate(cat, "b", 1)}
	profile := core.TasteProfile{ExposureCount: 2, RecentExposures: map[string]float64{"deezer:positive": 1, "b": 1}}
	for _, candidate := range candidates {
		with, err := NewRanker(cat, DefaultConfig()).Rank(context.Background(), []core.Candidate{candidate}, ports.RankRequest{Intent: testIntent(1), Profile: profile})
		if err != nil {
			t.Fatal(err)
		}
		without, err := NewRanker(cat, DefaultConfig()).Rank(context.Background(), []core.Candidate{candidate}, ports.RankRequest{Intent: testIntent(1)})
		if err != nil || !with[0].Available.RecentExposure || with[0].Scores.RecentExposure != 1 || math.Abs(without[0].Scores.Total-with[0].Scores.Total-.3) > 1e-8 {
			t.Fatalf("exposure treatment differs for %s: with=%+v without=%+v error=%v", candidate.Track.ID, with, without, err)
		}
		if without[0].Available.RecentExposure {
			t.Fatal("absent exposure invented evidence")
		}
	}
}

func TestDynamicMERTReferenceSearchAcquisitionAndReplay(t *testing.T) {
	cat, input := dynamicEvidenceFixture(t)
	intent := testIntent(1)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	intent.References = []core.IntentReference{
		{Kind: core.ReferenceTrack, TrackID: "deezer:positive", Influence: core.InfluencePositive},
		{Kind: core.ReferenceTrack, TrackID: "deezer:negative", Influence: core.InfluenceNegative},
	}
	queriesCalled, acquisitions := 0, 0
	base, _ := enhancedFixture(t)
	engine := New(cat, fakes.NewSimilarityEngine(base), cat, DefaultConfig())
	engine.WithMERTSimilaritySearchProvider(func(_ context.Context, _ core.MusicIntent, _ core.TasteProfile, queries []core.MERTSimilarityQuery, _ map[string]struct{}, _ int) (*core.MERTSimilaritySearch, error) {
		queriesCalled++
		if len(queries) != 1 || queries[0].Track.ID != "deezer:positive" {
			t.Fatalf("explicit dynamic MERT queries = %+v", queries)
		}
		queries[0].RepresentationID = input.Representations[queries[0].Track.ID].ID
		hit := input.Representations["b"]
		hit.ID = "mert-b"
		return &core.MERTSimilaritySearch{Recorded: true, CatalogVersion: input.CatalogVersion, Model: input.Model, Queries: queries,
			Hits: []core.MERTSimilarityHit{{GroupID: queries[0].GroupID, QueryTrackID: queries[0].Track.ID, TrackID: "b", Rank: 1, Score: 1, QueryWeight: 1, Representation: hit}}}, nil
	})
	engine.WithEnhancedAudioProvider(func(_ context.Context, _ core.MusicIntent, _ core.TasteProfile, tracks []core.TrackRef) (*core.EnhancedAudioSnapshot, error) {
		acquisitions++
		seen := map[string]bool{}
		for _, track := range tracks {
			seen[track.ID] = true
		}
		if !seen["deezer:positive"] || !seen["deezer:negative"] {
			t.Fatalf("dynamic positive/negative acquisition omitted: %v", seen)
		}
		return core.NewEnhancedAudioSnapshot(input)
	})
	first, err := engine.Build(context.Background(), intent)
	if err != nil || trackIDs(first.Tracks) != "b" || first.EnhancedAudio == nil {
		t.Fatalf("dynamic MERT build tracks=%v outcome=%v error=%v", first.Tracks, first.Outcome, err)
	}
	second, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: first.Intent, EnhancedAudio: first.EnhancedAudio})
	if err != nil || queriesCalled != 1 || acquisitions != 1 || !reflect.DeepEqual(first.Tracks, second.Tracks) {
		t.Fatalf("replay reacquired or changed result: search=%d acquisition=%d tracks=%v error=%v", queriesCalled, acquisitions, second.Tracks, err)
	}
}
