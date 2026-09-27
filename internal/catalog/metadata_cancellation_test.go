package catalog

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/multichannel"
)

func TestCatalogMetadataCancellationInterruptsSQL(t *testing.T) {
	for _, kind := range []string{"base", "dynamic base", "dynamic fallback"} {
		t.Run(kind, func(t *testing.T) {
			base := openTestdata(t)
			var catalog ports.Catalog = base
			id := "seed0001"
			if kind != "base" {
				fallback := core.TrackMeta{Ref: core.TrackRef{ID: "deezer:one", Artist: "Artist", Title: "Song"}}
				catalog = &DynamicCatalog{base: base, tracks: map[string]core.TrackMeta{fallback.Ref.ID: fallback}}
				if kind == "dynamic fallback" {
					id = fallback.Ref.ID
				}
			}
			want, found := catalog.Meta(id)
			if !found {
				t.Fatal("healthy fixture metadata missing")
			}
			base.db.SetMaxOpenConns(1)
			conn, err := base.db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan bool, 1)
			waits := base.db.Stats().WaitCount
			go func() {
				_, ok := ports.CatalogMeta(ctx, catalog, id)
				done <- ok
			}()
			waitForCatalogQuery(t, base.db, waits)
			cancel()
			select {
			case ok := <-done:
				if ok {
					t.Fatal("canceled metadata read published a result or fallback")
				}
			case <-time.After(time.Second):
				_ = conn.Close()
				<-done
				t.Fatal("metadata read waited for SQLite connection release after cancellation")
			}
			_ = conn.Close()
			got, ok := ports.CatalogMeta(context.Background(), catalog, id)
			if !ok || !reflect.DeepEqual(got, want) {
				t.Fatalf("canceled read changed later metadata: got=%+v want=%+v", got, want)
			}
			if _, ok := ports.CatalogMeta(ctx, catalog, id); ok {
				t.Fatal("canceled context returned healthy metadata")
			}
		})
	}
}

func TestNamedReferenceMetadataCancellationInterruptsRetrievalSQL(t *testing.T) {
	catalog := metadataResolverCatalog(t)
	insertResolverTrack(t, catalog.db, 0, "one", "Daft Punk", "Public Song")
	stmt, err := catalog.db.Prepare("SELECT artist, title, '' FROM tracks WHERE id = ?")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	catalog.metaStmt = stmt
	similarity := fakes.NewCatalog(1, fakes.CatalogTrack{ID: "one", Display: "Daft Punk - Public Song", Audio: []float32{1}, Track: []float32{1}})
	retriever := multichannel.NewRetriever(catalog, fakes.NewSimilarityEngine(similarity), multichannel.Config{})
	catalog.db.SetMaxOpenConns(1)
	conn, err := catalog.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	intent := core.MusicIntent{
		Version: core.CurrentIntentVersion,
		References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Daft Punk", TrackID: "one", Influence: core.InfluencePositive,
			Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, Selected: &core.ResolutionCandidate{
				Kind: core.ReferenceArtist, Artist: "Daft Punk", Representatives: []core.WeightedTrack{{TrackID: "one", Weight: 1}},
			}},
		}},
		Controls: core.IntentControls{TotalTrackCount: 10, RecommendationMode: core.EnhancedHybrid}, Mode: core.ModeSimilar,
	}.Normalized()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	waits := catalog.db.Stats().WaitCount
	go func() { _, err := retriever.Retrieve(ctx, ports.RetrievalRequest{Intent: intent}); done <- err }()
	waitForCatalogQuery(t, catalog.db, waits)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("retrieval lost cancellation: %v", err)
		}
	case <-time.After(time.Second):
		_ = conn.Close()
		<-done
		t.Fatal("named-reference retrieval waited for SQLite connection release after cancellation")
	}
}
