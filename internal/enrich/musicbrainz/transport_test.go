package musicbrainz

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAPIRequestSpacingAcrossClientsAndRedirects(t *testing.T) {
	first, err := New(Config{UserAgent: "test", Interval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(Config{UserAgent: "test", MirrorURL: "https://www.musicbrainz.org", Interval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	if first.interval != time.Second || second.interval != time.Second || first.limiter != second.limiter {
		t.Fatal("production minimum or shared limiter bypassed")
	}
	var starts []time.Time
	transport := transportFunc(func(r *http.Request) (*http.Response, error) {
		starts = append(starts, time.Now()) // Shared dispatch slot serializes this.
		response := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}
		if r.URL.Path == "/redirect" {
			response.StatusCode = 302
			response.Header.Set("Location", "/ws/2/artist")
		}
		return response, nil
	})
	first.hc.Transport.(*limitedTransport).base = transport
	second.hc.Transport.(*limitedTransport).base = transport
	var wg sync.WaitGroup
	for i, c := range []*Client{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := "/ws/2/recording"
			if i == 0 {
				path = "/redirect"
			}
			response, err := c.hc.Get(c.base + path)
			if err != nil {
				t.Error(err)
				return
			}
			_ = response.Body.Close()
		}()
	}
	wg.Wait()
	if len(starts) != 3 {
		t.Fatalf("dispatches=%d", len(starts))
	}
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < time.Second {
			t.Fatalf("requests started too close: %s", starts[i].Sub(starts[i-1]))
		}
	}
}

func TestQueuedRequestCancellationDoesNotDispatch(t *testing.T) {
	c := newClient(t, "http://127.0.0.1:1", time.Millisecond)
	if err := c.limiter.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.limiter.release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.base, nil)
	start := time.Now()
	if _, err := c.hc.Do(req); err == nil || time.Since(start) > time.Second {
		t.Fatal("queued cancellation did not finish promptly")
	}
}
