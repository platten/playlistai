package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
)

const verificationReleaseTrackID = "21ddf4c1-4106-49b4-b7f9-688f907c2c58"

func TestVerifyRecordingCorroboratesTitleThroughExactReleaseTrack(t *testing.T) {
	for _, tc := range []struct {
		name     string
		accepted bool
		calls    int
	}{
		{"exact proof", true, 2}, {"exact title", true, 1}, {"invalid embedded ISRC", true, 2},
		{"wrong release", false, 2}, {"wrong recording", false, 2}, {"wrong release track", false, 2},
		{"ambiguous release track", false, 2}, {"missing release track", false, 1}, {"missing release", false, 1},
		{"wrong artist", false, 1}, {"conflicting recording ISRC", false, 1}, {"conflicting release ISRC", false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			track := verificationTrack()
			track.ReleaseID, track.ReleaseTrackID = contextAlbumID, verificationReleaseTrackID
			track.ISRC = "USAAA1200001"
			if tc.name == "missing release track" {
				track.ReleaseTrackID = ""
			}
			if tc.name == "missing release" {
				track.ReleaseID = ""
			}
			if tc.name == "invalid embedded ISRC" {
				track.ISRC = "090266306622"
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch r.URL.Path {
				case "/ws/2/recording/" + acousticTestID:
					title, artist, artistID, isrc := "Canonical movement title", "Artist", contextArtistID, "USAAA1200001"
					if tc.name == "exact title" {
						title = track.Ref.Title
					}
					if tc.name == "wrong artist" {
						artist, artistID = "Different performer", contextAlbumID
					}
					if tc.name == "conflicting recording ISRC" {
						isrc = "USAAA1200002"
					}
					if !strings.Contains(r.URL.Query().Get("inc"), "isrcs") {
						t.Error("recording identity did not request ISRCs")
					}
					_, _ = fmt.Fprintf(w, `{"id":%q,"title":%q,"isrcs":[%q],"artist-credit":[{"name":%q,"artist":{"id":%q}}],"relations":[{"type":"vocal","artist":{"id":%q}}]}`, acousticTestID, title, isrc, artist, artistID, contextArtistID)
				case "/ws/2/release/" + contextAlbumID:
					releaseID, recordingID, releaseTrackID, isrc := contextAlbumID, acousticTestID, verificationReleaseTrackID, "USAAA1200001"
					if tc.name == "wrong release" {
						releaseID = contextArtistID
					}
					if tc.name == "wrong recording" {
						recordingID = contextArtistID
					}
					if tc.name == "wrong release track" {
						releaseTrackID = contextArtistID
					}
					if tc.name == "conflicting release ISRC" {
						isrc = "USAAA1200002"
					}
					entry := fmt.Sprintf(`{"id":%q,"title":"Edition title","recording":{"id":%q,"isrcs":[%q]}}`, releaseTrackID, recordingID, isrc)
					if tc.name == "ambiguous release track" {
						entry += "," + entry
					}
					_, _ = fmt.Fprintf(w, `{"id":%q,"title":"Edition","media":[{"tracks":[%s]}]}`, releaseID, entry)
				default:
					t.Errorf("unexpected request: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := newClient(t, server.URL, time.Nanosecond)
			got, err := client.VerifyRecording(context.Background(), track, nil)
			if tc.name == "conflicting recording ISRC" || tc.name == "conflicting release ISRC" || tc.name == "wrong recording" {
				if err != nil || got.Ref != track.Ref || got.RecordingID != track.RecordingID || got.IdentityStatus != core.ResolutionAmbiguous || got.Matched || len(got.Claims) != 0 || calls.Load() != int32(tc.calls) {
					t.Fatalf("recording ISRC conflict was not quarantined: %+v err=%v calls=%d", got, err, calls.Load())
				}
				return
			}
			if (err == nil) != tc.accepted || len(got.Claims) > 0 != tc.accepted || calls.Load() != int32(tc.calls) {
				t.Fatalf("accepted=%v claims=%+v err=%v calls=%d", tc.accepted, got.Claims, err, calls.Load())
			}
			if got.Ref != track.Ref || got.RecordingID != track.RecordingID {
				t.Fatal("local identity changed")
			}
			if tc.accepted && tc.name != "exact title" {
				proof := got.Claims[0]
				if proof.Kind != "recording_identity" || proof.Method != "release_track_link" || !strings.Contains(proof.Source.URL, "/release/"+contextAlbumID) || proof.Source.Revision == "" || proof.RetrievedAt == "" || proof.Locator != "media[0].tracks[0].recording.id" {
					t.Fatalf("identity proof not preserved: %+v", proof)
				}
				again, err := client.VerifyRecording(context.Background(), got, nil)
				if err != nil || len(again.Claims) != len(got.Claims) || calls.Load() != 2 {
					t.Fatalf("exact proof cache/dedup failed: %+v %v calls=%d", again, err, calls.Load())
				}
			}
		})
	}
}

func TestReleaseTrackCorroborationRespectsBudgetAndCancellation(t *testing.T) {
	for _, scenario := range []string{"parent canceled", "inherited budget", "release unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if strings.HasPrefix(r.URL.Path, "/ws/2/recording/") {
					_, _ = fmt.Fprintf(w, `{"id":%q,"title":"Different canonical title","artist-credit":[{"name":"Artist"}]}`, acousticTestID)
					return
				}
				if scenario == "parent canceled" {
					cancel()
				}
				http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			}))
			defer server.Close()
			client := newClient(t, server.URL, time.Nanosecond)
			track := verificationTrack()
			track.ReleaseID, track.ReleaseTrackID = contextAlbumID, verificationReleaseTrackID
			if scenario == "inherited budget" {
				ctx = context.WithValue(ctx, contextRequestLimitKey{}, 1)
			}
			got, err := client.VerifyRecording(ctx, track, nil)
			if err == nil || len(got.Claims) != 0 {
				t.Fatalf("unproven identity accepted: %+v %v", got, err)
			}
			expected := int32(2)
			if scenario == "inherited budget" {
				expected = 1
			}
			if calls.Load() != expected {
				t.Fatalf("corroboration exceeded one additional dispatch: calls=%d", calls.Load())
			}
			if scenario == "parent canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

