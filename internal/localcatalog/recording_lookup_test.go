package localcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/platten/playlistai/internal/librarypack"
)

func TestPreparedRecordingJoinUsesExactIdentityAndLibraryOnlyScope(t *testing.T) {
	const wanted = "12345678-1234-1234-1234-123456789abc"
	const wrong = "22345678-1234-1234-1234-123456789abc"
	local, manager := openTestCatalog(t, []librarypack.Track{
		{ID: "right", Artist: "Same Name", Title: "Same Title", MusicBrainzRecording: wanted},
		{ID: "wrong", Artist: "Same Name", Title: "Same Title", MusicBrainzRecording: wrong},
		{ID: "unknown", Artist: "Same Name", Title: "Same Title"},
	}, nil)
	defer manager.Close()
	defer local.Close()
	for _, mode := range []RecommendationMode{ModeCombined, ModeLibraryOnly} {
		cat := &CompositeCatalog{base: testBase{}, local: local, mode: mode}
		tracks, err := cat.RecordingsByMBID(context.Background(), wanted, 10)
		if err != nil || len(tracks) != 1 || tracks[0].ID != local.NamespacedID("right") {
			t.Fatalf("%s: %v %v", mode, tracks, err)
		}
		if tracks, err = cat.RecordingsByMBID(context.Background(), "Same Title", 10); err != nil || len(tracks) != 0 {
			t.Fatal("name substituted for identity", tracks, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := local.RecordingsByMBID(ctx, wanted, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestArtistMBIDIndexIncludesCompoundCreditsWithoutNamesakeFallback(t *testing.T) {
	const artist = "11111111-1111-4111-8111-111111111111"
	local, manager := openTestCatalog(t, []librarypack.Track{
		{ID: "compound", Artist: "Fela Kuti;Africa 70", Title: "Water", RawTags: json.RawMessage(`{"MUSICBRAINZ_ARTISTID":["11111111-1111-4111-8111-111111111111","22222222-2222-4222-8222-222222222222"]}`)},
		{ID: "namesake", Artist: "Fela Kuti", Title: "Water", RawTags: json.RawMessage(`{"MUSICBRAINZ_ARTISTID":"33333333-3333-4333-8333-333333333333"}`)},
		{ID: "album-only", Artist: "Fela Kuti", Title: "Other", RawTags: json.RawMessage(`{"MUSICBRAINZ_ALBUMARTISTID":"11111111-1111-4111-8111-111111111111"}`)},
	}, nil)
	defer manager.Close()
	defer local.Close()
	for _, mode := range []RecommendationMode{ModeCombined, ModeLibraryOnly} {
		cat := &CompositeCatalog{base: testBase{}, local: local, mode: mode}
		got, err := cat.ArtistRecordingsByMBID(context.Background(), artist, 512)
		if err != nil || len(got) != 1 || got[0].ID != local.NamespacedID("compound") {
			t.Fatalf("%s: %+v %v", mode, got, err)
		}
		got, err = cat.ArtistRecordingsByMBID(context.Background(), "Fela Kuti", 512)
		if err != nil || len(got) != 0 {
			t.Fatalf("name substituted for MBID: %+v %v", got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := local.ArtistRecordingsByMBID(ctx, artist, 5); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
