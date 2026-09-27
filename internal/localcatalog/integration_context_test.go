package localcatalog

import (
	"context"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
)

type contextOnlyResolver struct{ entered chan struct{} }

func (contextOnlyResolver) CatalogVersion() string { return "context-fixture" }
func (contextOnlyResolver) ResolveReference(core.IntentReference) core.ReferenceResolution {
	panic("composite dropped the resolution context")
}
func (r contextOnlyResolver) ResolveReferenceContext(ctx context.Context, _ core.IntentReference) core.ReferenceResolution {
	close(r.entered)
	<-ctx.Done()
	return core.ReferenceResolution{Status: core.ResolutionUnresolved}
}

func TestCompositeResolverForwardsCancellationToBase(t *testing.T) {
	local, manager := openTestCatalog(t, []librarypack.Track{{ID: "one", Artist: "Artist", Title: "Song"}}, nil)
	defer manager.Close()
	defer local.Close()
	base := contextOnlyResolver{entered: make(chan struct{})}
	r := &CompositeResolver{base: base, local: local, mode: ModeCombined}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan core.ReferenceResolution, 1)
	go func() {
		done <- r.ResolveReferenceContext(ctx, core.IntentReference{Kind: core.ReferenceArtist, Query: "Artist"})
	}()
	select {
	case <-base.entered:
	case <-time.After(time.Second):
		t.Fatal("composite never entered contextual base resolver")
	}
	cancel()
	select {
	case result := <-done:
		if result.Status != core.ResolutionUnresolved {
			t.Fatalf("canceled base query fell back to local catalog: %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("composite did not forward cancellation")
	}
}

func TestCompositeResolverLocalSQLHonorsDeadline(t *testing.T) {
	for _, kind := range []core.ReferenceKind{core.ReferenceArtist, core.ReferenceTrack} {
		t.Run(string(kind), func(t *testing.T) {
			local, manager := openTestCatalog(t, []librarypack.Track{{ID: "one", Artist: "Artist", Title: "Song"}}, nil)
			defer manager.Close()
			defer local.Close()
			local.indexes.metadata.SetMaxOpenConns(1)
			conn, err := local.indexes.metadata.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			r := &CompositeResolver{local: local, mode: ModeLibraryOnly}
			ref := core.IntentReference{Kind: kind, Query: "Artist"}
			if kind == core.ReferenceTrack {
				ref.Query = "Song"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			done := make(chan core.ReferenceResolution, 1)
			waits := local.indexes.metadata.Stats().WaitCount
			go func() { done <- r.ResolveReferenceContext(ctx, ref) }()
			select {
			case result := <-done:
				if ctx.Err() != context.DeadlineExceeded || result.Status != core.ResolutionUnresolved || local.indexes.metadata.Stats().WaitCount == waits {
					t.Fatalf("fixture did not cancel blocked local SQL: result=%+v err=%v", result, ctx.Err())
				}
			case <-time.After(time.Second):
				_ = conn.Close()
				<-done
				t.Fatal("local resolver ignored SQLite query deadline")
			}
			_ = conn.Close()
			if result := r.ResolveReference(ref); result.Status != core.ResolutionResolved {
				t.Fatalf("healthy local lookup changed: %+v", result)
			}
			if result := r.ResolveReferenceContext(ctx, core.IntentReference{Kind: core.ReferenceTrack, TrackID: local.NamespacedID("one")}); result.Status != core.ResolutionUnresolved {
				t.Fatal("expired context resolved a local ID")
			}
		})
	}
}
