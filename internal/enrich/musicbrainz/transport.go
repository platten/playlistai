package musicbrainz

import (
	"context"
	"net/http"
	"time"

	"github.com/platten/playlistai/internal/httpretry"
)

func (l *requestLimiter) acquire(ctx context.Context) error {
	select {
	case l.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (l *requestLimiter) release() { <-l.gate }

func (c *Client) waitForRequest(ctx context.Context) error {
	if wait := c.interval - time.Since(c.limiter.last); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.limiter.last = time.Now()
	return nil
}

type limitedTransport struct {
	client *Client
	base   http.RoundTripper
}

func (t *limitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return httpretry.RoundTrip(req, t.roundTripOnce)
}

func (t *limitedTransport) roundTripOnce(req *http.Request) (*http.Response, error) {
	if err := t.client.limiter.acquire(req.Context()); err != nil {
		return nil, err
	}
	defer t.client.limiter.release()
	if err := t.client.waitForRequest(req.Context()); err != nil {
		return nil, err
	}
	// Holding the slot through dispatch prevents delayed callers bunching up.
	// Redirects enter RoundTrip again and consume their own slot.
	return t.base.RoundTrip(req)
}
