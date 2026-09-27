package catalog

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/enrich/musicbrainz"
	"github.com/platten/playlistai/internal/fakes"
)

func waitForCatalogQuery(t *testing.T, db *sql.DB, previous int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for db.Stats().WaitCount == previous && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if db.Stats().WaitCount == previous {
		t.Fatal("fixture did not reach SQLite connection wait")
	}
}

func TestReferenceCancellationInterruptsSQLAndDoesNotCacheMissingResults(t *testing.T) {
	for _, kind := range []string{"artist alias", "artist search", "track search", "unicode scan", "ID", "spelling", "representatives", "dynamic base"} {
		t.Run(kind, func(t *testing.T) {
			c := metadataResolverCatalog(t)
			insertResolverTrack(t, c.db, 0, "one", "Christian Loffler", "Song")
			insertResolverTrack(t, c.db, 1, "two", "坂本龍一", "作品")
			stmt, err := c.db.Prepare("SELECT artist, title, '' FROM tracks WHERE id = ?")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			c.metaStmt = stmt
			ref := core.IntentReference{Kind: core.ReferenceArtist, Query: "Christian Loffler"}
			resolve := func(ctx context.Context) core.ReferenceResolution { return c.ResolveReferenceContext(ctx, ref) }
			switch kind {
			case "artist search":
				c.hasAliases = false
			case "track search":
				ref = core.IntentReference{Kind: core.ReferenceTrack, Query: "Song"}
			case "unicode scan":
				c.hasAliases, c.hasUnicodeSearch = false, false
				ref = core.IntentReference{Kind: core.ReferenceTrack, Query: "作品"}
			case "ID":
				ref = core.IntentReference{Kind: core.ReferenceTrack, TrackID: "one"}
			case "spelling":
				resolve = func(ctx context.Context) core.ReferenceResolution {
					return c.resolveArtistTypo(ctx, "Christan Loffler")
				}
			case "representatives":
				resolve = func(ctx context.Context) core.ReferenceResolution {
					if len(c.artistRepresentatives(ctx, "Christian Loffler")) == 0 {
						return unresolved()
					}
					return core.ReferenceResolution{Status: core.ResolutionResolved}
				}
			case "dynamic base":
				dynamic := &DynamicCatalog{base: c, resolver: c}
				resolve = func(ctx context.Context) core.ReferenceResolution { return dynamic.ResolveReferenceContext(ctx, ref) }
			}
			c.db.SetMaxOpenConns(1)
			conn, err := c.db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan core.ReferenceResolution, 1)
			waits := c.db.Stats().WaitCount
			go func() { done <- resolve(ctx) }()
			waitForCatalogQuery(t, c.db, waits)
			cancel()
			select {
			case result := <-done:
				if result.Status != core.ResolutionUnresolved {
					t.Fatalf("canceled lookup published a result: %+v", result)
				}
			case <-time.After(time.Second):
				_ = conn.Close()
				<-done
				t.Fatal("reference lookup ignored cancellation while SQLite was occupied")
			}
			if len(c.resolutionCache) != 0 || len(c.representativeCache) != 0 {
				t.Fatal("canceled lookup poisoned a shared cache")
			}
			_ = conn.Close()
			if result := resolve(context.Background()); result.Status == core.ResolutionUnresolved {
				t.Fatalf("later successful lookup lost its recording: %+v", result)
			}
			if result := resolve(ctx); result.Status != core.ResolutionUnresolved {
				t.Fatal("canceled caller received a cached result")
			}
		})
	}
}

func TestPrepareMusicCancellationInterruptsResolverSQL(t *testing.T) {
	c := metadataResolverCatalog(t)
	insertResolverTrack(t, c.db, 0, "one", "Artist", "Song")
	c.db.SetMaxOpenConns(1)
	conn, err := c.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client, err := musicbrainz.New(musicbrainz.Config{UserAgent: "offline-cancellation-fixture", MirrorURL: "http://127.0.0.1:1", Interval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Artist", Influence: core.InfluencePositive}}, Controls: core.IntentControls{TotalTrackCount: 1}, Mode: core.ModeSimilar}.Normalized()
	base := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "one", Display: "Artist - Song"})
	done := make(chan error, 1)
	waits := c.db.Stats().WaitCount
	go func() { _, err := client.PrepareMusic(ctx, intent, base, c, nil); done <- err }()
	waitForCatalogQuery(t, c.db, waits)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("parent cancellation lost: %v", err)
		}
	case <-time.After(time.Second):
		_ = conn.Close()
		<-done
		t.Fatal("PrepareMusic ignored cancellation while resolving against SQLite")
	}
}

func TestDynamicReferenceReadDoesNotWaitForSQLiteWrite(t *testing.T) {
	base := fakes.NewCatalog(1)
	d, err := OpenDynamic(base, base, filepath.Join(t.TempDir(), "dynamic.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	track := core.TrackMeta{Ref: core.TrackRef{ID: "deezer:1", Artist: "Artist", Title: "Before"}, PreviewURL: "https://example.invalid/preview"}
	if err := d.RegisterDynamicTrack(track); err != nil {
		t.Fatal(err)
	}
	d.db.SetMaxOpenConns(1)
	conn, err := d.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	waits := d.db.Stats().WaitCount
	written := make(chan error, 1)
	track.Ref.Title = "After"
	go func() { written <- d.RegisterDynamicTrack(track) }()
	waitForCatalogQuery(t, d.db, waits)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	read := make(chan core.ReferenceResolution, 1)
	go func() {
		read <- d.ResolveReferenceContext(ctx, core.IntentReference{Kind: core.ReferenceTrack, TrackID: track.Ref.ID})
	}()
	select {
	case result := <-read:
		if result.Selected == nil || result.Selected.Title != "Before" {
			t.Fatalf("uncommitted update became visible: %+v", result)
		}
	case <-ctx.Done():
		_ = conn.Close()
		<-written
		<-read
		t.Fatal("dynamic reference read waited on an unrelated SQLite write")
	}
	_ = conn.Close()
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if result := d.ResolveReference(core.IntentReference{Kind: core.ReferenceTrack, TrackID: track.Ref.ID}); result.Selected == nil || result.Selected.Title != "After" {
		t.Fatalf("committed write not published: %+v", result)
	}
}
