package musicbrainz

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/deezerhttp"
	"github.com/platten/playlistai/internal/httpretry"
)

var virtualEndpointID atomic.Uint64

// Every bubble owns its limiter channels and timestamps. These loopback URLs
// are identities only: the in-process transport never opens a listening socket.
func virtualClientConfig(t *testing.T, cachePath string) Config {
	t.Helper()
	host := fmt.Sprintf("localhost:%d", 20000+virtualEndpointID.Add(1))
	base := "http://" + host
	t.Cleanup(func() {
		applicationLimiters.Delete(host)
		discogsThrottles.Delete(base)
	})
	return Config{UserAgent: "PlaylistAI fixture", MirrorURL: base, DeezerURL: base, DiscogsURL: base, CachePath: cachePath, Interval: time.Second}
}

// Preserve both production retry wrappers. Deezer loopback requests bypass its
// process-wide throttle, exactly as they do in the real httptest server fixture.
func virtualSeedTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *atomic.Int32) {
	t.Helper()
	c, err := New(virtualClientConfig(t, filepath.Join(t.TempDir(), "metadata.sqlite")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	var calls atomic.Int32
	transport := transportFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		response := httptest.NewRecorder()
		handler(response, r)
		result := response.Result()
		result.Request = r
		return result, nil
	})
	c.hc.Transport = &limitedTransport{client: c, base: transport}
	c.deezerClient = deezerhttp.Client(&http.Client{Timeout: 8 * time.Second, Transport: transport})
	return c, &calls
}

// Retry jitter is positive. Check every exponential minimum rather than only
// total elapsed time; changing clocks must not silently remove backoff attempts.
func assertVirtualRetryDelays(t *testing.T, starts []time.Time) {
	t.Helper()
	if len(starts) == 0 || len(starts)%httpretry.MaxAttempts != 0 {
		t.Fatalf("incomplete retry groups: %d attempts", len(starts))
	}
	for i := range starts {
		attempt := i % httpretry.MaxAttempts
		if attempt > 0 && starts[i].Sub(starts[i-1]) < time.Second<<(attempt-1) {
			t.Fatalf("retry %d skipped backoff: %s", i, starts[i].Sub(starts[i-1]))
		}
	}
}
