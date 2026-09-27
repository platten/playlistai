package bridge

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type automaticDeadlineEngine struct {
	deadlineCheckingRecommendationEngine
}

func TestAutomaticFrozenReplayKeepsFitAndArtistDecisions(t *testing.T) {
	c := newLoadedContainer(t)
	if err := c.SetRecommendationMode(core.Automatic); err != nil {
		t.Fatal(err)
	}
	a := New(c, nil)
	generated, err := a.GenerateFromPrompt(context.Background(), "like Justice, 3 tracks")
	if err != nil {
		t.Fatal(err)
	}
	if generated.Playlist.Search == nil || generated.Playlist.Search.PolicyVersion != core.AutomaticSearchPolicyVersion {
		t.Fatal("automatic search not frozen")
	}
	runtime := a.runtime()
	runtime.AutomaticReco = replayOnlyEngine{"changed-automatic-version"}
	a.runtime = func() app.RuntimeSnapshot { return runtime }
	if err := c.Profiles.ClearProfiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	replayed, err := a.BuildPlaylist(context.Background(), generated.Request)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Reproducibility.ID != generated.Playlist.Reproducibility.ID || !reflect.DeepEqual(replayed.FitAssessments, generated.Playlist.FitAssessments) || !reflect.DeepEqual(replayed.Intent.References, generated.Playlist.Intent.References) {
		t.Fatal("automatic replay changed frozen evidence or identity")
	}
}

func TestAutomaticArtistCorrectionPreservesAlternativesAndInput(t *testing.T) {
	c := newLoadedContainer(t)
	a := New(c, nil)
	runtime := a.runtime()
	runtime.AutomaticReco = &automaticDeadlineEngine{}
	a.runtime = func() app.RuntimeSnapshot { return runtime }
	const first = "11111111-1111-4111-8111-111111111111"
	const second = "22222222-2222-4222-8222-222222222222"
	grounding := &core.IdentityGrounding{Provider: "MusicBrainz", MatchType: "canonical", SnapshotVersion: "fixture", MatchedSpelling: "Justice", Candidates: []core.IdentityCandidate{
		{ID: first, Kind: core.ReferenceArtist, Name: "Justice", MatchType: "canonical"},
		{ID: second, Kind: core.ReferenceArtist, Name: "Justice", MatchType: "alias"},
	}}
	grounding.Decision = core.DecideArtist(grounding)
	req := BuildPlaylistRequest{Version: core.CurrentIntentVersion, Intent: core.MusicIntent{Version: core.CurrentIntentVersion, Seed: "9", Controls: core.IntentControls{RecommendationMode: core.Automatic, TotalTrackCount: 1}, References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Justice", Influence: core.InfluencePositive, Grounding: grounding}}}, ArtistSelections: []ResolutionSelection{{Kind: core.ReferenceArtist, Query: "Justice", IdentityID: second}}}
	result, err := a.BuildPlaylist(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	got := result.Intent.References[0].Grounding
	if !got.Confirmed || got.Decision != nil || got.Candidates[0].ID != second || len(got.Alternatives) != 1 || got.Alternatives[0].ID != first {
		t.Fatal("artist correction lost provenance or alternatives", got)
	}
	if grounding.Confirmed || len(grounding.Candidates) != 2 || grounding.Decision == nil || grounding.Decision.SelectedID != first {
		t.Fatal("artist correction mutated original request")
	}
	req.ArtistSelections[0].IdentityID = "33333333-3333-4333-8333-333333333333"
	if _, err := a.BuildPlaylist(context.Background(), req); err == nil {
		t.Fatal("unoffered artist identity accepted")
	}
}

func (*automaticDeadlineEngine) AlgorithmVersion() string { return "automatic-test/v1" }

func TestAutomaticEntryPointsUseOwnEngineAndSharedBudget(t *testing.T) {
	for _, entry := range []string{"prompt", "resolved", "submitted"} {
		t.Run(entry, func(t *testing.T) {
			c := newLoadedContainer(t)
			if err := c.SetRecommendationMode(core.Automatic); err != nil {
				t.Fatal(err)
			}
			a := New(c, nil)
			runtime := a.runtime()
			engine := &automaticDeadlineEngine{}
			runtime.AutomaticReco = engine
			runtime.Reco = failAfterParsingEngine{}
			a.runtime = func() app.RuntimeSnapshot { return runtime }
			var result PlaylistResult
			var err error
			if entry == "resolved" {
				result, err = a.BuildPlaylist(context.Background(), BuildPlaylistRequest{Version: core.CurrentIntentVersion, Intent: core.MusicIntent{Version: core.CurrentIntentVersion, Controls: core.IntentControls{RecommendationMode: core.Automatic, TotalTrackCount: 1}, References: []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "seed0001", Influence: core.InfluencePositive}}, Seed: "9223372036854775806"}})
			} else {
				input := ports.IntentInput{Prompt: "1 track like Justice"}
				if entry == "submitted" {
					input.SubmittedAtMilliseconds = time.Now().Add(-4 * time.Second).UnixMilli()
				}
				var generated GenerateResult
				generated, err = a.generateFromPromptOperation(context.Background(), input, nil)
				result = generated.Playlist
			}
			if err != nil {
				t.Fatal(err)
			}
			if !engine.hadDeadline || engine.remaining <= 0 || engine.remaining > ports.AutomaticGenerationLimit {
				t.Fatalf("automatic budget not shared: %v", engine.remaining)
			}
			if entry == "submitted" && engine.remaining > ports.AutomaticGenerationLimit-4*time.Second {
				t.Fatal("submission clock restarted", engine.remaining)
			}
			if result.Reproducibility.AlgorithmVersion != "automatic-test/v1" || result.Intent.Controls.RecommendationMode != core.Automatic {
				t.Fatal("lost engine identity", result.Reproducibility)
			}
		})
	}
}
