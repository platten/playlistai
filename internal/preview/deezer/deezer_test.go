package deezer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func newServer(t *testing.T, handler http.HandlerFunc) (*Provider, *int32) {
	t.Helper()
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqs, 1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return New(Config{BaseURL: srv.URL}), &reqs
}

func TestPreviewURLMatch(t *testing.T) {
	t.Parallel()
	p, _ := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"preview":"https://cdn.deezer/preview1.mp3"}]}`))
	})

	url, ok, err := p.PreviewURL(context.Background(), core.TrackRef{ID: "a", Artist: "Justice", Title: "Genesis"}, "https://bundled/x.mp3")
	if err != nil || !ok || url != "https://cdn.deezer/preview1.mp3" {
		t.Fatalf("got %q ok=%v err=%v", url, ok, err)
	}
}

func TestPreviewURLMissFallsBackToBundled(t *testing.T) {
	t.Parallel()
	p, _ := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	})

	url, ok, err := p.PreviewURL(context.Background(), core.TrackRef{ID: "a", Artist: "X", Title: "Y"}, "https://bundled/x.mp3")
	if err != nil || !ok || url != "https://bundled/x.mp3" {
		t.Fatalf("got %q ok=%v err=%v", url, ok, err)
	}
}

func TestPreviewURLMissNoBundled(t *testing.T) {
	t.Parallel()
	p, _ := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	})

	url, ok, err := p.PreviewURL(context.Background(), core.TrackRef{ID: "a", Artist: "X", Title: "Y"}, "")
	if err != nil || ok || url != "" {
		t.Fatalf("clean miss expected, got %q ok=%v err=%v", url, ok, err)
	}
}

func TestServerErrorFallsBackToBundled(t *testing.T) {
	t.Parallel()
	p, _ := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	url, ok, err := p.PreviewURL(context.Background(), core.TrackRef{ID: "a", Artist: "X", Title: "Y"}, "https://bundled/x.mp3")
	if err != nil || !ok || url != "https://bundled/x.mp3" {
		t.Fatalf("errors should not break a bundled fallback: %q ok=%v err=%v", url, ok, err)
	}
}

func TestServerErrorNoBundledReturnsErr(t *testing.T) {
	t.Parallel()
	p, _ := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, ok, err := p.PreviewURL(context.Background(), core.TrackRef{ID: "a", Artist: "X", Title: "Y"}, "")
	if err == nil || ok {
		t.Fatalf("expected an error with no fallback, ok=%v err=%v", ok, err)
	}
}

func TestEmptyRefSkipsRequestAndUsesBundled(t *testing.T) {
	t.Parallel()
	p, reqs := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	})

	url, ok, err := p.PreviewURL(context.Background(), core.TrackRef{ID: "a"}, "https://bundled/x.mp3")
	if err != nil || !ok || url != "https://bundled/x.mp3" {
		t.Fatalf("got %q ok=%v err=%v", url, ok, err)
	}
	if atomic.LoadInt32(reqs) != 0 {
		t.Fatalf("a ref with no artist/title should never hit the network, got %d requests", *reqs)
	}
}

func TestCacheAvoidsSecondRequest(t *testing.T) {
	t.Parallel()
	p, reqs := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"preview":"https://cdn.deezer/p.mp3"}]}`))
	})

	ref := core.TrackRef{ID: "a", Artist: "X", Title: "Y"}
	for i := 0; i < 2; i++ {
		url, ok, err := p.PreviewURL(context.Background(), ref, "")
		if err != nil || !ok || url != "https://cdn.deezer/p.mp3" {
			t.Fatalf("call %d: got %q ok=%v err=%v", i, url, ok, err)
		}
	}
	if got := atomic.LoadInt32(reqs); got != 1 {
		t.Fatalf("expected exactly one HTTP request, got %d", got)
	}
}

func TestQueryEncoding(t *testing.T) {
	t.Parallel()
	var gotQuery string
	p, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		_, _ = w.Write([]byte(`{"data":[]}`))
	})

	_, _, _ = p.PreviewURL(context.Background(), core.TrackRef{ID: "a", Artist: `Sly & the Family "Stone"`, Title: "Thank You"}, "")
	want := `artist:"Sly & the Family 'Stone'" track:"Thank You"`
	if gotQuery != want {
		t.Fatalf("query = %q, want %q", gotQuery, want)
	}
}

func TestName(t *testing.T) {
	t.Parallel()
	if New(Config{}).Name() != "deezer" {
		t.Fatal("unexpected name")
	}
}
