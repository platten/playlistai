package musicbrainz

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
)

func TestCachedVerificationNeverFetchesAndRetainsExactEditionPolicy(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint("conflict=", conflict), func(t *testing.T) {
			var calls atomic.Int32
			length := 576093
			if conflict {
				length = 380346
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch r.URL.Path {
				case "/ws/2/recording/" + acousticTestID:
					_, _ = fmt.Fprintf(w, `{"id":%q,"title":"Song","length":380346,"artist-credit":[{"name":"Artist","artist":{"id":%q}}],"genres":[{"name":"jazz","count":3}]}`, acousticTestID, contextArtistID)
				case "/ws/2/release/" + contextAlbumID:
					_, _ = fmt.Fprintf(w, `{"id":%q,"title":"Edition","media":[{"tracks":[{"id":%q,"title":"Song","length":%d,"recording":{"id":%q}}]}]}`, contextAlbumID, verificationReleaseTrackID, length, acousticTestID)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
				}
			}))
			defer server.Close()
			client := newClient(t, server.URL, time.Nanosecond)
			track := verificationTrack()
			track.Matched = true
			track.ReleaseID, track.ReleaseTrackID = contextAlbumID, verificationReleaseTrackID
			if _, err := client.VerifyCachedRecording(context.Background(), track); !errors.Is(err, core.ErrUnavailable) || calls.Load() != 0 {
				t.Fatalf("cache miss fetched: calls=%d err=%v", calls.Load(), err)
			}
			if _, err := client.VerifyRecording(context.Background(), track, nil); err != nil || calls.Load() != 1 {
				t.Fatal("prime recording", err, calls.Load())
			}
			track.FullRecordingDuration = &core.RecordingDuration{Milliseconds: 576093, Source: "local:stream", RecordingID: track.RecordingID}
			got, err := client.VerifyCachedRecording(context.Background(), track)
			if err != nil || got.IdentityStatus != core.ResolutionResolved || calls.Load() != 1 || len(got.Claims) != 0 {
				t.Fatal("missing edition must remain unknown without fetching or extracting claims", got, err, calls.Load())
			}
			if _, err := client.VerifyRecording(context.Background(), track, nil); err != nil || calls.Load() != 2 {
				t.Fatal("prime exact edition", err, calls.Load())
			}
			got, err = client.VerifyCachedRecording(context.Background(), track)
			if err != nil || calls.Load() != 2 || (got.IdentityStatus == core.ResolutionAmbiguous) != conflict || got.Matched == conflict || len(got.Claims) != 0 {
				t.Fatal("cached edition policy changed", got, err, calls.Load())
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := client.VerifyCachedRecording(ctx, track); !errors.Is(err, context.Canceled) || calls.Load() != 2 {
				t.Fatal("cancellation ignored", err, calls.Load())
			}
		})
	}
}
