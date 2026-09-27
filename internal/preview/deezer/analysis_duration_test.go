package deezer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestAnalysisRejectsConflictingFullRecordingDuration(t *testing.T) {
	const liveID = "d4606a1b-8c34-4477-8887-bf0ea8c8ea41"
	ref := core.TrackRef{ID: "pack:review:live-recording", Artist: "Artist", Title: "Song", RecordingIdentity: "musicbrainz:" + liveID}
	for _, tc := range []struct {
		name           string
		providerLength int64
		knownLength    int64
		want           core.ResolutionStatus
	}{
		// The version is in the catalog release, not the title. Exact title
		// equality cannot authenticate a 164s studio performance as 222.533s live.
		{"studio substituted for live", 164, 222533, core.ResolutionUnresolved},
		{"rounded matching duration", 223, 222533, core.ResolutionResolved},
		{"within duration tolerance", 226, 222533, core.ResolutionResolved},
		{"outside duration tolerance", 227, 222533, core.ResolutionUnresolved},
		{"provider duration absent", 0, 222533, core.ResolutionResolved},
		{"provider duration invalid", -1, 222533, core.ResolutionResolved},
		{"provider duration oversized", 86401, 222533, core.ResolutionResolved},
		{"catalog duration absent", 164, 0, core.ResolutionResolved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprintf(w, `{"data":[{"id":2428039,"title":"Song","duration":%d,"artist":{"name":"Artist"},"preview":"https://cdn.dzcdn.net/example"}]}`, tc.providerLength)
			}))
			defer srv.Close()
			known := core.EnrichedTrack{Ref: ref, Matched: true, IdentityStatus: core.ResolutionResolved, RecordingID: liveID, Album: "Live release",
				FullRecordingDuration: &core.RecordingDuration{Milliseconds: tc.knownLength, Source: "catalog:stream", RecordingID: ref.RecordingIdentity}}
			got, err := New(Config{BaseURL: srv.URL}).ResolveAudioPreview(context.Background(), ref, known)
			if err != nil || got.Identity.Status != tc.want {
				t.Fatalf("status=%s err=%v; want %s", got.Identity.Status, err, tc.want)
			}
			if tc.want != core.ResolutionResolved && got.URL != "" {
				t.Fatal("conflicting recording yielded an analysis URL")
			}
			if tc.want == core.ResolutionResolved {
				wantDuration := tc.providerLength * 1000
				if tc.providerLength <= 0 || tc.providerLength > 86400 {
					wantDuration = 0
				}
				if !got.Identity.CurrentPolicy() || got.Identity.FullRecordingMilliseconds != wantDuration {
					t.Fatal("current policy or provider full-duration provenance lost")
				}
			}
		})
	}
}

func TestAnalysisAmbiguousCatalogIdentityDoesNotFallBackToName(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	ref := core.TrackRef{ID: "s", Artist: "Artist", Title: "Song"}
	known := core.EnrichedTrack{Ref: ref, IdentityStatus: core.ResolutionAmbiguous}
	got, err := New(Config{BaseURL: srv.URL}).ResolveAudioPreview(context.Background(), ref, known)
	if err != nil || calls != 0 || got.URL != "" || got.Identity.Status != core.ResolutionAmbiguous {
		t.Fatalf("catalog conflict triggered name lookup: calls=%d got=%+v err=%v", calls, got, err)
	}
}

func TestAnalysisDurationCannotBorrowAnotherRecording(t *testing.T) {
	track := core.EnrichedTrack{RecordingID: "first", Ref: core.TrackRef{ID: "catalog-row", RecordingIdentity: "musicbrainz:first"}}
	for _, id := range []string{"first", "musicbrainz:first", "catalog-row"} {
		if !previewDurationBelongsTo(track, id) {
			t.Fatalf("matching duration identity %q ignored", id)
		}
	}
	if previewDurationBelongsTo(track, "second") {
		t.Fatal("duration from an unrelated recording accepted")
	}
}

func TestAnalysisISRCMatchDoesNotOverrideDurationConflict(t *testing.T) {
	ref := core.TrackRef{ID: "s", Artist: "Artist", Title: "Song"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/track/isrc:USIR29000064" {
			t.Errorf("unexpected route %q", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"id":1,"title":"Song","duration":164,"isrc":"USIR29000064","artist":{"name":"Artist"},"preview":"https://cdn.dzcdn.net/example"}`)
	}))
	defer srv.Close()
	known := core.EnrichedTrack{Ref: ref, Matched: true, IdentityStatus: core.ResolutionResolved, RecordingID: "recording", ISRC: "USIR29000064",
		FullRecordingDuration: &core.RecordingDuration{Milliseconds: 222533, Source: "catalog", RecordingID: "recording"}}
	got, err := New(Config{BaseURL: srv.URL}).ResolveAudioPreview(context.Background(), ref, known)
	if err != nil || got.Identity.Status != core.ResolutionUnresolved || got.URL != "" {
		t.Fatalf("ISRC conflict escaped full-duration guard: %+v err=%v", got, err)
	}
}
