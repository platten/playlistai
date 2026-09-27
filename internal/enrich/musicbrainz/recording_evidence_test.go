package musicbrainz

import (
	"context"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

func TestRecordingMetadataPreservesOriginalDateAndTagFacets(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		recordingDate string
		groupDate     string
	}{
		{"later compilation", "1971-02-03", "1998-06-01"},
		{"missing group date", "1971-02-03", ""},
		{"unknown recording date", "", "1998-06-01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := mbRelease{ID: "release-1", Title: "Compilation", Date: "2007-01-02"}
			release.ReleaseGroup.FirstReleaseDate = tc.groupDate
			recording := mbRecording{
				ID: acousticTestID, Title: "Song", Score: 100, FirstReleaseDate: tc.recordingDate,
				ArtistCredit: []mbArtistCredit{{Name: "Artist"}},
				Genres:       []mbTag{{Name: "jazz", Count: 2}}, Tags: []mbTag{{Name: "favorite", Count: 4}},
				Releases: []mbRelease{release},
			}
			recording.ArtistCredit[0].Artist.ID = contextArtistID
			server := newMBServer(t, map[string]mbRecording{"song": recording})
			client := newClient(t, server.URL, time.Nanosecond)
			ref := core.TrackRef{ID: "local", Artist: "Artist", Title: "Song"}
			catalog := &dynamicCatalogFixture{Catalog: fakes.NewCatalog(1)}
			var snapshot core.KnowledgeSnapshot
			if err := client.addDynamicKnowledgeRecording(context.Background(), recording, catalog, &snapshot); err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Tracks) != 1 {
				t.Fatalf("dynamic tracks = %d", len(snapshot.Tracks))
			}
			for name, got := range map[string]core.EnrichedTrack{
				"search":  client.one(context.Background(), ref),
				"dynamic": snapshot.Tracks[0],
			} {
				if got.OriginalReleaseDate != tc.recordingDate || got.ReleaseEditionDate != release.Date {
					t.Errorf("%s date semantics: original=%q edition=%q", name, got.OriginalReleaseDate, got.ReleaseEditionDate)
				}
				if len(got.GenreTags) != 2 || got.GenreTags[0].Facet != "genre" || got.GenreTags[1].Facet != "tag" || got.GenreTags[0].EntityID != recording.ID || got.GenreTags[1].EntityID != recording.ID {
					t.Errorf("%s lost recording tag attribution: %+v", name, got.GenreTags)
				}
			}
		})
	}
}
