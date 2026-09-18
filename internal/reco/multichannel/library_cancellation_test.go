package multichannel

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type cancellableLibraryFixture struct {
	ports.Catalog
	mu     sync.Mutex
	reads  map[string]int
	onRead func(context.Context)
}

func (c *cancellableLibraryFixture) Vectors(string) (ports.Vectors, bool) {
	return ports.Vectors{}, false
}
func (c *cancellableLibraryFixture) LibraryVector(ctx context.Context, id string) (core.LibraryVector, bool, error) {
	c.mu.Lock()
	c.reads[id]++
	c.mu.Unlock()
	if c.onRead != nil {
		c.onRead(ctx)
	}
	values := []float32{1, 0}
	if id == "b" {
		values = []float32{0, 1}
	}
	return core.LibraryVector{Source: core.LibraryEvidenceSource{SpaceID: "fixture"}, Values: values}, true, ctx.Err()
}
func (c *cancellableLibraryFixture) LibraryDSPPreference(context.Context, string, core.MusicIntent) (float64, bool) {
	return 0, false
}

func TestLibrarySelectionAndSequencePropagateReadCancellation(t *testing.T) {
	for _, stage := range []string{"selection", "sequence"} {
		t.Run(stage, func(t *testing.T) {
			base, _ := enhancedFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cat := &cancellableLibraryFixture{Catalog: base, reads: map[string]int{}, onRead: func(readContext context.Context) {
				if readContext != ctx {
					t.Error("library read lost request context")
				}
				cancel()
			}}
			cfg := DefaultConfig()
			cfg.LibraryEvidenceEnabled = true
			intent := testIntent(2)
			intent.Controls.RecommendationMode = core.EnhancedHybrid
			candidates := candidatesForTracks(refs(base, "a", "b"))
			for i := range candidates {
				candidates[i].Scores.Total = 1
			}
			var err error
			if stage == "selection" {
				var got ports.SelectionResult
				got, err = NewSelector(cat, cfg).Select(ctx, candidates, ports.SelectionRequest{Intent: intent, Count: 2})
				if len(got.Candidates) != 0 {
					t.Fatal("canceled selection returned partial success")
				}
			} else {
				var got ports.SequenceResult
				got, err = NewSequencer(cat, cfg).Sequence(ctx, ports.SequenceRequest{Intent: intent, Candidates: candidates})
				if len(got.Tracks) != 0 {
					t.Fatal("canceled sequence returned partial success")
				}
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation swallowed: %v", err)
			}
			var reads int
			for _, n := range cat.reads {
				reads += n
			}
			if reads != 1 {
				t.Fatalf("continued reading after cancellation: %d", reads)
			}
		})
	}
}

func TestLibrarySequenceCachesOncePerTrackAndRequest(t *testing.T) {
	base, _ := enhancedFixture(t)
	cat := &cancellableLibraryFixture{Catalog: base, reads: map[string]int{}}
	cfg := DefaultConfig()
	cfg.LibraryEvidenceEnabled = true
	sequencer := NewSequencer(cat, cfg)
	intent := testIntent(3)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	intent.Controls.TransitionSmoothness = 1
	request := ports.SequenceRequest{Intent: intent, Candidates: candidatesForTracks(refs(base, "a", "b", "c")), ReferenceAnchors: refs(base, "seed"), RecentSelections: refs(base, "seed")}
	// Concurrent calls on one reusable sequencer must own independent caches.
	results := make(chan error, 2)
	for range 2 {
		go func() {
			got, err := sequencer.Sequence(context.Background(), request)
			if err == nil && len(got.Tracks) != 3 {
				err = errors.New("missing selected tracks")
			}
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b", "c", "seed"} {
		if cat.reads[id] != 2 {
			t.Fatalf("track %s read %d times, want once per request", id, cat.reads[id])
		}
	}
	if sequencer.libraryVectors != nil {
		t.Fatal("request cache leaked into reusable sequencer")
	}
}

func TestLibraryHydrationStaysWithinEnabledEnhancedMode(t *testing.T) {
	base, _ := enhancedFixture(t)
	cat := &cancellableLibraryFixture{Catalog: base, reads: map[string]int{}, onRead: func(context.Context) { t.Fatal("inactive library mode read audio evidence") }}
	for _, enabled := range []bool{false, true} {
		cfg := DefaultConfig()
		cfg.LibraryEvidenceEnabled = enabled
		intent := testIntent(1)
		intent.Controls.RecommendationMode = core.EnhancedHybrid
		if enabled {
			intent.Controls.RecommendationMode = core.DeejAIOnly
		}
		candidates := candidatesForTracks(refs(base, "a"))
		candidates[0].Scores.Total = 1
		if _, err := NewSelector(cat, cfg).Select(context.Background(), candidates, ports.SelectionRequest{Intent: intent, Count: 1, Required: refs(base, "seed")}); err != nil {
			t.Fatal(err)
		}
		if _, err := NewSequencer(cat, cfg).Sequence(context.Background(), ports.SequenceRequest{Intent: intent, Candidates: candidates, ReferenceAnchors: refs(base, "seed")}); err != nil {
			t.Fatal(err)
		}
	}
}
