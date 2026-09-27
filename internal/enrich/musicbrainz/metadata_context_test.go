package musicbrainz

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

// Return a valid row at the cancellation boundary: callers must neither use
// legacy metadata nor publish the interrupted identity as a successful match.
type interruptedMetadataCatalog struct {
	*fakes.Catalog
	cancel             context.CancelFunc
	legacy, contextual int
}

func (c *interruptedMetadataCatalog) Meta(id string) (core.TrackMeta, bool) {
	c.legacy++
	c.cancel()
	return c.Catalog.Meta(id)
}

func (c *interruptedMetadataCatalog) MetaContext(ctx context.Context, id string) (core.TrackMeta, bool) {
	c.contextual++
	c.cancel()
	return c.Catalog.Meta(id)
}

func TestAcquisitionMetadataCancellationDoesNotPublishIdentity(t *testing.T) {
	for _, stage := range []string{"index", "seed", "knowledge", "context seeds", "known recording"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			base := fakes.NewCatalog(1, fakes.CatalogTrack{ID: "track", Display: "Artist - Song", Audio: []float32{1}, Track: []float32{1}})
			cat := &interruptedMetadataCatalog{Catalog: base, cancel: cancel}
			ref := core.TrackRef{ID: "track", Artist: "Artist", Title: "Song"}
			switch stage {
			case "index":
				if got := indexKnownArtistRecordings(ctx, cat, []core.TrackRef{ref}); len(got) != 0 {
					t.Errorf("interrupted row published as identity index: %v", got)
				}
			case "seed":
				if got, ok := matchSeedRecording(ctx, cat, base, "Song", []string{"Artist"}); ok || got.ID != "" {
					t.Errorf("interrupted row published as seed: %+v", got)
				}
			case "knowledge":
				client := &Client{}
				snapshot := &core.KnowledgeSnapshot{}
				client.addKnowledgeRecording(ctx, mbRecording{ID: "recording", Title: "Song", ArtistCredit: []mbArtistCredit{{Name: "Artist"}}}, cat, base, snapshot, "track")
				if len(snapshot.Tracks) != 0 || len(snapshot.Candidates) != 0 {
					t.Errorf("interrupted row published as knowledge: %+v", snapshot)
				}
			case "context seeds":
				if got := contextCatalogSeeds(ctx, []core.WeightedTrack{{TrackID: "track", Weight: 1}}, cat); len(got) != 0 {
					t.Errorf("interrupted row published as context seed: %+v", got)
				}
			case "known recording":
				index := map[string]string{recordingNameKey("Artist", "Song"): "track"}
				if got := matchKnownRecording(ctx, cat, index, mbRecording{Title: "Song", ArtistCredit: []mbArtistCredit{{Name: "Artist"}}}); got != "" {
					t.Errorf("interrupted row used for recording match: %s", got)
				}
			}
			if cat.legacy != 0 || cat.contextual != 1 || ctx.Err() != context.Canceled {
				t.Fatalf("metadata did not receive cancellation: legacy=%d contextual=%d err=%v", cat.legacy, cat.contextual, ctx.Err())
			}
		})
	}
}
