package librarypack

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestArtistMBIDsOnlyAcceptsRecordingCredits(t *testing.T) {
	const a = "11111111-1111-4111-8111-111111111111"
	const b = "22222222-2222-4222-8222-222222222222"
	for _, raw := range []string{
		`{"MUSICBRAINZ_ARTISTID":"` + a + `; ` + b + `; ` + a + `"}`,
		`{"musicbrainz artistid":["` + a + `","` + b + `","not-an-id"]}`,
	} {
		if got := ArtistMBIDs(json.RawMessage(raw)); !reflect.DeepEqual(got, []string{a, b}) {
			t.Fatalf("%s: %+v", raw, got)
		}
	}
	for _, raw := range []string{`{"ARTIST":"` + a + `"}`, `{"musicbrainz_albumartistid":"` + a + `"}`, `{"musicbrainz_artistid":12}`, `invalid`} {
		if got := ArtistMBIDs(json.RawMessage(raw)); len(got) != 0 {
			t.Fatalf("invented performer: %s %+v", raw, got)
		}
	}
	var closed *Generation
	if _, err := closed.ArtistRecordingsByMBID(context.Background(), a, 1); err == nil {
		t.Fatal("closed generation accepted")
	}
}
