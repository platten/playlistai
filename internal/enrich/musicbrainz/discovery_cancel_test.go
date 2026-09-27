package musicbrainz

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

type blockingContextResolver struct {
	*fakes.Catalog
	entered chan struct{}
	legacy  atomic.Int32
}

func (r *blockingContextResolver) ResolveReference(core.IntentReference) core.ReferenceResolution {
	r.legacy.Add(1)
	return core.ReferenceResolution{Status: core.ResolutionUnresolved}
}

func (r *blockingContextResolver) ResolveReferenceContext(ctx context.Context, _ core.IntentReference) core.ReferenceResolution {
	close(r.entered)
	<-ctx.Done()
	return core.ReferenceResolution{Status: core.ResolutionUnresolved}
}

func TestReferenceLookupCancellationStopsPreparationAndDiscovery(t *testing.T) {
	for _, preparation := range []bool{true, false} {
		t.Run(fmt.Sprintf("preparation=%t", preparation), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cat := fakes.NewCatalog(2)
			resolver := &blockingContextResolver{Catalog: cat, entered: make(chan struct{})}
			client := newClient(t, "http://127.0.0.1:1", time.Nanosecond)
			stream := candidateStream{client: client, cat: cat, resolver: resolver, initialized: true, artists: []core.GenreArtist{{ID: contextArtistID, Name: "Artist"}}}
			done := make(chan error, 1)
			go func() {
				if preparation {
					_, err := client.PrepareMusic(ctx, seedTestIntent(), cat, resolver, nil)
					done <- err
				} else {
					_, err := stream.nextMusicBrainz(ctx)
					done <- err
				}
			}()
			select {
			case <-resolver.entered:
			case <-time.After(time.Second):
				t.Fatal("reference lookup did not receive the request context")
			}
			cancel()
			select {
			case err := <-done:
				if err != context.Canceled {
					t.Fatalf("canceled resolution became a semantic result: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("reference lookup did not stop after cancellation")
			}
			if resolver.legacy.Load() != 0 || stream.recordingReads != 0 {
				t.Fatal("canceled lookup used legacy resolution or continued discovery")
			}
		})
	}
}

func TestPrepareMusicCanceledBeforeSavedEvidence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &Client{}
	cat := fakes.NewCatalog(2)
	for _, saved := range []*core.KnowledgeSnapshot{nil, {ID: "saved"}} {
		intent := core.MusicIntent{Knowledge: saved}
		if _, err := client.PrepareMusic(ctx, intent, cat, cat, nil); err != context.Canceled {
			t.Fatalf("saved=%v: cancellation=%v", saved, err)
		}
	}
}

func TestStopPrefetchCancelsCoordinatorLookupBeforeWaitingForLock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cat := fakes.NewCatalog(2)
	resolver := &blockingContextResolver{Catalog: cat, entered: make(chan struct{})}
	client := newClient(t, "http://127.0.0.1:1", time.Nanosecond)
	prefetch := &discoveryPrefetch{ctx: ctx, cancel: cancel, pages: make(map[string]*prefetchedPage)}
	stream := candidateStream{client: client, cat: cat, resolver: resolver, prefetch: prefetch, artists: []core.GenreArtist{{ID: contextArtistID, Name: "Artist"}}}
	prefetch.wg.Add(1)
	go func() {
		defer prefetch.wg.Done()
		stream.schedulePrefetch()
	}()
	select {
	case <-resolver.entered:
	case <-time.After(time.Second):
		t.Fatal("coordinator lookup did not start")
	}
	done := make(chan struct{})
	go func() {
		stream.StopPrefetch()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("prefetch shutdown waited for its own uncanceled lookup")
	}
	if prefetch.issued != 0 || resolver.legacy.Load() != 0 {
		t.Fatal("stopped prefetch dispatched provider work or used legacy resolution")
	}
}

type cancelingMetadataCatalog struct {
	*fakes.Catalog
	cancel context.CancelFunc
	calls  int
	tracks []core.TrackRef
}

func (c *cancelingMetadataCatalog) Meta(id string) (core.TrackMeta, bool) {
	c.calls++
	if c.calls == 1 {
		c.cancel()
	}
	return c.Catalog.Meta(id)
}

func (c *cancelingMetadataCatalog) ArtistRecordings(context.Context, string) ([]core.TrackRef, error) {
	return c.tracks, nil
}

func TestCanceledDiscoveryStopsArtistMetadataIndex(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var rows []fakes.CatalogTrack
	var tracks []core.TrackRef
	for i := range 32 {
		id := fmt.Sprint(i)
		rows = append(rows, fakes.CatalogTrack{ID: id, Display: "Artist - Song " + id})
		tracks = append(tracks, core.TrackRef{ID: id, Artist: "Artist", Title: "Song " + id})
	}
	base := fakes.NewCatalog(2, rows...)
	catalog := &cancelingMetadataCatalog{Catalog: base, cancel: cancel, tracks: tracks}
	client := newClient(t, "http://127.0.0.1:1", time.Nanosecond)
	stream := candidateStream{client: client, cat: catalog, resolver: base, initialized: true, artists: []core.GenreArtist{{ID: contextArtistID, Name: "Artist"}}}
	if _, err := stream.nextMusicBrainz(ctx); err != context.Canceled {
		t.Fatalf("cancellation=%v", err)
	}
	if catalog.calls != 1 || len(stream.exactByArtist) != 0 || stream.recordingReads != 0 {
		t.Fatalf("canceled discovery continued metadata or provider work: calls=%d indexed=%d provider=%d", catalog.calls, len(stream.exactByArtist), stream.recordingReads)
	}
}
