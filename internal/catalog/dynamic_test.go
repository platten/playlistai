package catalog_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

func TestDynamicSearchSnapshotInvalidatesAndPreservesStableOrder(t *testing.T) {
	base := fakes.NewCatalog(1)
	path := filepath.Join(t.TempDir(), "tracks.sqlite")
	d, err := catalog.OpenDynamic(base, base, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if got := d.Resolve("", 20); len(got) != 0 {
		t.Fatal(got)
	}
	for _, id := range []string{"deezer:3", "deezer:1", "deezer:2"} {
		if err := d.RegisterDynamicTrack(core.TrackMeta{Ref: core.TrackRef{ID: id, Artist: "Björk", Title: "Old song"}, PreviewURL: "https://example.invalid/p"}); err != nil {
			t.Fatal(err)
		}
	}
	before := d.Resolve("BJÖRK", 2)
	if len(before) != 2 || before[0].ID != "deezer:1" || before[1].ID != "deezer:2" {
		t.Fatalf("unstable limited search: %+v", before)
	}
	if err := d.RegisterDynamicTrack(core.TrackMeta{Ref: core.TrackRef{ID: "deezer:1", Artist: "Other artist", Title: "New song"}, PreviewURL: "https://example.invalid/p"}); err != nil {
		t.Fatal(err)
	}
	if got := d.Resolve("new song", 20); len(got) != 1 || got[0].ID != "deezer:1" {
		t.Fatalf("stale updated search: %+v", got)
	}
	if before[0].Title != "Old song" {
		t.Fatal("previous search result mutated")
	}
	reopened, err := catalog.OpenDynamic(base, base, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, want := reopened.Resolve("", 20), d.Resolve("", 20); !reflect.DeepEqual(got, want) {
		t.Fatalf("reload search differs: %v / %v", got, want)
	}
	if d.Len() != 0 {
		t.Fatal("search cache added dense rows")
	}
}

func TestDynamicCatalogPersistsMetadataWithoutAddingDenseRows(t *testing.T) {
	base := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "spotify1", Display: "Base - Song", Audio: []float32{1, 0}, Track: []float32{0, 1}})
	path := filepath.Join(t.TempDir(), "candidate-catalog", "tracks.sqlite")
	overlay, err := catalog.OpenDynamic(base, base, path)
	if err != nil {
		t.Fatal(err)
	}
	duration := &core.RecordingDuration{Milliseconds: 183000, Source: "musicbrainz", RecordingID: "mbid-1"}
	meta := core.TrackMeta{Ref: core.TrackRef{ID: "deezer:42", Artist: "New Artist", Title: "New Song"}, PreviewURL: "https://cdn.example/42.mp3", Album: "New Album", AlbumReliable: true, FullRecordingDuration: duration}
	if err := overlay.RegisterDynamicTrack(meta); err != nil {
		t.Fatal(err)
	}
	if overlay.Len() != base.Len() {
		t.Fatalf("dynamic track changed dense row count: %d", overlay.Len())
	}
	if _, ok := overlay.RowOf(meta.Ref.ID); ok {
		t.Fatal("dynamic track entered dense vector rows")
	}
	if _, ok := overlay.Vectors(meta.Ref.ID); ok {
		t.Fatal("dynamic track acquired incompatible Deej-AI vectors")
	}
	if got, ok := overlay.Meta(meta.Ref.ID); !ok || got.Ref != meta.Ref || got.PreviewURL != meta.PreviewURL {
		t.Fatalf("registered metadata = %+v ok=%v", got, ok)
	}
	if got := overlay.ResolveReference(core.IntentReference{Kind: core.ReferenceTrack, TrackID: meta.Ref.ID}); got.Status != core.ResolutionResolved || got.Selected == nil || got.Selected.EntityID != meta.Ref.ID {
		t.Fatalf("dynamic resolution = %+v", got)
	}
	if err := overlay.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := catalog.OpenDynamic(base, base, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, ok := reopened.Meta(meta.Ref.ID)
	if !ok || got.Ref != meta.Ref || got.FullRecordingDuration == nil || got.FullRecordingDuration.RecordingID != "mbid-1" {
		t.Fatalf("persisted metadata = %+v ok=%v", got, ok)
	}
}

func TestDynamicCatalogRejectsUnscopedOrUnplayableTracks(t *testing.T) {
	base := fakes.NewCatalog(1)
	overlay, err := catalog.OpenDynamic(base, base, filepath.Join(t.TempDir(), "tracks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer overlay.Close()
	for _, meta := range []core.TrackMeta{
		{Ref: core.TrackRef{ID: "42", Artist: "A", Title: "T"}, PreviewURL: "https://example/42.mp3"},
		{Ref: core.TrackRef{ID: "deezer:42", Artist: "A", Title: "T"}},
	} {
		if err := overlay.RegisterDynamicTrack(meta); err == nil {
			t.Fatalf("accepted invalid dynamic track: %+v", meta)
		}
	}
}
