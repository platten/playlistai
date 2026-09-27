package bridge

import (
	"context"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type replayOnlyEngine struct{ version string }

func (e replayOnlyEngine) AlgorithmVersion() string { return e.version }
func (e replayOnlyEngine) Build(context.Context, core.MusicIntent) (core.Playlist, error) {
	panic("frozen replay reached changing providers")
}

func TestFrozenSearchReplaysWithoutProvidersOrProfileStorage(t *testing.T) {
	ctx := context.Background()
	c := newLoadedContainer(t)
	a := New(c, nil)
	generated, err := a.GenerateFromPrompt(ctx, "like Justice, 3 tracks")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := generated.Playlist.Search
	if snapshot == nil || snapshot.Result == nil || snapshot.Validate() != nil || len(snapshot.Candidates) == 0 {
		t.Fatal("actual search evidence not frozen")
	}
	// An updated engine must not prevent delivery of the immutable saved
	// result or trigger provider work to reinterpret old evidence.
	useRecommendationEngine(a, replayOnlyEngine{"new-engine-after-saved-generation"})
	if err := c.Profiles.ClearProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := a.BuildPlaylist(ctx, generated.Request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Tracks, generated.Playlist.Tracks) || result.Reproducibility.ID != generated.Playlist.Reproducibility.ID {
		t.Fatal("saved delivery was not reproduced")
	}
	if snapshot.Result.Search != nil || snapshot.Validate() != nil {
		t.Fatal("replay mutated its input")
	}
}

func TestRetiredVersionWithoutProfileCannotSilentlyRegenerate(t *testing.T) {
	a := New(newLoadedContainer(t), nil)
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Seed: "9", Controls: core.IntentControls{RecommendationMode: core.EnhancedHybrid, TotalTrackCount: 1}, References: []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "seed0001", Influence: core.InfluencePositive}}}.Normalized()
	identity, err := generationIdentity(intent, a.catalogVersion(), "retired-version", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.BuildPlaylist(context.Background(), BuildPlaylistRequest{Version: core.CurrentIntentVersion, Intent: intent, Reproducibility: identity})
	if err == nil {
		t.Fatal("legacy version replay silently regenerated")
	}
}

type correctedCreditCatalog struct{ ports.Catalog }

func (c correctedCreditCatalog) Meta(id string) (core.TrackMeta, bool) {
	meta, ok := c.Catalog.Meta(id)
	if ok {
		meta.Ref.Artist = "Corrected performer credit"
	}
	return meta, ok
}

func TestFrozenReplayKeepsSavedRecentCreditsAfterCatalogUpdate(t *testing.T) {
	ctx := context.Background()
	c := newLoadedContainer(t)
	a := New(c, nil)
	generated, err := a.GenerateFromPrompt(ctx, "like Justice, 3 tracks")
	if err != nil {
		t.Fatal(err)
	}
	req := generated.Request
	req.Search, req.Reproducibility = nil, Reproducibility{}
	track := generated.Playlist.Tracks[0]
	req.RecentSelections = []core.TrackRef{{ID: track.ID, Artist: track.Artist, Title: track.Title}}
	rebuilt, err := a.BuildPlaylist(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	req.Intent, req.Search, req.Reproducibility = rebuilt.Intent, rebuilt.Search, rebuilt.Reproducibility
	runtime := a.runtime()
	runtime.Catalog, runtime.Reco = correctedCreditCatalog{runtime.Catalog}, replayOnlyEngine{"new-version"}
	a.runtime = func() app.RuntimeSnapshot { return runtime }
	if err := c.Profiles.ClearProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	replayed, err := a.BuildPlaylist(ctx, req)
	if err != nil || replayed.Reproducibility.ID != rebuilt.Reproducibility.ID || !reflect.DeepEqual(replayed.Tracks, rebuilt.Tracks) {
		t.Fatalf("saved context was replaced with current catalog: %+v %v", replayed.Reproducibility, err)
	}
}

func TestLegacyIDOnlyRecentContextRequiresExactSavedFingerprint(t *testing.T) {
	ctx := context.Background()
	a := New(newLoadedContainer(t), nil)
	generated, err := a.GenerateFromPrompt(ctx, "like Justice, 3 tracks")
	if err != nil {
		t.Fatal(err)
	}
	req := generated.Request
	req.Search, req.Reproducibility = nil, Reproducibility{}
	req.RecentSelections = []core.TrackRef{{ID: generated.Playlist.Tracks[0].ID}}
	built, err := a.BuildPlaylist(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	req.Intent, req.Search, req.Reproducibility = built.Intent, built.Search, built.Reproducibility
	runtime := a.runtime()
	runtime.Reco = replayOnlyEngine{"updated-version"}
	for _, variant := range []string{"legacy IDs", "changed catalog", "changed supplied credit"} {
		t.Run(variant, func(t *testing.T) {
			current := runtime
			request := req
			request.RecentSelections = append([]core.TrackRef(nil), req.RecentSelections...)
			if variant == "changed catalog" {
				current.Catalog = correctedCreditCatalog{runtime.Catalog}
			}
			if variant == "changed supplied credit" {
				request.RecentSelections[0].Artist = "Explicitly different credit"
			}
			a.runtime = func() app.RuntimeSnapshot { return current }
			replayed, err := a.BuildPlaylist(ctx, request)
			if variant != "legacy IDs" {
				if err == nil {
					t.Fatal("different recent context silently replayed")
				}
				return
			}
			if err != nil || replayed.Reproducibility.ID != built.Reproducibility.ID || !reflect.DeepEqual(built.Tracks, replayed.Tracks) {
				t.Fatalf("valid ID-only history did not replay: %v", err)
			}
		})
	}
}
