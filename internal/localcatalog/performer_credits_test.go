package localcatalog

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/platten/playlistai/internal/librarypack"
)

func TestRecordingRolesCorrectDisplayWithoutChangingIdentity(t *testing.T) {
	raw := json.RawMessage(`{"ARTIST":"Arthur Rubinstein;Richard Gardner","PERFORMER_NAME":"Arthur Rubinstein","RECORDING ENGINEER":"Richard Gardner"}`)
	tracks := []librarypack.Track{{ID: "piano", Artist: "Arthur Rubinstein;Richard Gardner", Title: "Mazurka", RecordingIdentity: "musicbrainz:9b523fda-78bd-4232-b6db-0c25194d010d", RawTags: raw}}
	tracks = append(tracks, librarypack.Track{ID: "spaced", Artist: "Pianist; Engineer", Title: "Piece", RawTags: json.RawMessage(`{"PERFORMER_NAME":"Pianist","RECORDING ENGINEER":"Engineer"}`)})
	c, manager := openTestCatalog(t, tracks, nil)
	defer manager.Close()
	defer c.Close()
	track, found, err := c.Lookup(context.Background(), c.NamespacedID("piano"))
	if err != nil || !found || track.Artist != "Arthur Rubinstein" || track.RecordingIdentity != tracks[0].RecordingIdentity || track.ID != c.NamespacedID("piano") {
		t.Fatalf("role correction changed identity or kept engineer: %+v %v", track, err)
	}
	if got := c.Annotations(context.Background(), track.ID); len(got) != 3 {
		t.Fatalf("raw credit or role attribution lost: %+v", got)
	}
	if refs, err := c.ArtistRecordings(context.Background(), "Engineer"); err != nil || len(refs) != 0 {
		t.Fatalf("engineer-only credit returned as performer: %+v %v", refs, err)
	}
	if refs, err := c.ArtistRecordings(context.Background(), "Pianist"); err != nil || len(refs) != 1 {
		t.Fatalf("performer lookup lost recording: %+v %v", refs, err)
	}
	for _, tc := range []struct{ artist, tags, want string }{
		{"AC/DC; literal", `{"PERFORMER_NAME":"AC/DC; literal","RECORDING ENGINEER":"Engineer"}`, "AC/DC; literal"},
		{"Performer;Engineer;Unlabelled", `{"PERFORMER_NAME":"Performer","RECORDING ENGINEER":"Engineer"}`, "Performer;Engineer;Unlabelled"},
		{"Performer;Engineer", `{"PERFORMER_NAME":["Performer","Engineer"],"RECORDING ENGINEER":"Engineer"}`, "Performer;Engineer"},
		{"Performer;Engineer", `{"ARTIST":"Performer;Engineer"}`, "Performer;Engineer"},
	} {
		if got := displayArtist(tc.artist, annotations(json.RawMessage(tc.tags))); got != tc.want {
			t.Errorf("%q became %q, want %q", tc.artist, got, tc.want)
		}
	}
}
