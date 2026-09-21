package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

func prefetchFixture(t testing.TB, delay time.Duration) (*Client, *candidateStream, *atomic.Int32) {
	t.Helper()
	c, err := New(Config{UserAgent: "test", MirrorURL: "http://127.0.0.1:12345", Interval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	calls := &atomic.Int32{}
	// Bypass provider spacing here to force out-of-order completions. Production
	// limiter spacing and priority have separate transport tests.
	c.hc = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		id := strings.TrimPrefix(r.URL.Query().Get("query"), "arid:")
		wait := delay
		if id == "a0" {
			wait += delay
		}
		select {
		case <-time.After(wait):
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		raw := fmt.Sprintf(`{"count":1,"recordings":[{"id":"r%s","title":"Song","artist-credit":[{"name":"Artist %s","artist":{"id":"%s"}}]}]}`, id, id, id)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(raw)), Request: r}, nil
	})}
	var tracks []fakes.CatalogTrack
	pool := core.GenreArtistPool{Genre: "ambient", Sources: []string{"fixture"}}
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("a%d", i)
		tracks = append(tracks, fakes.CatalogTrack{ID: id, Display: "Artist " + id + " - Song"})
		pool.Artists = append(pool.Artists, core.GenreArtist{ID: id, Name: "Artist " + id})
	}
	cat := &artistRecordingFixture{Catalog: fakes.NewCatalog(2, tracks...)}
	intent := core.MusicIntent{Seed: "42", Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "ambient", Influence: core.InfluencePositive}}}, Knowledge: &core.KnowledgeSnapshot{ArtistPools: []core.GenreArtistPool{pool}}}
	return c, c.OpenCandidates(intent, cat, cat).(*candidateStream), calls
}

func TestPrefetchOrderedReplayAndUnusedSnapshot(t *testing.T) {
	_, serial, _ := prefetchFixture(t, time.Millisecond)
	_, parallel, _ := prefetchFixture(t, time.Millisecond)
	parallel.StartPrefetch(context.Background())
	defer parallel.StopPrefetch()
	for range 8 {
		a, err := serial.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		b, err := parallel.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("completion order changed candidates: %+v %+v", a, b)
		}
	}
	parallel.StopPrefetch()
	// Provider URLs and fixed responses match; scheduling must not enter hashes.
	if !reflect.DeepEqual(serial.Snapshot(), parallel.Snapshot()) {
		t.Fatal("prefetch changed replay snapshot")
	}
	_, unused, _ := prefetchFixture(t, time.Millisecond)
	before, _ := json.Marshal(unused.Snapshot())
	unused.StartPrefetch(context.Background())
	<-unused.prefetch.ready
	unused.StopPrefetch()
	after, _ := json.Marshal(unused.Snapshot())
	if string(before) != string(after) {
		t.Fatal("unused plan changed snapshot")
	}
}

