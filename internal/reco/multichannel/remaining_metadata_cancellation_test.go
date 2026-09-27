package multichannel

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type remainingMetadataCatalog struct {
	*fakes.Catalog
	blockAt     int32
	calls       atomic.Int32
	legacyCalls atomic.Int32
	entered     chan struct{}
}

func (c *remainingMetadataCatalog) Meta(id string) (core.TrackMeta, bool) {
	c.legacyCalls.Add(1)
	return c.Catalog.Meta(id)
}

func (c *remainingMetadataCatalog) MetaContext(ctx context.Context, id string) (core.TrackMeta, bool) {
	if c.calls.Add(1) == c.blockAt {
		close(c.entered)
		<-ctx.Done()
		return core.TrackMeta{}, false
	}
	return c.Catalog.Meta(id)
}

func remainingMetadataFixture() *remainingMetadataCatalog {
	return &remainingMetadataCatalog{Catalog: fakes.NewCatalog(1,
		fakes.CatalogTrack{ID: "one", Display: "Artist - Song", Audio: []float32{1}, Track: []float32{1}},
		fakes.CatalogTrack{ID: "two", Display: "Second - Other", Audio: []float32{1}, Track: []float32{1}}), entered: make(chan struct{})}
}

func TestRemainingMetadataPreparationPropagatesCancellation(t *testing.T) {
	for _, stage := range []string{"album diversity", "named artist fallback", "candidate identity", "recommendation pool", "artist-only fallback", "inferred anchor", "recording identity", "duration fit", "criterion annotations", "verifier identity", "seed admission"} {
		t.Run(stage, func(t *testing.T) {
			cat := remainingMetadataFixture()
			cat.blockAt = 1
			if stage == "album diversity" {
				cat.blockAt = 2
			}
			o := New(cat, nil, nil, DefaultConfig())
			intent := core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeSimilar, Count: 1, Controls: core.IntentControls{TotalTrackCount: 1, RecommendationMode: core.DeejAIOnly}}.Normalized()
			meta, _ := cat.Catalog.Meta("one")
			candidate := core.Candidate{Track: meta.Ref, Scores: core.CandidateScores{Total: 1}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				switch stage {
				case "album diversity":
					_, err = o.selector.Select(ctx, []core.Candidate{candidate}, ports.SelectionRequest{Intent: intent, Count: 1})
				case "named artist fallback":
					intent.References = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "one", Influence: core.InfluencePositive}}
					_, err = o.selector.Select(ctx, []core.Candidate{candidate}, ports.SelectionRequest{Intent: intent, Count: 1})
				case "candidate identity":
					_, _, _, err = o.prepareIterativeCandidate(ctx, candidate, intent, newEligibility(intent, nil, nil), map[string]bool{})
				case "recommendation pool":
					_, err = o.prepareRecommendationPool(ctx, []core.Candidate{candidate}, ports.RetrievalRequest{Intent: intent, AttemptedIDs: map[string]struct{}{}}, newEligibility(intent, nil, nil), map[string]bool{}, 1)
				case "artist-only fallback":
					intent.HardConstraints = []core.HardConstraint{{Kind: "require_artist", Value: "Artist"}}
					intent.References = []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Artist", Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, Selected: &core.ResolutionCandidate{Artist: "Artist"}}}}
					_, err = artistOnlyCandidates(ctx, cat, intent)
				case "inferred anchor":
					intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "jazz"}}
					intent.InferredAnchors = []core.InferredAnchor{{Reference: core.IntentReference{Kind: core.ReferenceArtist, Query: "Artist", Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, Selected: &core.ResolutionCandidate{Artist: "Artist", Representatives: []core.WeightedTrack{{TrackID: "one", Weight: 1}}}}}}}
					_, _, err = o.assessInferredAnchors(ctx, intent)
				case "recording identity":
					o.enhanced = true
					o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{{Ref: meta.Ref, RecordingID: "11111111-1111-1111-1111-111111111111", IdentityStatus: core.ResolutionResolved}}}
					if _, found := o.knowledgeTrackContext(ctx, "one"); found {
						err = errors.New("canceled identity evidence was published")
					} else {
						err = ctx.Err()
					}
				case "duration fit":
					intent.DurationSeconds = 180
					_, err = o.fitDuration(ctx, intent, []core.Candidate{candidate}, []core.Candidate{candidate}, nil, 0)
				case "criterion annotations":
					o.assessClause(ctx, "one", core.AudioClause{Kind: "genre", Text: "jazz"}, core.AudioAssessment{})
					err = ctx.Err()
				case "seed admission":
					o.enhanced = true
					_, err = o.rankGroundedSeeds(ctx, []core.Candidate{candidate}, intent, nil)
				case "verifier identity":
					o.enhanced = true
					o.recordingVerifier = recordingVerifierFunc(func(context.Context, core.EnrichedTrack, []core.MusicalCriterion) (core.EnrichedTrack, error) {
						return core.EnrichedTrack{}, errors.New("unexpected verifier call")
					})
					intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "jazz"}}
					err = o.verifyRecording(ctx, meta.Ref, intent)
				}
				done <- err
			}()
			select {
			case <-cat.entered:
			case err := <-done:
				t.Fatalf("preparation bypassed contextual metadata: %v (legacy=%d)", err, cat.legacyCalls.Load())
			case <-time.After(time.Second):
				t.Fatal("metadata boundary was not reached")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation became a semantic result: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("preparation ignored cancellation")
			}
			if got := cat.legacyCalls.Load(); got != 0 {
				t.Fatalf("used %d background metadata reads", got)
			}
		})
	}
}

func TestSelectionContextMetadataPreservesActiveResults(t *testing.T) {
	for _, mode := range []core.RecommendationMode{core.EnhancedHybrid, core.AcousticBrainzFirst, core.CLAPFirst, core.DeejAIOnly} {
		t.Run(string(mode), func(t *testing.T) {
			cat := remainingMetadataFixture()
			intent := core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeSimilar, Count: 2, Controls: core.IntentControls{TotalTrackCount: 2, RecommendationMode: mode}}.Normalized()
			var candidates []core.Candidate
			for _, id := range []string{"one", "two"} {
				meta, _ := cat.Catalog.Meta(id)
				candidates = append(candidates, core.Candidate{Track: meta.Ref, Scores: core.CandidateScores{Total: 1}})
			}
			request := ports.SelectionRequest{Intent: intent, Count: 2}
			want, err := NewSelector(cat.Catalog, DefaultConfig()).Select(context.Background(), candidates, request)
			if err != nil {
				t.Fatal(err)
			}
			got, err := NewSelector(cat, DefaultConfig()).Select(context.Background(), candidates, request)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("selection changed: got=%+v want=%+v error=%v", got, want, err)
			}
			sequence := ports.SequenceRequest{Intent: intent, Candidates: candidates, Seed: 1}
			wantSequence, err := NewSequencer(cat.Catalog, DefaultConfig()).Sequence(context.Background(), sequence)
			if err != nil {
				t.Fatal(err)
			}
			gotSequence, err := NewSequencer(cat, DefaultConfig()).Sequence(context.Background(), sequence)
			if err != nil || !reflect.DeepEqual(gotSequence, wantSequence) {
				t.Fatalf("sequence changed: got=%+v want=%+v error=%v", gotSequence, wantSequence, err)
			}
			if got := cat.legacyCalls.Load(); got != 0 {
				t.Fatalf("used %d background metadata reads", got)
			}
		})
	}
}
