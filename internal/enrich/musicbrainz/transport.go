package musicbrainz

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/platten/playlistai/internal/httpretry"
)

// A speculative page yields its admission slot to queued required lookups.
// In-flight HTTP requests are never interrupted merely because priority changes.
type speculativeRequestKey struct{}

func (l *requestLimiter) signalLocked() {
	if l.changed != nil {
		close(l.changed)
	}
	l.changed = make(chan struct{})
}

func (l *requestLimiter) acquire(ctx context.Context) error {
	speculative, _ := ctx.Value(speculativeRequestKey{}).(bool)
	if !speculative {
		l.priorityMu.Lock()
		l.required++
		l.priorityMu.Unlock()
		defer func() {
			l.priorityMu.Lock()
			l.required--
			l.signalLocked()
			l.priorityMu.Unlock()
		}()
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		l.priorityMu.Lock()
		if l.changed == nil {
			l.changed = make(chan struct{})
		}
		changed := l.changed
		blocked := speculative && l.required > 0
		l.priorityMu.Unlock()
		if blocked {
			select {
			case <-changed:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		select {
		case l.gate <- struct{}{}:
			l.priorityMu.Lock()
			blocked = speculative && l.required > 0
			l.priorityMu.Unlock()
			if blocked {
				l.release()
				continue
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
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
	for {
		if err := waitMetadataBackoff(req.Context()); err != nil {
			return nil, err
		}
		if err := t.client.limiter.acquire(req.Context()); err != nil {
			return nil, err
		}
		if metadataBackoff(req.Context()) > 0 {
			t.client.limiter.release()
			continue
		}
		if err := t.client.waitForRequest(req.Context()); err != nil {
			t.client.limiter.release()
			return nil, err
		}
		speculative, _ := req.Context().Value(speculativeRequestKey{}).(bool)
		t.client.limiter.priorityMu.Lock()
		yield := speculative && t.client.limiter.required > 0
		t.client.limiter.priorityMu.Unlock()
		if yield {
			t.client.limiter.release()
			continue
		}
		break
	}
	defer t.client.limiter.release()
	// Charge only actual dispatches after priority admission, including retries.
	if limit, ok := req.Context().Value(metadataAttemptLimitKey{}).(int); ok {
		budget := req.Context().Value(knowledgeBudgetKey{}).(*knowledgeBudget)
		budget.mu.Lock()
		if budget.requests >= limit {
			budget.mu.Unlock()
			return nil, httpretry.Permanent(fmt.Errorf("metadata request budget exhausted"))
		}
		budget.requests++
		budget.mu.Unlock()
	}
	// Holding the slot through dispatch prevents delayed callers bunching up.
	// Redirects enter RoundTrip again and consume their own slot.
	t.client.limiter.last = time.Now()
	resp, err := t.base.RoundTrip(req)
	if resp != nil && (resp.StatusCode == 429 || resp.StatusCode == 503) {
		if budget, ok := req.Context().Value(knowledgeBudgetKey{}).(*knowledgeBudget); ok {
			namespace, _ := req.Context().Value(metadataNamespaceKey{}).(string)
			wait := 5 * time.Second
			if value := resp.Header.Get("Retry-After"); value != "" {
				wait = httpretry.RetryAfter(value, time.Now())
			}
			budget.mu.Lock()
			if budget.backoffs == nil {
				budget.backoffs = make(map[string]time.Time)
			}
			budget.backoffs[namespace] = time.Now().Add(wait)
			budget.mu.Unlock()
		}
	}
	return resp, err
}

// Recheck after transport admission: another queued request may have received
// Retry-After since metadataGet's cache and budget checks.
func metadataBackoff(ctx context.Context) time.Duration {
	budget, _ := ctx.Value(knowledgeBudgetKey{}).(*knowledgeBudget)
	if budget == nil {
		return 0
	}
	namespace, _ := ctx.Value(metadataNamespaceKey{}).(string)
	budget.mu.Lock()
	defer budget.mu.Unlock()
	return time.Until(budget.backoffs[namespace])
}

func waitMetadataBackoff(ctx context.Context) error {
	for {
		delay := metadataBackoff(ctx)
		if delay <= 0 {
			return ctx.Err()
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
