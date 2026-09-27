package localcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/ports"
)

func TestCompositeMetadataCancellationInterruptsLocalSQL(t *testing.T) {
	for _, path := range []string{"local", "local library only", "base match", "recording metadata"} {
		t.Run(path, func(t *testing.T) {
			const recording = "12345678-1234-1234-1234-123456789abc"
			local, manager := openTestCatalog(t, []librarypack.Track{{
				ID: "one", Artist: "Artist", Title: "Song", Album: "Album",
				MusicBrainzRecording: recording, ISRC: "USAAA2400001",
				DurationMilliseconds: 180000, DurationReliable: true, DurationProvenance: "fixture",
				RawTags: json.RawMessage(`{"genre":"jazz"}`),
			}}, nil)
			defer manager.Close()
			defer local.Close()
			base := recordingMetadataBase{meta: core.TrackMeta{Ref: core.TrackRef{ID: "outside", Artist: "Artist", Title: "Song"}, MusicBrainzRecording: recording}}
			catalog := &CompositeCatalog{base: base, local: local, mode: ModeCombined}
			id := local.NamespacedID("one")
			if path == "local library only" {
				catalog.mode = ModeLibraryOnly
			}
			if path == "base match" {
				id = "outside"
			}
			want, ok := catalog.Meta(id)
			if !ok || len(want.Annotations) == 0 {
				t.Fatal("healthy annotated metadata fixture missing")
			}
			var release func()
			var waits int64
			if path == "base match" {
				local.indexes.metadata.SetMaxOpenConns(1)
				conn, err := local.indexes.metadata.Conn(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				release = func() { _ = conn.Close() }
				waits = local.indexes.metadata.Stats().WaitCount
			} else {
				// The immutable generation pool has four connections. Open row
				// streams hold all four without exposing or altering its database.
				var sources []librarypack.TrackReadCloser
				release = func() {
					for _, source := range sources {
						_ = source.Close()
					}
				}
				defer release()
				for range 4 {
					source, err := local.generation.OpenTrackSource(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					sources = append(sources, source)
				}
			}
			defer release()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			done := make(chan bool, 1)
			go func() {
				if path == "recording metadata" {
					_, ok, _ := catalog.LibraryRecordingMetadata(ctx, id)
					done <- ok
					return
				}
				_, ok := catalog.MetaContext(ctx, id)
				done <- ok
			}()
			select {
			case ok := <-done:
				if ok || ctx.Err() != context.DeadlineExceeded {
					t.Fatalf("blocked local query returned before deadline or published metadata: ok=%v err=%v", ok, ctx.Err())
				}
			case <-time.After(time.Second):
				release()
				<-done
				t.Fatal("local metadata ignored cancellation before connection release")
			}
			if path == "base match" && local.indexes.metadata.Stats().WaitCount == waits {
				t.Fatal("fixture did not reach matching SQL")
			}
			release()
			got, ok := catalog.MetaContext(context.Background(), id)
			if !ok || !reflect.DeepEqual(got, want) {
				t.Fatalf("later metadata changed or retained canceled miss: got=%+v want=%+v", got, want)
			}
			if _, ok := catalog.MetaContext(ctx, id); ok {
				t.Fatal("expired context returned metadata")
			}
			if len(base.meta.Annotations) != 0 {
				t.Fatal("composite mutated base metadata")
			}
		})
	}
}

func TestRecordingMetadataForwardsCancellationToBaseAnnotations(t *testing.T) {
	local, manager := openTestCatalog(t, []librarypack.Track{{ID: "one", Artist: "Artist", Title: "Song", RawTags: json.RawMessage(`{"genre":"jazz"}`)}}, nil)
	defer manager.Close()
	defer local.Close()
	base := cancellationMetadataBase{entered: make(chan struct{})}
	catalog := &CompositeCatalog{base: base, local: local, mode: ModeCombined}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := catalog.LibraryRecordingMetadata(ctx, local.NamespacedID("one")); done <- err }()
	select {
	case <-base.entered:
	case <-time.After(time.Second):
		t.Fatal("recording annotations did not reach contextual base metadata")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("recording metadata lost cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("recording annotations did not forward cancellation")
	}
}

type cancellationMetadataBase struct {
	testBase
	entered chan struct{}
}

func (cancellationMetadataBase) Meta(string) (core.TrackMeta, bool) {
	panic("composite dropped metadata context")
}

func (b cancellationMetadataBase) MetaContext(ctx context.Context, id string) (core.TrackMeta, bool) {
	if ctx.Done() != nil {
		close(b.entered)
		<-ctx.Done()
	}
	return core.TrackMeta{Ref: core.TrackRef{ID: id, Artist: "Artist", Title: "Song"}}, true
}

func TestCompositeMetadataForwardsCancellationThroughNestedBase(t *testing.T) {
	local, manager := openTestCatalog(t, []librarypack.Track{{ID: "one", Artist: "Artist", Title: "Song"}}, nil)
	defer manager.Close()
	defer local.Close()
	base := cancellationMetadataBase{entered: make(chan struct{})}
	inner := &CompositeCatalog{base: base, local: local, mode: ModeCombined}
	outer := &CompositeCatalog{base: inner, local: local, mode: ModeCombined}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan bool, 1)
	go func() { _, ok := ports.CatalogMeta(ctx, outer, "outside"); done <- ok }()
	select {
	case <-base.entered:
	case <-time.After(time.Second):
		t.Fatal("nested metadata did not reach contextual base")
	}
	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("canceled nested lookup published base metadata")
		}
	case <-time.After(time.Second):
		t.Fatal("nested metadata did not forward cancellation")
	}
	want, ok := outer.Meta("outside")
	got, found := outer.MetaContext(context.Background(), "outside")
	if !ok || !found || !reflect.DeepEqual(got, want) {
		t.Fatal("healthy nested metadata no longer matches legacy behavior")
	}
}

