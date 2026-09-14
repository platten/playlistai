package bridge

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

type deadlineCheckingRecommendationEngine struct {
	hadDeadline bool
}

func (r *deadlineCheckingRecommendationEngine) Build(ctx context.Context, intent core.MusicIntent) (core.Playlist, error) {
	_, r.hadDeadline = ctx.Deadline()
	intent = intent.Normalized()
	return core.Playlist{
		Mode: intent.Mode, Seed: intent.Seed, Intent: intent,
		Tracks: []core.TrackRef{{ID: "seed0001", Artist: "Justice", Title: "Genesis"}},
	}, nil
}

func TestBuildPlaylistDoesNotAddGenerationDeadline(t *testing.T) {
	for _, entrypoint := range []string{"resolved intent", "prompt"} {
		t.Run(entrypoint, func(t *testing.T) {
			c := newLoadedContainer(t)
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
			if recommender.hadDeadline {
				t.Fatal("playlist generation added an internal deadline")
			}
		})
	}
}