func TestPrefetchBoundAndCancellation(t *testing.T) {
	_, s, calls := prefetchFixture(t, time.Hour)
	before, _ := json.Marshal(s.Snapshot())
	s.StartPrefetch(context.Background())
	<-s.prefetch.ready
	s.prefetch.mu.Lock()
	count := len(s.prefetch.pages)
	s.prefetch.mu.Unlock()
	if count != 4 {
		t.Fatalf("outstanding pages=%d", count)
	}
	done := make(chan struct{})
	go func() { s.StopPrefetch(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stop did not join canceled requests")
	}
	s.StopPrefetch()
	if calls.Load() > 4 {
		t.Fatalf("too many speculative requests: %d", calls.Load())
	}
	after, _ := json.Marshal(s.Snapshot())
	if string(before) != string(after) {
		t.Fatal("canceled speculative pages changed snapshot")
	}
}

func TestRequiredRequestPrecedesQueuedSpeculation(t *testing.T) {
	limiter := &requestLimiter{gate: make(chan struct{}, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := limiter.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	order := make(chan string, 2)
	var wg sync.WaitGroup
	for _, required := range []bool{false, true} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			requestCtx := ctx
			name := "required"
			if !required {
				requestCtx = context.WithValue(ctx, speculativeRequestKey{}, true)
				name = "speculative"
			}
			if err := limiter.acquire(requestCtx); err != nil {
				t.Error(err)
				return
			}
			order <- name
			limiter.release()
		}()
	}
	for {
		limiter.priorityMu.Lock()
		queued := limiter.required
		limiter.priorityMu.Unlock()
		if queued > 0 {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(time.Millisecond)
	}
	limiter.release()
	wg.Wait()
	if got := <-order; got != "required" {
		t.Fatalf("first admission=%s", got)
	}
}

func BenchmarkDiscoveryPrefetch(b *testing.B) {
	for _, parallel := range []bool{false, true} {
		b.Run(fmt.Sprintf("parallel=%t", parallel), func(b *testing.B) {
			b.ReportAllocs()
			var firstChecked, total time.Duration
			for range b.N {
				c, s, calls := prefetchFixture(b, 2*time.Millisecond)
				c.interval = 2 * time.Millisecond
				c.limiter = &requestLimiter{gate: make(chan struct{}, 1)}
				c.hc.Transport = &limitedTransport{client: c, base: c.hc.Transport}
				started := time.Now()
				if parallel {
					s.StartPrefetch(context.Background())
				}
				for i := range 8 {
					if _, err := s.Next(context.Background()); err != nil {
						b.Fatal(err)
					}
					time.Sleep(2 * time.Millisecond) // fixed audio-assessment fixture
					if i == 0 {
						firstChecked += time.Since(started)
					}
				}
				s.StopPrefetch()
				total += time.Since(started)
				if calls.Load() != 8 {
					b.Fatalf("requests=%d", calls.Load())
				}
			}
			b.ReportMetric(float64(firstChecked.Microseconds())/float64(b.N), "first-checked-us/op")
			b.ReportMetric(float64(total.Microseconds())/float64(b.N), "playlist-us/op")
			b.ReportMetric(8, "requests/op")
		})
	}
}

func TestUnusedPrefetchErrorsStayOutOfSnapshot(t *testing.T) {
	c, s, _ := prefetchFixture(t, 0)
	c.hc.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 400, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	})
	before, _ := json.Marshal(s.Snapshot())
	s.StartPrefetch(context.Background())
	<-s.prefetch.ready
	s.prefetch.mu.Lock()
	pages := make([]*prefetchedPage, 0, len(s.prefetch.pages))
	for _, page := range s.prefetch.pages {
		pages = append(pages, page)
	}
	s.prefetch.mu.Unlock()
	for _, page := range pages {
		<-page.done
		if page.err == nil {
			t.Fatal("expected speculative failure")
		}
	}
	s.prefetch.mu.Lock()
	outstanding := len(s.prefetch.pages)
	s.prefetch.mu.Unlock()
	if outstanding != 4 {
		t.Fatalf("completed pages escaped bound: %d", outstanding)
	}
	s.StopPrefetch()
	after, _ := json.Marshal(s.Snapshot())
	if string(before) != string(after) {
		t.Fatal("unused failures changed snapshot")
	}
}

func TestPrefetchQueuedBackoffAndSharedRequestBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, s, _ := prefetchFixture(t, 0)
		c.limiter = &requestLimiter{gate: make(chan struct{}, 1)}
		c.hc.Transport = &limitedTransport{client: c, base: c.hc.Transport}
		ctx := context.Background()
		if err := c.limiter.acquire(ctx); err != nil {
			t.Fatal(err)
		}
		budget := &knowledgeBudget{requests: KnowledgeRequests - 2}
		ctx = context.WithValue(ctx, knowledgeBudgetKey{}, budget)
		s.StartPrefetch(ctx)
		synctest.Wait()
		started := time.Now()
		budget.mu.Lock()
		budget.backoffs = map[string]time.Time{"knowledge-v1:": started.Add(2 * time.Second)}
		budget.mu.Unlock()
		c.limiter.release()
		<-s.prefetch.ready
		s.prefetch.mu.Lock()
		tail := s.prefetch.tail
		s.prefetch.mu.Unlock()
		<-tail
		if elapsed := time.Since(started); elapsed < 2*time.Second {
			t.Fatalf("queued request bypassed backoff: %s", elapsed)
		}
		s.StopPrefetch()
		budget.mu.Lock()
		charged := budget.requests
		budget.mu.Unlock()
		if charged != KnowledgeRequests {
			t.Fatalf("shared budget requests=%d", charged)
		}
	})
}