type bindingCancellationBase struct {
	testBase
	entered  chan struct{}
	received context.Context
}

func (*bindingCancellationBase) Meta(id string) (core.TrackMeta, bool) {
	return core.TrackMeta{Ref: core.TrackRef{ID: id, Artist: "Artist", Title: id}}, true
}

func (b *bindingCancellationBase) MetaContext(ctx context.Context, id string) (core.TrackMeta, bool) {
	if id == "two" && ctx.Done() != nil {
		close(b.entered)
		<-ctx.Done()
	}
	return b.Meta(id)
}

func (*bindingCancellationBase) BindRecordingKnowledge([]core.EnrichedTrack) {
	panic("contextual base binder was bypassed")
}

func (b *bindingCancellationBase) BindRecordingKnowledgeContext(ctx context.Context, _ []core.EnrichedTrack) {
	b.received = ctx
}

func TestRecordingKnowledgeCancellationDoesNotPublishPartialMap(t *testing.T) {
	base := &bindingCancellationBase{entered: make(chan struct{})}
	prior := map[string]core.EnrichedTrack{"retained": {Ref: core.TrackRef{ID: "retained"}}}
	catalog := &CompositeCatalog{base: base, recordingKnowledge: prior}
	tracks := []core.EnrichedTrack{
		{Ref: core.TrackRef{ID: "one", Artist: "Artist", Title: "one"}, RecordingID: "12345678-1234-1234-1234-123456789abc", IdentityStatus: core.ResolutionResolved},
		{Ref: core.TrackRef{ID: "two", Artist: "Artist", Title: "two"}, RecordingID: "22345678-1234-1234-1234-123456789abc", IdentityStatus: core.ResolutionResolved},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { catalog.BindRecordingKnowledgeContext(ctx, tracks); close(done) }()
	select {
	case <-base.entered:
		if base.received != ctx {
			t.Fatal("base binder did not receive caller context")
		}
	case <-time.After(time.Second):
		t.Fatal("binder did not reach contextual metadata after first identity")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("binder did not stop after cancellation")
	}
	if !reflect.DeepEqual(catalog.recordingKnowledge, prior) {
		t.Fatal("canceled binder replaced prior knowledge with a partial map")
	}
	catalog.BindRecordingKnowledgeContext(context.Background(), tracks)
	if len(catalog.recordingKnowledge) != 2 {
		t.Fatal("healthy binding retained canceled miss")
	}
	legacy := &CompositeCatalog{base: base}
	legacy.BindRecordingKnowledge(tracks)
	if !reflect.DeepEqual(catalog.recordingKnowledge, legacy.recordingKnowledge) {
		t.Fatal("active contextual binding changed legacy identities")
	}
	catalog.BindRecordingKnowledgeContext(ctx, nil)
	if !reflect.DeepEqual(catalog.recordingKnowledge, legacy.recordingKnowledge) {
		t.Fatal("already canceled binder cleared existing knowledge")
	}
}

type legacyCancelBinder struct {
	testBase
	cancel context.CancelFunc
}

func (b legacyCancelBinder) BindRecordingKnowledge([]core.EnrichedTrack) { b.cancel() }

func TestRecordingKnowledgeLegacyBinderCancellationKeepsPriorMap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prior := map[string]core.EnrichedTrack{"retained": {Ref: core.TrackRef{ID: "retained"}}}
	catalog := &CompositeCatalog{base: legacyCancelBinder{cancel: cancel}, recordingKnowledge: prior}
	catalog.BindRecordingKnowledgeContext(ctx, nil)
	if !reflect.DeepEqual(catalog.recordingKnowledge, prior) {
		t.Fatal("legacy base cancellation published an empty identity map")
	}
}
