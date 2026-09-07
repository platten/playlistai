package deezer

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/deezerhttp"
)

type recordingTransport struct{ starts []time.Time }

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.starts = append(r.starts, time.Now())
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: req,
		Body: io.NopCloser(strings.NewReader(`{"data":[{"id":1,"title":"Song","artist":{"name":"Artist"},"preview":"https://cdn.dzcdn.net/preview.mp3"}]}`))}, nil
}

func TestPlaybackAndAnalysisProvidersShareRequestThrottle(t *testing.T) {
	transport := &recordingTransport{}
	client := &http.Client{Transport: transport}
	playback, analysis := New(Config{HTTPClient: client}), New(Config{HTTPClient: client})
	ref := core.TrackRef{ID: "shared-budget", Artist: "Artist", Title: "Song"}
	ctx := context.Background()
	for range 2 { // The second metadata lookup must use the cache.
		if _, ok, err := playback.PreviewURL(ctx, ref, ""); err != nil || !ok {
			t.Fatalf("playback: ok=%v err=%v", ok, err)
		}
	}
	if result, err := analysis.ResolveAudioPreview(ctx, ref, core.EnrichedTrack{}); err != nil || result.Identity.Status != core.ResolutionResolved {
		t.Fatalf("analysis: %+v %v", result, err)
	}
	resp, err := deezerhttp.Client(client).Get("https://cdn.dzcdn.net/preview.mp3")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if len(transport.starts) != 3 {
		t.Fatalf("got %d requests, want 3", len(transport.starts))
	}
	for i := 1; i < len(transport.starts); i++ {
		if gap := transport.starts[i].Sub(transport.starts[i-1]); gap < 2*time.Second {
			t.Fatalf("requests %d and %d separated by only %s", i-1, i, gap)
		}
	}
}

func TestAnalysisRequiresCorroboratedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     core.ResolutionStatus
	}{
		{"wrong first result", `{"data":[{"id":1,"title":"Song","artist":{"name":"Other"},"preview":"https://cdn.dzcdn.net/wrong"},{"id":2,"title":"Song","artist":{"name":"Artist"},"preview":"https://cdn.dzcdn.net/right"}]}`, core.ResolutionResolved},
		{"version mismatch", `{"data":[{"id":1,"title":"Song","title_version":"Live","artist":{"name":"Artist"},"preview":"https://cdn.dzcdn.net/live"}]}`, core.ResolutionUnresolved},
		{"ambiguous", `{"data":[{"id":1,"title":"Song","artist":{"name":"Artist"}},{"id":2,"title":"Song","artist":{"name":"Artist"}}]}`, core.ResolutionAmbiguous},
		{"missing", `{"data":[]}`, core.ResolutionUnresolved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("limit") != "5" {
					t.Error("analysis reused first-hit lookup")
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			p := New(Config{BaseURL: srv.URL})
			result, err := p.ResolveAudioPreview(context.Background(), core.TrackRef{ID: "s", Artist: "Artist", Title: "Song"}, core.EnrichedTrack{})
			if err != nil || result.Identity.Status != tc.status {
				t.Fatalf("%+v %v", result, err)
			}
			if tc.status != core.ResolutionResolved && result.URL != "" {
				t.Fatal("unverified audio returned")
			}
			if tc.status == core.ResolutionResolved && result.Identity.ProviderID != "2" {
				t.Fatal("first result accepted")
			}
		})
	}
}

func TestAnalysisPrefersVerifiedISRCAndPreservesUnicode(t *testing.T) {
	ref := core.TrackRef{ID: "s", Artist: "宇多田ヒカル", Title: "光"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/track/isrc:JPAA00000001" {
			t.Errorf("path=%s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":3,"title":"光","isrc":"JPAA00000001","artist":{"name":"宇多田ヒカル"},"preview":"https://cdn.dzcdn.net/right"}`))
	}))
	defer srv.Close()
	p := New(Config{BaseURL: srv.URL})
	result, err := p.ResolveAudioPreview(context.Background(), ref, core.EnrichedTrack{Ref: ref, Matched: true, IdentityStatus: core.ResolutionResolved, ISRC: "JP-AA0-00-00001", RecordingID: "recording"})
	if err != nil || result.Identity.Status != core.ResolutionResolved || result.Identity.RecordingID != "recording" {
		t.Fatalf("%+v %v", result, err)
	}
}
