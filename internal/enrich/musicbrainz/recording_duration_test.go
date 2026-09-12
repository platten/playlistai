package musicbrainz

import (
	"context"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
)

func TestFullRecordingDurationRequiresUniqueMatchedRecordingAndPositiveLength(t *testing.T) {
	for _, tc := range []struct {
		name      string
		length    *int64
		artist    string
		id        string
		duplicate bool
		want      bool
	}{
		{"matched", durationMS(250123), "Justice", "recording-1", false, true},
		{"unknown", nil, "Justice", "recording-1", false, false},
		{"zero", durationMS(0), "Justice", "recording-1", false, false},
		{"negative", durationMS(-10), "Justice", "recording-1", false, false},
		{"wrong artist", durationMS(250123), "Other", "recording-1", false, false},
		{"no identity", durationMS(250123), "Justice", "", false, false},
		{"ambiguous recording", durationMS(250123), "Justice", "recording-1", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := mbRecording{ID: tc.id, Title: "Genesis", Score: 100, Length: tc.length, ArtistCredit: []mbArtistCredit{{Name: tc.artist}}}
			hits := map[string]mbRecording{"Genesis": rec}
			if tc.duplicate {
				other := rec
				other.ID = "recording-2"
				hits["Justice"] = other
			}
			server := newMBServer(t, hits)
			client := newClient(t, server.URL, time.Millisecond)
			ref := core.TrackRef{ID: "catalog-1", Title: "Genesis", Artist: "Justice"}
			got := client.query(context.Background(), ref)
			if (got.FullRecordingDuration != nil) != tc.want {
				t.Fatalf("unexpected duration: %+v", got)
			}
			if tc.want {
				d := got.FullRecordingDuration
				if d.Milliseconds != 250123 || d.Source != "musicbrainz" || d.RecordingID != got.RecordingID {
					t.Fatalf("duration provenance lost: %+v", d)
				}
				cached, ok := client.CachedRecording(ref)
				if !ok || cached.FullRecordingDuration == nil || *cached.FullRecordingDuration != *d {
					t.Fatal("cached recording lost duration")
				}
			}
		})
	}
}

func durationMS(n int64) *int64 { return &n }