func TestRecordingISRCConflictUsesOnlyCanonicalIdentifiers(t *testing.T) {
	for _, tc := range []struct {
		local    string
		provider []string
		conflict bool
	}{
		{"090266306622", []string{"USAAA1200001"}, false},
		{"USAAA1200001", []string{"090266306622"}, false},
		{"USAAA1200001", []string{"USAAA1200002"}, true},
		{"USAAA1200001", []string{"USAAA1200002", "USAAA1200001"}, false},
	} {
		if got := conflictingRecordingISRCs(core.EnrichedTrack{ISRC: tc.local}, tc.provider); got != tc.conflict {
			t.Errorf("local=%q provider=%v conflict=%v", tc.local, tc.provider, got)
		}
	}
}

func TestRecordingDurationPrefersExactDeclaredReleaseTrack(t *testing.T) {
	for _, scenario := range []string{"agree", "edition title", "mismatch", "foreign release", "foreign track", "foreign recording", "missing recording", "malformed recording", "conflicting ISRC", "missing length", "invalid length", "nested length only", "duplicate track", "unavailable", "deadline"} {
		t.Run(scenario, func(t *testing.T) {
			track := verificationTrack()
			track.Matched = true
			if scenario == "edition title" {
				track.Ref.Title = "Edition title"
			}
			track.ISRC = "USAAA1200001"
			track.ReleaseID = contextAlbumID
			track.ReleaseTrackID = verificationReleaseTrackID
			track.FullRecordingDuration = &core.RecordingDuration{Milliseconds: 253859, Source: "local:stream", RecordingID: track.RecordingID}
			var releaseCalls atomic.Int32
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/ws/2/recording/") {
					_, _ = fmt.Fprintf(w, `{"id":%q,"title":"Song","length":321000,"isrcs":["USAAA1200001"],"artist-credit":[{"name":"Artist"}]}`, track.RecordingID)
					return
				}
				releaseCalls.Add(1)
				if scenario == "unavailable" {
					http.Error(w, "missing", http.StatusNotFound)
					return
				}
				if scenario == "deadline" {
					cancel()
					<-r.Context().Done()
					return
				}
				releaseID, trackID, recordingID, isrc := track.ReleaseID, track.ReleaseTrackID, track.RecordingID, track.ISRC
				switch scenario {
				case "foreign release":
					releaseID = acousticTestID
				case "foreign track":
					trackID = acousticTestID
				case "foreign recording":
					recordingID = contextAlbumID
				case "missing recording":
					recordingID = ""
				case "malformed recording":
					recordingID = "not-a-recording"
				case "conflicting ISRC":
					isrc = "USAAA1200002"
				}
				row := map[string]any{"id": trackID, "length": 253859, "recording": map[string]any{"id": recordingID, "isrcs": []string{isrc}, "length": 321000}}
				switch scenario {
				case "mismatch":
					row["length"] = 380346
				case "invalid length":
					row["length"] = -1
				case "missing length", "nested length only":
					delete(row, "length")
				}
				tracks := []any{row}
				if scenario == "duplicate track" {
					tracks = append(tracks, row)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": releaseID, "title": "Release", "media": []any{map[string]any{"tracks": tracks}}})
			}))
			defer server.Close()
			client := newClient(t, server.URL, time.Nanosecond)
			defer client.Close()
			got, err := client.VerifyRecording(ctx, track, nil)
			if releaseCalls.Load() != 1 {
				t.Fatalf("release requests=%d", releaseCalls.Load())
			}
			if scenario == "deadline" {
				if err == nil {
					t.Fatal("cancellation ignored")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Ref != track.Ref || got.RecordingID != track.RecordingID {
				t.Fatal("identity retagged")
			}
			if scenario == "mismatch" || scenario == "foreign recording" || scenario == "conflicting ISRC" {
				if got.IdentityStatus != core.ResolutionAmbiguous || got.Matched || len(got.Claims) != 0 {
					t.Fatal("exact duration conflict lost", got)
				}
				return
			}
			if got.IdentityStatus != core.ResolutionResolved {
				t.Fatal("aggregate length overruled edition or unknown evidence", got)
			}
			if scenario == "agree" {
				if len(got.Claims) != 1 || got.Claims[0].Method != "release_track_link" || got.Claims[0].Source.Revision == "" {
					t.Fatal("exact release provenance missing", got.Claims)
				}
			}
		})
	}
}
