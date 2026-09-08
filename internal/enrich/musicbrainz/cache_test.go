package musicbrainz

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
)

func TestAllMusicBrainzEndpointsUseWeekCache(t *testing.T) {
	fixtures := map[string]string{
		"/genres":  `<a href="/genre/g">Genre</a>`,
		"/genre/g": `<html></html>`, "/genre/g/aliases": `<html></html>`,
		"/ws/2/recording": `{"recordings":[]}`, "/ws/2/artist": `{"artists":[]}`, "/ws/2/release-group": `{"release-groups":[]}`,
		"/ws/2/recording/r": `{"id":"r","relations":[]}`, "/ws/2/work/w": `{"id":"w","relations":[]}`,
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = fmt.Fprint(w, fixtures[r.URL.Path]) }))
	defer server.Close()
	c := newClient(t, server.URL, time.Nanosecond)
	for path := range fixtures {
		for range 2 {
			if _, err := c.knowledgeGet(context.Background(), path, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	if int(calls.Load()) != len(fixtures) {
		t.Fatal("warm reads fetched again")
	}
	if _, err := c.db.Exec("UPDATE mb_cache SET fetched_at=?", time.Now().Add(-musicBrainzTTL+time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	for path := range fixtures {
		if _, err := c.knowledgeGet(context.Background(), path, true); err != nil {
			t.Fatal(err)
		}
	}
	if int(calls.Load()) != len(fixtures) {
		t.Fatal("fresh empty/positive data was bypassed")
	}
	if _, err := c.db.Exec("UPDATE mb_cache SET fetched_at=?", time.Now().Add(-musicBrainzTTL).Unix()); err != nil {
		t.Fatal(err)
	}
	for path := range fixtures {
		if _, err := c.knowledgeGet(context.Background(), path, false); err != nil {
			t.Fatal(err)
		}
	}
	if int(calls.Load()) != 2*len(fixtures) {
		t.Fatal("week-old data was not refreshed")
	}
}

func TestEnrichmentRawCachePersistenceAndPolicyReinterpretation(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprint(w, `{"recordings":[{"id":"r","score":90,"title":"曲","artist-credit":[{"name":"Artist"}]}]}`)
	}))
	defer server.Close()
	cfg := Config{UserAgent: "test", MirrorURL: server.URL, CachePath: filepath.Join(t.TempDir(), "cache.sqlite"), Interval: time.Nanosecond}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ref := core.TrackRef{Artist: "Artist", Title: "曲"}
	for range 2 {
		if got := c.one(context.Background(), ref); !got.Matched {
			t.Fatal(got)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	cfg.MinScore = 95
	c, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := c.one(context.Background(), ref); got.Matched {
		t.Fatal("cached projection bypassed new match policy")
	}
	if _, ok := c.CachedRecording(ref); !ok || calls.Load() != 1 {
		t.Fatal("reopened cache performed network access")
	}
	if err := c.ClearCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.CachedRecording(ref); ok || calls.Load() != 1 {
		t.Fatal("cleared cache-only read fetched")
	}
}

func TestMetadataCacheOriginAndQueryIdentity(t *testing.T) {
	if metadataKey("https://a", "/x?a=1&b=2", "mb") != metadataKey("https://a", "/x?b=2&a=1", "mb") {
		t.Fatal("parameter order fragmented cache")
	}
	for _, other := range []string{metadataKey("https://b", "/x?a=1&b=2", "mb"), metadataKey("https://a", "/x?a=1&b=2", "other"), metadataKey("https://a", "/x?a=1&b=3", "mb")} {
		if other == metadataKey("https://a", "/x?a=1&b=2", "mb") {
			t.Fatal("cache identity collision")
		}
	}
}

func TestCacheCoalescesAndClearDiscardsInflightWrite(t *testing.T) {
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		_, _ = fmt.Fprint(w, `{"recordings":[]}`)
	}))
	defer server.Close()
	c := newClient(t, server.URL, time.Nanosecond)
	var wg sync.WaitGroup
	wg.Go(func() { _, _ = c.knowledgeGet(context.Background(), "/test", false) })
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.knowledgeGet(ctx, "/test", false); !errors.Is(err, context.Canceled) {
		t.Fatal("queued cancellation ignored")
	}
	if err := c.ClearCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if e := c.readCache(context.Background(), metadataKey(c.base, "/test", "knowledge-v1:")); e.body != "" {
		t.Fatal("in-flight response refilled cleared cache")
	}
	for range 20 {
		wg.Go(func() {
			if _, err := c.knowledgeGet(context.Background(), "/test", false); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 2 {
		t.Fatalf("expected two owner requests, got %d", calls.Load())
	}
}

func TestInvalidResponsesNeverPoisonCache(t *testing.T) {
	for _, body := range []string{`{`, `{"error":"outage"}`, `{"message":"outage"}`, `null`, `{}`, `{"recordings":null}`} {
		t.Run(body, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = fmt.Fprint(w, body) }))
			defer server.Close()
			c := newClient(t, server.URL, time.Nanosecond)
			for range 2 {
				if _, err := c.knowledgeGet(context.Background(), "/ws/2/recording", false); err == nil {
					t.Fatal("invalid response accepted")
				}
			}
			if calls.Load() != 2 {
				t.Fatal("failure cached as empty success")
			}
		})
	}
}

func TestMemoryCacheBoundAndOfflineBehavior(t *testing.T) {
	c := &Client{}
	for i := range memoryCacheEntries + 10 {
		c.writeCache(context.Background(), fmt.Sprint(i), []byte(`{"recordings":[]}`), 0)
	}
	if len(c.memory) != memoryCacheEntries {
		t.Fatal("unbounded memory cache")
	}
	if err := c.ClearCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.memory) != 0 {
		t.Fatal("memory cache not cleared")
	}
}

func TestStaleMusicBrainzOutageDoesNotRenewTimestamp(t *testing.T) {
	var calls atomic.Int32
	var offline atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if offline.Load() {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = fmt.Fprint(w, `{"recordings":[]}`)
	}))
	defer server.Close()
	c := newClient(t, server.URL, time.Nanosecond)
	path := "/ws/2/recording?query=artist%3A%22Artist%22"
	if _, err := c.knowledgeGet(context.Background(), path, false); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour).Unix()
	if _, err := c.db.Exec("UPDATE mb_cache SET fetched_at=?", old); err != nil {
		t.Fatal(err)
	}
	offline.Store(true)
	for range 2 {
		if _, err := c.knowledgeGet(context.Background(), path, false); err != nil {
			t.Fatal("stale outage data lost", err)
		}
	}
	entry := c.readCache(context.Background(), metadataKey(c.base, path, "knowledge-v1:"))
	if entry.fetched != old || calls.Load() != 3 {
		t.Fatal("stale data was renewed or refresh skipped")
	}
}
