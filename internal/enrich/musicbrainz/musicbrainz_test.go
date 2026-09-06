package musicbrainz

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

type mbServer struct {
	*httptest.Server
	reqs   int32
	lastUA string
}

// newMBServer serves a canned recording search. `hits` maps a lowercased title
// substring to a recording; an unmatched query returns an empty list.
func newMBServer(t *testing.T, hits map[string]mbRecording) *mbServer {
	t.Helper()
	s := &mbServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&s.reqs, 1)
		s.lastUA = r.Header.Get("User-Agent")
		q := r.URL.Query().Get("query")
		var recs []mbRecording
		for key, rec := range hits {
			if containsFold(q, key) {
				recs = append(recs, rec)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"recordings": recs})
	}))
	t.Cleanup(s.Close)
	return s
}

func containsFold(s, sub string) bool {
	s, sub = toLower(s), toLower(sub)
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func newClient(t *testing.T, base string, interval time.Duration) *Client {
	t.Helper()
	c, err := New(Config{
		UserAgent: "PlaylistAI-test/1.0 ( https://example.com )",
		CachePath: filepath.Join(t.TempDir(), "mb.sqlite"),
		MirrorURL: base,
		Interval:  interval,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestEnrichMatchAndMiss(t *testing.T) {
	t.Parallel()
	srv := newMBServer(t, map[string]mbRecording{
		"genesis": {
			ID: "r1", Score: 100, Title: "Genesis",
			ISRCs:        []string{"FRV840700010", "FRV840700011"},
			ArtistCredit: []mbArtistCredit{{Name: "Justice"}},
			Releases:     []mbRelease{{ID: "release-1", Title: "Cross", Date: "2007-06-11"}},
		},
	})
	c := newClient(t, srv.URL, time.Millisecond)

	got, err := c.Enrich(context.Background(), []core.TrackRef{
		{ID: "a", Artist: "Justice", Title: "Genesis"},
		{ID: "b", Artist: "Nobody", Title: "Nothing At All"},
	}, &fakes.RecordingProgress{})
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}

	m := got[0]
	if !m.Matched || m.ISRC != "FRV840700010" || len(m.AllISRCs) != 2 {
		t.Fatalf("match: %+v", m)
	}
	if m.Album != "Cross" || m.Year != 2007 || len(m.AllArtists) != 1 {
		t.Fatalf("metadata: %+v", m)
	}
	if m.RecordingID != "r1" || m.ReleaseID != "release-1" || m.ReleaseEditionDate != "2007-06-11" || m.OriginalReleaseDate != "" {
		t.Fatalf("canonical identity or date semantics: %+v", m)
	}
	if m.Ref.ID != "a" {
		t.Fatalf("ref lost: %+v", m.Ref)
	}

	if got[1].Matched || got[1].ISRC != "" {
		t.Fatalf("unmatched track carried data: %+v", got[1])
	}
	if got[1].Ref.ID != "b" {
		t.Fatal("unmatched ref lost")
	}
}

func TestEnrichUsesUserAgentAndCache(t *testing.T) {
	t.Parallel()
	srv := newMBServer(t, map[string]mbRecording{
		"a": {ID: "r", Score: 90, Title: "A", ISRCs: []string{"X"}},
	})
	c := newClient(t, srv.URL, time.Millisecond)

	ref := []core.TrackRef{{ID: "1", Artist: "Band", Title: "A"}}
	if _, err := c.Enrich(context.Background(), ref, nil); err != nil {
		t.Fatal(err)
	}
	if srv.lastUA == "" || srv.lastUA == "Go-http-client/1.1" {
		t.Fatalf("User-Agent not set: %q", srv.lastUA)
	}
	first := atomic.LoadInt32(&srv.reqs)

	// second call for the same track hits the cache, no HTTP
	if _, err := c.Enrich(context.Background(), ref, nil); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&srv.reqs) != first {
		t.Fatalf("cache miss on repeat: %d then %d", first, atomic.LoadInt32(&srv.reqs))
	}
}

func TestRateLimitSpacing(t *testing.T) {
	t.Parallel()
	srv := newMBServer(t, map[string]mbRecording{
		"x": {ID: "r", Score: 100, Title: "x"},
	})
	c := newClient(t, srv.URL, 40*time.Millisecond)

	refs := []core.TrackRef{
		{ID: "1", Artist: "A", Title: "x1"},
		{ID: "2", Artist: "B", Title: "x2"},
		{ID: "3", Artist: "C", Title: "x3"},
	}
	start := time.Now()
	if _, err := c.Enrich(context.Background(), refs, nil); err != nil {
		t.Fatal(err)
	}
	// 3 live requests, spaced by ~40ms → at least ~80ms total
	if elapsed := time.Since(start); elapsed < 70*time.Millisecond {
		t.Fatalf("rate limiter too fast: %v for 3 requests", elapsed)
	}
}

func TestDefaultInterval(t *testing.T) {
	t.Parallel()
	c, err := New(Config{UserAgent: "x/1.0 (u)"})
	if err != nil {
		t.Fatal(err)
	}
	if c.interval != time.Second {
		t.Fatalf("default interval = %v", c.interval)
	}
	if c.minScore != 85 {
		t.Fatalf("default minScore = %d", c.minScore)
	}
}

func TestNewRejectsEmptyUserAgent(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{UserAgent: "   "}); err == nil {
		t.Fatal("expected an error for a blank User-Agent")
	}
}

func TestContextCancelReturnsPartial(t *testing.T) {
	t.Parallel()
	srv := newMBServer(t, map[string]mbRecording{"x": {ID: "r", Score: 100, Title: "x"}})
	c := newClient(t, srv.URL, 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	refs := []core.TrackRef{
		{ID: "1", Artist: "A", Title: "x1"},
		{ID: "2", Artist: "B", Title: "x2"},
		{ID: "3", Artist: "C", Title: "x3"},
	}
	got, err := c.Enrich(ctx, refs, nil)
	if err == nil {
		t.Fatal("expected a context error")
	}
	if len(got) >= len(refs) {
		t.Fatalf("expected a partial result, got %d", len(got))
	}
}
