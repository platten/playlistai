package deezerhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
}

func TestClientsShareOneBudgetWithoutChangingCaller(t *testing.T) {
	source := &http.Client{Timeout: 8 * time.Second}
	a, b := Client(source), Client(&http.Client{})
	if Interval != 2*time.Second || a.Transport.(*throttledTransport).limiter != b.Transport.(*throttledTransport).limiter {
		t.Fatal("clients do not share the two-second budget")
	}
	if source.Transport != nil || a.Timeout != source.Timeout {
		t.Fatal("caller settings changed")
	}
	if Client(a).Transport != a.Transport {
		t.Fatal("double wrapping adds a second throttle")
	}
}

func TestConcurrentDispatchesNeverBurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLimiter(Interval)
		var starts []time.Time
		base := roundTripper(func(*http.Request) (*http.Response, error) {
			starts = append(starts, time.Now())
			return response("ok"), nil
		})
		var wg sync.WaitGroup
		for range 5 {
			wg.Go(func() {
				req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.deezer.com/search", nil)
				resp, err := l.roundTrip(req, base)
				if err != nil {
					t.Error(err)
					return
				}
				_ = resp.Body.Close()
			})
		}
		wg.Wait()
		for i := 1; i < len(starts); i++ {
			if starts[i].Sub(starts[i-1]) < 2*time.Second {
				t.Fatal("concurrent request burst")
			}
		}
	})
}

func TestCanceledWaitDoesNotDispatchOrReserveFutureSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLimiter(Interval)
		calls := 0
		base := roundTripper(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("fixture network error") })
		request := func(ctx context.Context) error {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.deezer.com/search", nil)
			_, err := l.roundTrip(req, base)
			return err
		}
		started := time.Now()
		_ = request(context.Background())
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if err := request(ctx); !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
			t.Fatal("canceled waiter sent a request")
		}
		_ = request(context.Background())
		if calls != 2 || time.Since(started) != 2*time.Second {
			t.Fatal("failed requests bypassed throttle or cancellation reserved a slot")
		}
	})
}

func TestRedirectsAndPlaybackUseThrottledMemoryDownload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var starts []time.Time
		base := roundTripper(func(req *http.Request) (*http.Response, error) {
			starts = append(starts, time.Now())
			if req.Header.Get("Cache-Control") != "no-store" {
				t.Error("preview request may be cached")
			}
			if req.URL.Path == "/redirect" {
				r := response("")
				r.StatusCode = 302
				r.Header.Set("Location", "https://cdn.dzcdn.net/preview.mp3")
				return r, nil
			}
			return response("synthetic"), nil
		})
		client := &http.Client{Transport: &throttledTransport{base: base, limiter: newLimiter(Interval)}}
		data, err := playbackURL(context.Background(), "https://cdn.dzcdn.net/redirect", client)
		if err != nil || data != "data:audio/mpeg;base64,c3ludGhldGlj" {
			t.Fatalf("playback still needs remote browser requests: %q %v", data, err)
		}
		_, err = playbackURL(context.Background(), "https://cdn.dzcdn.net/preview.mp3", client)
		if err != nil || len(starts) != 3 {
			t.Fatalf("requests=%d %v", len(starts), err)
		}
		for i := 1; i < len(starts); i++ {
			if starts[i].Sub(starts[i-1]) != 2*time.Second {
				t.Fatal("redirect/playback bypassed throttle")
			}
		}
	})
}

func TestOtherProvidersAndLookalikesAreNotDeezer(t *testing.T) {
	for address, want := range map[string]bool{"https://API.DEEZER.COM./search": true, "https://cdn.dzcdn.net/p": true, "https://deezer.com.evil.invalid/p": false, "https://notdeezer.com/p": false, "https://spotify.invalid/p": false} {
		u, _ := url.Parse(address)
		if IsDeezer(u) != want {
			t.Fatalf("incorrect host classification %s", address)
		}
	}
	address := "https://spotify.invalid/p"
	got, err := PlaybackURL(context.Background(), address)
	if err != nil || got != address {
		t.Fatal("changed other provider playback")
	}
}

func TestPlaybackRejectsOversizedAudioAndForeignRedirect(t *testing.T) {
	for _, redirect := range []bool{false, true} {
		base := roundTripper(func(req *http.Request) (*http.Response, error) {
			if req.URL.Hostname() != "cdn.dzcdn.net" {
				t.Fatal("followed foreign redirect")
			}
			r := response("")
			if redirect {
				r.StatusCode = 302
				r.Header.Set("Location", "https://other.invalid/preview")
			} else {
				r.ContentLength = 9 << 20
			}
			return r, nil
		})
		client := &http.Client{Transport: &throttledTransport{base: base, limiter: newLimiter(Interval)}}
		if _, err := playbackURL(context.Background(), "https://cdn.dzcdn.net/preview", client); err == nil {
			t.Fatal("unsafe preview accepted")
		}
	}
}
