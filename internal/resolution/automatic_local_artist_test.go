package resolution

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/localcatalog"
	"github.com/platten/playlistai/internal/ports"
)

// The embedded interfaces must never be called in the library-only fixture.
type unusedAutomaticBase struct {
	ports.Catalog
	ports.CandidateRetriever
}

func TestAutomaticLocalArtistUsesPackIdentityMapping(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	archive := filepath.Join(root, "local.paipack")
	_, err := librarypack.Write(ctx, archive, librarypack.Pack{CorpusGeneration: "fixture", MetadataGeneration: "fixture", Tracks: []librarypack.Track{{ID: "one", Artist: "Local Artist", Title: "Track"}}}, librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := librarypack.OpenManager(ctx, filepath.Join(root, "managed"), librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	staged, err := manager.Stage(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	if err = localcatalog.BuildIndexes(ctx, staged.Generation(), localcatalog.IndexBuildOptions{Workers: 1, ShardRows: 2, MaxScratchBytes: 1 << 20}); err != nil {
		t.Fatal(err)
	}
	if err = manager.Activate(ctx, staged); err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	local, err := localcatalog.Open(lease, localcatalog.Options{SourceID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	base := unusedAutomaticBase{}
	overlay, err := localcatalog.NewRecommendationOverlay(ctx, local, base, &testResolver{}, base, localcatalog.ModeLibraryOnly, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(overlay.Close)
	names, err := local.LookupArtistNames(ctx, []string{"Local Artist"})
	if err != nil || len(names) != 1 || len(names[0].Candidates) != 1 {
		t.Fatalf("local lookup: %+v %v", names, err)
	}
	artist := names[0].Candidates[0]
	grounding := &core.IdentityGrounding{Provider: "paipack", MatchedSpelling: "Local Artist", SnapshotVersion: local.SnapshotIdentity().Snapshot, Candidates: []core.IdentityCandidate{{ID: artist.MBID, Name: artist.Name, Kind: core.ReferenceArtist, MatchType: string(artist.MatchType)}}}
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Controls: core.IntentControls{RecommendationMode: core.Automatic}, References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Local Artist", Grounding: grounding}}}
	got, issues := ApplyContext(ctx, overlay.Resolver, intent)
	if len(issues) != 0 || got.References[0].TrackID != local.NamespacedID("one") || got.References[0].Grounding.Decision == nil {
		t.Fatalf("local default lost: %+v %+v", got.References, issues)
	}
	selected := *got.References[0].Resolution.Selected
	for _, change := range []func(*core.ResolutionCandidate){
		func(c *core.ResolutionCandidate) { c.EntityID = "local-artist:other" },
		func(c *core.ResolutionCandidate) { c.Artist = "Other" },
		func(c *core.ResolutionCandidate) { c.Evidence = nil },
		func(c *core.ResolutionCandidate) {
			c.Representatives = []core.WeightedTrack{{TrackID: "bundled-name-only"}}
		},
	} {
		invalid := selected
		change(&invalid)
		if decidedArtistMatchesSelected(grounding.Candidates[0], invalid) {
			t.Fatalf("unrelated local identity accepted: %+v", invalid)
		}
	}
	mbid := grounding.Candidates[0]
	mbid.ID = "a74b1b7f-71a5-4011-9441-d0b5e4122711"
	if decidedArtistMatchesSelected(mbid, selected) {
		t.Fatal("local name authenticated unrelated MusicBrainz identity")
	}
}
