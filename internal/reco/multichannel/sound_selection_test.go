package multichannel

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func TestSoundSelectionComparesCachedAlternativesBeforeStopping(t *testing.T) {
	for _, count := range []int{1, 2} {
		for _, mode := range []core.RecommendationMode{core.AcousticBrainzFirst, core.CLAPFirst, core.EnhancedHybrid} {
			t.Run(fmt.Sprintf("N%d-%s", count, mode), func(t *testing.T) {
				cat, service, retriever := recommendationPoolFixture(t, 2*count, count)
				service.Policy = audio.Policy{}
				intent := testIntent(count)
				intent.VerificationPolicy = core.BestAvailable
				intent.Controls.RecommendationMode = mode
				intent.Preferences.Moods = []core.IntentPreference{{Value: "relaxing", Influence: core.InfluencePositive}}
				engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
				engine.retriever = retriever
				got, err := engine.Build(context.Background(), intent)
				if err != nil || len(got.Tracks) != count || got.AudioEvidence == nil || len(got.AudioEvidence.Assessments) != 2*count {
					t.Fatalf("tracks=%v evidence=%+v err=%v", got.IDs(), got.AudioEvidence, err)
				}
				for _, track := range got.Tracks {
					if track.ID < fmt.Sprintf("p%03d", count) {
						t.Fatalf("weaker mood match selected despite cached alternatives: %v", got.IDs())
					}
				}
				again, err := engine.Build(context.Background(), got.Intent)
				if err != nil || !reflect.DeepEqual(got.IDs(), again.IDs()) || got.Intent.Count != count {
					t.Fatalf("comparison pool changed requested count or seed replay: %v", err)
				}
				if service.Resolver.(*noPreviewFetch).calls != 0 {
					t.Fatal("cached alternatives triggered a preview download")
				}
			})
		}
	}
}

func TestSoundComparisonStopPreservesCheckedTracks(t *testing.T) {
	cat, service, retriever := recommendationPoolFixture(t, 4, 0)
	service.Policy = audio.Policy{}
	intent := testIntent(2)
	intent.VerificationPolicy = core.BestAvailable
	intent.Preferences.Moods = []core.IntentPreference{{Value: "relaxing", Influence: core.InfluencePositive}}
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
	engine.retriever = retriever
	stop := make(chan struct{})
	checked := 0
	got, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, StopChecking: stop, OnChecked: func(core.TrackRef) {
		checked++
		if checked == 2 {
			close(stop)
		}
	}})
	if err != nil || len(got.Tracks) != 2 || checked != 2 || got.Outcome.State != core.OutcomePartial {
		t.Fatalf("stopped comparison lost checked tracks: %v checked=%d outcome=%+v err=%v", got.IDs(), checked, got.Outcome, err)
	}
}

func TestEnhancedAdditivePhraseIsNotNegation(t *testing.T) {
	for _, prompt := range []string{"not only deep bass but also sharp attacks", "not just deep bass", "not merely deep bass"} {
		found := false
		for _, clause := range enhancedClauses(core.MusicIntent{OriginalDescription: prompt}) {
			if clause.Text == "bass heavy" {
				found = true
				if clause.Negative {
					t.Fatalf("additive request inverted: %q", prompt)
				}
			}
		}
		if !found {
			t.Fatalf("bass request lost: %q", prompt)
		}
	}
}
