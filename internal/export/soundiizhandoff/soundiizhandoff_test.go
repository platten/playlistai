package soundiizhandoff

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func newExporter(t *testing.T, h http.HandlerFunc) *Exporter {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	e := New()
	e.endpoint = srv.URL + "/go/import-playlist"
	return e
}

func sampleReq() ports.ExportRequest {
	return ports.ExportRequest{
		Name: "Justice walk",
		Tracks: []core.EnrichedTrack{
			{Ref: core.TrackRef{Artist: "Justice", Title: "Genesis"}, AllArtists: []string{"Justice"}},
			{Ref: core.TrackRef{Artist: "SebastiAn", Title: "Rerun"}},
		},
	}
}

func TestHandoffSuccess(t *testing.T) {
	t.Parallel()
	var body request
	e := newExporter(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_ = json.NewEncoder(w).Encode(response{
			Status:   "success",
			ShareURL: "https://soundiiz.com/go/import-playlist/abc123def",
			NbTracks: 2,
		})
	})

	res, err := e.Export(context.Background(), sampleReq(), nil)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if res.Location != "https://soundiiz.com/go/import-playlist/abc123def" || res.Count != 2 {
		t.Fatalf("result = %+v", res)
	}

	if body.SourceName != "Playlist AI" || body.Title != "Justice walk" {
		t.Fatalf("request meta = %+v", body)
	}
	if len(body.Tracklist) != 2 || body.Tracklist[0].Title != "Genesis" ||
		len(body.Tracklist[0].Artists) != 1 || body.Tracklist[1].Artists[0] != "SebastiAn" {
		t.Fatalf("tracklist = %+v", body.Tracklist)
	}
}

func TestHandoffRejectsForeignShareURL(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{
		"https://evil.com/go/import-playlist/x",
		"http://soundiiz.com/go/import-playlist/x", // not https
		"https://soundiiz.com/elsewhere/x",
		"https://soundiiz.com.evil.com/go/import-playlist/x",
		"",
	} {
		e := newExporter(t, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(response{Status: "success", ShareURL: bad})
		})
		if _, err := e.Export(context.Background(), sampleReq(), nil); err == nil {
			t.Fatalf("accepted a bad share URL: %q", bad)
		}
	}
}

func TestHandoffStatusError(t *testing.T) {
	t.Parallel()
	e := newExporter(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(response{Status: "error", Message: "playlist too large"})
	})
	_, err := e.Export(context.Background(), sampleReq(), nil)
	if err == nil || err.Error() != "playlist too large" {
		t.Fatalf("err = %v", err)
	}
}

func TestHandoffDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()
	e := newExporter(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, "https://soundiiz.com/go/import-playlist/redirected", http.StatusFound)
	})
	if _, err := e.Export(context.Background(), sampleReq(), nil); err == nil {
		t.Fatal("a redirect should be an error, not followed")
	}
}

func TestValidateShareURL(t *testing.T) {
	t.Parallel()
	if err := validateShareURL("https://soundiiz.com/go/import-playlist/ok"); err != nil {
		t.Fatalf("valid URL rejected: %v", err)
	}
	if err := validateShareURL("https://soundiiz.com/go/import-playlist/"); err != nil {
		t.Fatalf("prefix-only URL rejected: %v", err)
	}
}
