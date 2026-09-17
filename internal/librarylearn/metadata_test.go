package librarylearn

import (
	"context"
	"math"
	"reflect"
	"testing"
)

func metadataFixture() []MetadataTrack {
	return []MetadataTrack{
		{TrackID: "t3", AlbumID: "album-b", ArtistIDs: []string{"artist-b"}, AlbumArtistIDs: []string{"artist-b"}, Genres: []string{"Rock"}},
		{TrackID: "t1", AlbumID: "album-a", ArtistIDs: []string{"artist-a"}, AlbumArtistIDs: []string{"artist-a"}, Genres: []string{"R&B", "Soul"}},
		{TrackID: "t2", AlbumID: "album-a", ArtistIDs: []string{"artist-a"}, AlbumArtistIDs: []string{"artist-a"}, Genres: []string{"soul", "R&B"}},
		{TrackID: "t4", AlbumID: "album-c", ArtistIDs: []string{"artist-c", "artist-a"}, AlbumArtistIDs: []string{"artist-c"}, Genres: []string{"Rock", "Soul"}},
	}
}

func TestMetadataDeterministicAndDistinctAlbums(t *testing.T) {
	var models []MetadataModel
	for _, workers := range []int{1, 2, 4} {
		model, err := BuildMetadata(context.Background(), metadataFixture(), MetadataOptions{Workers: workers, SVDDim: 2})
		if err != nil {
			t.Fatal(err)
		}
		models = append(models, model)
	}
	for _, model := range models[1:] {
		if !reflect.DeepEqual(model, models[0]) {
			t.Fatal("metadata or SVD changed with worker count")
		}
	}
	model := models[0]
	if !reflect.DeepEqual(model.Vocabulary, []string{"r&b", "rock", "soul"}) {
		t.Fatalf("vocabulary = %v", model.Vocabulary)
	}
	if len(model.Rows) != 3 || model.SVD.Outcome != SVDAvailable {
		t.Fatalf("model = %+v", model)
	}
	if score, ok := model.WeightedGenreCosine("artist-a", "artist-c"); !ok || score <= 0 || score >= 1 {
		t.Fatalf("weighted baseline score=%v available=%v", score, ok)
	}
	if _, ok := model.WeightedGenreCosine("artist-a", "missing"); ok {
		t.Fatal("missing metadata became a zero-valued match")
	}
	duplicate := append(metadataFixture(), metadataFixture()[1])
	duplicate[len(duplicate)-1].TrackID = "duplicate-edition"
	again, err := BuildMetadata(context.Background(), duplicate, MetadataOptions{Workers: 4, SVDDim: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(model.Rows, again.Rows) || !reflect.DeepEqual(model.IDF, again.IDF) {
		t.Fatal("raw song count changed distinct artist-album weighting")
	}
	for _, row := range model.Rows {
		var squared float64
		for _, value := range row.Values {
			squared += value.Value * value.Value
		}
		if len(row.Values) > 0 && math.Abs(squared-1) > 1e-12 {
			t.Fatalf("row %s norm=%v", row.ArtistID, squared)
		}
	}
}

func TestMetadataInsufficientStructureKeepsBaseline(t *testing.T) {
	model, err := BuildMetadata(context.Background(), []MetadataTrack{{TrackID: "one", ArtistIDs: []string{"artist"}, Genres: []string{"Rock"}}}, MetadataOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if model.SVD.Outcome != SVDInsufficientStructure || len(model.Rows) != 1 || len(model.Rows[0].Values) != 1 {
		t.Fatalf("unexpected small model: %+v", model)
	}
}

func TestMetadataCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BuildMetadata(ctx, metadataFixture(), MetadataOptions{}); err == nil {
		t.Fatal("canceled metadata build succeeded")
	}
}
