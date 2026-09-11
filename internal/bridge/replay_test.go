package bridge

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestRebuildPinsProfileButChangedControlsUseCurrentTaste(t *testing.T) {
	ctx := context.Background()
	c := newLoadedContainer(t)
	api := New(c, nil)
	generated, err := api.GenerateFromPrompt(ctx, "like Justice, 3 tracks")
	if err != nil {
		t.Fatal(err)
	}
	if err := api.AcknowledgePlaylistDisplayed(ctx, generated.Playlist.PresentationID); err != nil {
		t.Fatal(err)
	}
	req := generated.Request
	// Display adds exposures, so a newly built profile differs even
	// without an explicit like/dislike. Exact replay must use the saved one.
	replayed, err := api.BuildPlaylist(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Reproducibility.ProfileSnapshot != req.Reproducibility.ProfileSnapshot {
		t.Fatal("exact replay rebuilt the taste profile")
	}
	oldVersion := req
	oldVersion.Reproducibility.AlgorithmVersion = "retired-version"
	if _, err := api.BuildPlaylist(ctx, oldVersion); err == nil {
		t.Fatal("incompatible algorithm silently replayed")
	}
	seed := core.RNGSeed("123456789")
	req.Overrides.Seed = &seed
	fresh, err := api.BuildPlaylist(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Reproducibility.ProfileSnapshot == generated.Request.Reproducibility.ProfileSnapshot {
		t.Fatal("fresh generation ignored new exposures")
	}
	if err := c.Profiles.ClearProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := api.BuildPlaylist(ctx, generated.Request); err == nil {
		t.Fatal("missing saved snapshot silently used current taste")
	}
}

func TestOverridesInvalidateLegacyDiscoveryWithoutMutatingSavedIntent(t *testing.T) {
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Seed: "1",
		Knowledge: &core.KnowledgeSnapshot{DiscoveryRecorded: true, Discovery: []core.TrackRef{{ID: "old"}}},
	}.Normalized()
	same := applyOverrides(intent, ControlOverrides{})
	if !same.Knowledge.DiscoveryRecorded {
		t.Fatal("unchanged intent lost replay")
	}
	seed := core.RNGSeed("2")
	changed := applyOverrides(intent, ControlOverrides{Seed: &seed})
	if changed.Knowledge.DiscoveryRecorded || len(changed.Knowledge.Discovery) != 0 {
		t.Fatal("new seed kept the old discovery sequence")
	}
	if !intent.Knowledge.DiscoveryRecorded || len(intent.Knowledge.Discovery) != 1 {
		t.Fatal("override mutated the saved intent")
	}
}
