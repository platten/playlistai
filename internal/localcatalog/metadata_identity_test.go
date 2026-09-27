package localcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/enrich/musicbrainz"
	"github.com/platten/playlistai/internal/librarypack"
)

func TestRecordingMetadataForwardsOnlyCanonicalRecordingArtistIDs(t *testing.T) {
	const first = "12345678-1234-1234-1234-123456789abc"
	const second = "22345678-1234-1234-1234-123456789abc"
	for _, test := range []struct {
		name string
		raw  string
		want []string
	}{
		{"canonical arrays deduplicate and sort", fmt.Sprintf(`{"MUSICBRAINZ_ARTISTID":[%q,%q,%q]}`, second, strings.ToUpper(first), first), []string{first, second}},
		// Installed discovery packs store multiple UUIDs in one typed value.
		{"packed semicolon list", fmt.Sprintf(`{"MUSICBRAINZ_ARTISTID":%q}`, second+"; "+strings.ToUpper(first)), []string{first, second}},
		{"malformed list rejected whole", fmt.Sprintf(`{"MUSICBRAINZ_ARTISTID":%q}`, first+";not-an-id"), nil},
		{"empty component rejected", fmt.Sprintf(`{"MUSICBRAINZ_ARTISTID":%q}`, first+";"), nil},
		{"other separators not guessed", fmt.Sprintf(`{"MUSICBRAINZ_ARTISTID":%q}`, first+","+second), nil},
		{"album artist and free text excluded", fmt.Sprintf(`{"MUSICBRAINZ_ALBUMARTISTID":%q,"ARTIST":%q}`, first, second), nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			local, manager := openTestCatalog(t, []librarypack.Track{{ID: "track", Artist: "AC/DC; literal", Title: "Recording", RawTags: json.RawMessage(test.raw)}}, nil)
			defer manager.Close()
			defer local.Close()
			catalog := &CompositeCatalog{base: testBase{}, local: local, mode: ModeCombined}
			metadata, ok, err := catalog.LibraryRecordingMetadata(context.Background(), local.NamespacedID("track"))
			if err != nil || !ok || !reflect.DeepEqual(metadata.ArtistIDs, test.want) {
				t.Fatalf("IDs=%v want=%v found=%v err=%v", metadata.ArtistIDs, test.want, ok, err)
			}
			if metadata.Ref.Artist != "AC/DC; literal" {
				t.Fatalf("free-text artist changed: %q", metadata.Ref.Artist)
			}
		})
	}
}

func TestPackedCompositeArtistMetadataReachesVerifierWithoutRelaxingIdentity(t *testing.T) {
	const recordingID = "12345678-1234-1234-1234-123456789abc"
	const artistID = "22345678-1234-1234-1234-123456789abc"
	const orchestraID = "32345678-1234-1234-1234-123456789abc"
	const otherID = "42345678-1234-1234-1234-123456789abc"
	for _, test := range []struct {
		name string
	}{
		{"matching composite"}, {"recording conflict"}, {"artist conflict"}, {"title version conflict"}, {"ISRC conflict"}, {"album artist only"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tag := "MUSICBRAINZ_ARTISTID"
			if test.name == "album artist only" {
				tag = "MUSICBRAINZ_ALBUMARTISTID"
			}
			raw, err := json.Marshal(map[string]string{tag: artistID + ";" + orchestraID})
			if err != nil {
				t.Fatal(err)
			}
			local, manager := openTestCatalog(t, []librarypack.Track{{ID: "track", Artist: "Artist;Orchestra", Title: "Symphony", MusicBrainzRecording: recordingID, ISRC: "USABC1200001", RawTags: raw}}, nil)
			defer manager.Close()
			defer local.Close()
			catalog := &CompositeCatalog{base: testBase{}, local: local, mode: ModeCombined}
			metadata, ok, err := catalog.LibraryRecordingMetadata(context.Background(), local.NamespacedID("track"))
			if err != nil || !ok {
				t.Fatal(ok, err)
			}
			id, creditedArtist, title, isrc := recordingID, artistID, "Symphony", "USABC1200001"
			switch test.name {
			case "recording conflict":
				id = otherID
			case "artist conflict":
				creditedArtist = otherID
			case "title version conflict":
				title = "Symphony (live)"
			case "ISRC conflict":
				isrc = "USABC1200002"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/ws/2/recording/"+recordingID {
					t.Errorf("unexpected identity fallback request: %s", r.URL)
					http.NotFound(w, r)
					return
				}
				_, _ = fmt.Fprintf(w, `{"id":%q,"title":%q,"isrcs":[%q],"first-release-date":"2001","artist-credit":[{"name":"Artist","artist":{"id":%q,"name":"Artist"}}]}`, id, title, isrc, creditedArtist)
			}))
			defer server.Close()
			verifier, err := musicbrainz.New(musicbrainz.Config{UserAgent: "offline-recording-identity-test", MirrorURL: server.URL, Interval: time.Nanosecond})
			if err != nil {
				t.Fatal(err)
			}
			defer verifier.Close()
			verified, err := verifier.VerifyRecording(context.Background(), metadata, nil)
			if test.name == "matching composite" {
				if err != nil || len(verified.Claims) != 1 || verified.Claims[0].Kind != "original_release_date" || verified.Claims[0].RecordingID != recordingID {
					t.Fatalf("corroborated recording facts missing: %+v err=%v", verified.Claims, err)
				}
			} else if test.name == "ISRC conflict" {
				if err != nil || verified.Ref != metadata.Ref || verified.RecordingID != metadata.RecordingID || verified.IdentityStatus != core.ResolutionAmbiguous || verified.Matched || len(verified.Claims) != 0 {
					t.Fatalf("recording ISRC conflict was not quarantined: %+v err=%v", verified, err)
				}
			} else if err == nil || len(verified.Claims) != 0 {
				t.Fatalf("identity conflict accepted: %+v err=%v", verified.Claims, err)
			}
		})
	}
}

func TestRecordingMetadataKeepsWholeRecordingDuration(t *testing.T) {
	local, manager := openTestCatalog(t, []librarypack.Track{{ID: "track", Artist: "Artist", Title: "Song", MusicBrainzRecording: "12345678-1234-1234-1234-123456789abc", DurationMilliseconds: 576093, DurationReliable: true, DurationProvenance: "stream"}}, nil)
	defer manager.Close()
	defer local.Close()
	catalog := &CompositeCatalog{base: testBase{}, local: local, mode: ModeCombined}
	metadata, ok, err := catalog.LibraryRecordingMetadata(context.Background(), local.NamespacedID("track"))
	if err != nil || !ok || !metadata.FullRecordingDuration.Valid() || metadata.FullRecordingDuration.Milliseconds != 576093 {
		t.Fatalf("metadata=%+v ok=%v err=%v", metadata, ok, err)
	}
}
