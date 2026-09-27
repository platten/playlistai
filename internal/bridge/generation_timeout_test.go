package bridge

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type deadlineCheckingRecommendationEngine struct {
	hadDeadline bool
	remaining   time.Duration
}

type failAfterParsingEngine struct{}

type selectionDeadlineResolver struct{ ports.ReferenceResolver }

func (selectionDeadlineResolver) ResolveReferenceContext(ctx context.Context, _ core.IntentReference) core.ReferenceResolution {
	<-ctx.Done()
	return core.ReferenceResolution{Status: core.ResolutionUnresolved}
}

func TestSelectionWorkDeadlineReturnsHonestPartial(t *testing.T) {
	a := New(newLoadedContainer(t), nil)
	runtime := a.runtime()
	runtime.Resolver = selectionDeadlineResolver{runtime.Resolver}
	a.runtime = func() app.RuntimeSnapshot { return runtime }
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := a.GenerateFromPromptResolved(ctx, "like Justice, 3 tracks", []ResolutionSelection{{Kind: core.ReferenceArtist, Query: "Justice", TrackID: "seed0001"}})
	if err != nil || result.Playlist.Outcome.State != core.OutcomePartial || ctx.Err() != nil {
		t.Fatalf("selection work deadline was not reported before parent expiry: state=%s err=%v parent=%v", result.Playlist.Outcome.State, err, ctx.Err())
	}
	if result.Status.Parser.Backend == "" || result.Request.Intent.Version == 0 {
		t.Fatal("timed-out selection lost its completed interpretation")
	}
}

func (failAfterParsingEngine) AlgorithmVersion() string { return "failure-fixture/v1" }
func (failAfterParsingEngine) Build(context.Context, core.MusicIntent) (core.Playlist, error) {
	return core.Playlist{}, errors.New("fixture generation failure")
}

func TestGenerationFailureKeepsActualParserOutcome(t *testing.T) {
	a := New(newLoadedContainer(t), nil)
	useRecommendationEngine(a, failAfterParsingEngine{})
	result, err := a.GenerateFromPrompt(context.Background(), "1 track like Justice")
	if err == nil || result.Status.Parser.Backend == "" || result.Request.Intent.Version == 0 {
		t.Fatalf("failed generation lost its successful interpretation: %+v, %v", result.Status, err)
	}
	if !reflect.DeepEqual(result.Playlist.Status.Parser, result.Status.Parser) {
		t.Fatal("result parser status disagrees with operation parser status")
	}
}

func (r *deadlineCheckingRecommendationEngine) Build(ctx context.Context, intent core.MusicIntent) (core.Playlist, error) {
	deadline, ok := ctx.Deadline()
	r.hadDeadline = ok
	r.remaining = time.Until(deadline)
	intent = intent.Normalized()
	return core.Playlist{
		Mode: intent.Mode, Seed: intent.Seed, Intent: intent,
		Tracks: []core.TrackRef{{ID: "seed0001", Artist: "Justice", Title: "Genesis"}},
	}, nil
}

func TestEnhancedBuildHasSharedGenerationDeadline(t *testing.T) {
	for _, entrypoint := range []string{"resolved intent", "prompt"} {
		t.Run(entrypoint, func(t *testing.T) {
			c := newLoadedContainer(t)
			if err := c.SetRecommendationMode(core.EnhancedHybrid); err != nil {
				t.Fatal(err)
			}
			recommender := &deadlineCheckingRecommendationEngine{}
			api := New(c, nil)
			useRecommendationEngine(api, recommender)
			if entrypoint == "prompt" {
				if _, err := api.GenerateFromPrompt(context.Background(), "1 track like Justice"); err != nil {
					t.Fatal(err)
				}
			} else {
				_, err := api.BuildPlaylist(context.Background(), BuildPlaylistRequest{
					Version: core.CurrentIntentVersion,
					Intent: core.MusicIntent{
						Version:    core.CurrentIntentVersion,
						References: []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "seed0001", Influence: core.InfluencePositive}},
						Controls:   core.IntentControls{TotalTrackCount: 1, AudioWeight: .5, CooccurrenceWeight: .5},
						Seed:       "9",
					},
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if !recommender.hadDeadline || recommender.remaining <= 0 || recommender.remaining > ports.EnhancedGenerationLimit {
				t.Fatalf("Enhanced generation deadline missing or extended: %+v", recommender)
			}
		})
	}
}
