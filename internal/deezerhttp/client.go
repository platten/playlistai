// Package deezerhttp owns the application-wide Deezer request budget.
package deezerhttp

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/httpretry"
)

const Interval = 2 * time.Second

var shared = newLimiter(Interval)

type limiter struct {
	gate     chan struct{}
	last     time.Time // protected by gate, including the transport dispatch
	interval time.Duration
}

func newLimiter(interval time.Duration) *limiter {
	return &limiter{gate: make(chan struct{}, 1), interval: interval}
}

func (l *limiter) roundTrip(req *http.Request, transport http.RoundTripper) (*http.Response, error) {
	ctx := req.Context()
	select {
	case l.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-l.gate }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if delay := time.Until(l.last.Add(l.interval)); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.last = time.Now()
	// Keep dispatch serialized until response headers. Queued requests cannot
	// reserve old time slots and later bunch together after a scheduling delay.
	return transport.RoundTrip(req)
}

type throttledTransport struct {
	base    http.RoundTripper
	limiter *limiter
}

func (t *throttledTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return httpretry.RoundTrip(req, t.roundTripOnce)
}

func (t *throttledTransport) roundTripOnce(req *http.Request) (*http.Response, error) {
	if !IsDeezer(req.URL) {
		return t.base.RoundTrip(req)
	}
	return t.limiter.roundTrip(req, t.base)
}

// IsDeezer includes API and preview-CDN hosts, including redirected requests.
// Local fixture servers and other providers do not consume Deezer's budget.
func IsDeezer(u *url.URL) bool {
	if u == nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	return host == "deezer.com" || strings.HasSuffix(host, ".deezer.com") || host == "dzcdn.net" || strings.HasSuffix(host, ".dzcdn.net")
}

// Client copies the caller's settings and wraps each actual HTTP dispatch, so
// redirects and separate provider/client instances cannot bypass the limit.
func Client(source *http.Client) *http.Client {
	client := *source
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if _, ok := base.(*throttledTransport); !ok {
		client.Transport = &throttledTransport{base: base, limiter: shared}
	}
	return &client
}

// PlaybackURL prevents the browser from making unthrottled CDN/range requests.
// Only Deezer previews are materialized, in bounded transient memory. Playback
// releases the data URL when its audio element is cleared; nothing is cached on disk.
func PlaybackURL(ctx context.Context, address string) (string, error) {
	return playbackURL(ctx, address, &http.Client{Timeout: 30 * time.Second})
}

func playbackURL(ctx context.Context, address string, source *http.Client) (string, error) {
	u, err := url.Parse(address)
	if err != nil {
		return "", err
	}
	// Catalog and provider metadata are data, not permission to make the
	// desktop WebView access arbitrary URLs, local files or private services.
	validHTTPS := func(u *url.URL) bool { return u.Scheme == "https" && u.User == nil && u.Port() == "" && u.Opaque == "" }
	host := strings.ToLower(u.Hostname())
	if validHTTPS(u) && (host == "p.scdn.co" || host == "podz-content.spotifycdn.com") {
		return address, nil
	}
	allowed := func(u *url.URL) bool {
		host := strings.ToLower(u.Hostname())
		return validHTTPS(u) && (host == "dzcdn.net" || strings.HasSuffix(host, ".dzcdn.net"))
	}
	if !allowed(u) {
		return "", fmt.Errorf("deezer: invalid preview URL")
	}
	client := Client(source)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !allowed(req.URL) {
			return fmt.Errorf("deezer: refused preview redirect")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Cache-Control", "no-store")
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("deezer: preview download failed")
	}
	defer resp.Body.Close()
	const maxBytes = 8 << 20
	if resp.StatusCode != http.StatusOK || resp.ContentLength > maxBytes {
		return "", fmt.Errorf("deezer: unavailable or oversized preview")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	defer clear(raw)
	if err != nil || len(raw) == 0 || len(raw) > maxBytes {
		return "", fmt.Errorf("deezer: incomplete or oversized preview")
	}
	return "data:audio/mpeg;base64," + base64.StdEncoding.EncodeToString(raw), nil
}
