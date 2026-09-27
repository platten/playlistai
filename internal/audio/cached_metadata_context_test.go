package audio

import (
	"context"
	"errors"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

type interruptedCachedMetadata struct {
	*fakes.Catalog
	cancel             context.CancelFunc
	legacy, contextual int
}

func (c *interruptedCachedMetadata) Meta(id string) (core.TrackMeta, bool) {
	c.legacy++
	c.cancel()
	return c.Catalog.Meta(id)
}

func (c *interruptedCachedMetadata) MetaContext(ctx context.Context, id string) (core.TrackMeta, bool) {
	c.contextual++
	c.cancel()
	return c.Catalog.Meta(id)
}

func TestCachedSearchMetadataCancellationRetainsOnlyCompletedRows(t *testing.T) {
	service, _, resolver, _ := testService(t)
	base := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "track", Display: "Artist - Song"})
	meta, _ := base.Meta("track")
	record := validStoredAnalysis()
	record.TrackKey = core.ProvisionalRecordingKey(meta.Ref)
	record.ID = ""
	record.ID = Fingerprint(record)
	if err := service.Store.Put(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	intent := core.MusicIntent{VerificationPolicy: core.BestAvailable, Preferences: core.SemanticPreferences{Moods: []core.IntentPreference{{Value: "sleepy", Influence: core.InfluencePositive}}}}
	session, err := service.Begin(context.Background(), intent, "catalog", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cat := &interruptedCachedMetadata{Catalog: base, cancel: cancel}
	got, err := session.CachedCandidates(ctx, cat, 1, nil)
	if !errors.Is(err, context.Canceled) || len(got) != 0 || cat.legacy != 0 || cat.contextual != 1 {
		t.Fatalf("interrupted metadata admitted: rows=%v error=%v legacy=%d contextual=%d", got, err, cat.legacy, cat.contextual)
	}
	if resolver.calls != 0 || len(session.Snapshot().Assessments) != 0 {
		t.Fatal("canceled cache search began acquisition or wrote assessments")
	}
}