func TestPrefetchedFailureIsReportedOnlyOnConsumption(t *testing.T) {
	c, s, _ := prefetchFixture(t, 0)
	c.hc.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 400, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	})
	s.StartPrefetch(context.Background())
	defer s.StopPrefetch()
	<-s.prefetch.ready
	if s.failures != 0 || len(s.snapshot.Notices) != 0 {
		t.Fatal("speculative failure changed candidate state")
	}
	if _, err := s.Next(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("consumed failure=%v", err)
	}
	if s.failures != 3 {
		t.Fatalf("consumed failures=%d", s.failures)
	}
}

func TestPrefetchContinuationWaitsForConsumedPageOffset(t *testing.T) {
	c, s, _ := prefetchFixture(t, 0)
	var mu sync.Mutex
	seen := map[string]bool{}
	c.hc.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		id := strings.TrimPrefix(r.URL.Query().Get("query"), "arid:")
		offset := r.URL.Query().Get("offset")
		mu.Lock()
		if offset != "" && !seen[id] {
			t.Error("continuation requested before preceding page")
		}
		if offset != "" && offset != "1" {
			t.Errorf("offset=%s", offset)
		}
		seen[id] = true
		mu.Unlock()
		raw := fmt.Sprintf(`{"count":2,"recordings":[{"id":"r%s","title":"Song","artist-credit":[{"name":"Artist %s","artist":{"id":"%s"}}]}]}`, id, id, id)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(raw)), Request: r}, nil
	})
	s.StartPrefetch(context.Background())
	defer s.StopPrefetch()
	count := 0
	for {
		_, err := s.Next(context.Background())
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
	}
	if count != 8 {
		t.Fatalf("duplicate continuation recordings accepted: %d", count)
	}
	for artist, pages := range s.recordingPages {
		if pages != 2 || s.recordingOffsets[artist] != 2 {
			t.Fatalf("artist %s pages=%d offset=%d", artist, pages, s.recordingOffsets[artist])
		}
	}
}

func TestRequiredLookupDuringSpeculativeSpacingGetsBudgetFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _, _ := prefetchFixture(t, 0)
		c.interval = time.Second
		c.limiter = &requestLimiter{gate: make(chan struct{}, 1), last: time.Now()}
		var order []string
		c.hc.Transport = &limitedTransport{client: c, base: transportFunc(func(r *http.Request) (*http.Response, error) {
			order = append(order, r.URL.Query().Get("query"))
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"recordings":[]}`)), Request: r}, nil
		})}
		budget := &knowledgeBudget{requests: KnowledgeRequests - 1}
		ctx := context.WithValue(context.Background(), knowledgeBudgetKey{}, budget)
		speculativeDone := make(chan error, 1)
		go func() {
			_, err := c.knowledgeGet(context.WithValue(ctx, speculativeRequestKey{}, true), recordingPagePath("speculative", 0), false)
			speculativeDone <- err
		}()
		synctest.Wait() // speculative request is waiting for its spacing timer
		requiredDone := make(chan error, 1)
		go func() { _, err := c.knowledgeGet(ctx, recordingPagePath("required", 0), false); requiredDone <- err }()
		if err := <-requiredDone; err != nil {
			t.Fatal(err)
		}
		if err := <-speculativeDone; err == nil || !strings.Contains(err.Error(), "budget exhausted") {
			t.Fatalf("speculative err=%v", err)
		}
		if !reflect.DeepEqual(order, []string{"arid:required"}) {
			t.Fatalf("dispatch order=%v", order)
		}
	})
}
