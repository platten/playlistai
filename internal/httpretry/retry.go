// Package httpretry retries transient failures of bodyless, idempotent reads.
// Callers retain their client timeouts, redirect policy and provider throttles.
package httpretry

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const MaxAttempts = 4
const maxWait = 30 * time.Second

type attemptCheckKey struct{}

// WithAttemptCheck lets a request owner charge every dispatch, including retries
// and redirects, to its existing budget. A rejected attempt never reaches next.
func WithAttemptCheck(ctx context.Context, check func() error) context.Context {
	return context.WithValue(ctx, attemptCheckKey{}, check)
}

// RetryAfter accepts both HTTP forms, saturating large values without overflow.
func RetryAfter(header string, now time.Time) time.Duration {
	value := strings.TrimSpace(header)
	if seconds, err := strconv.ParseUint(value, 10, 64); err == nil || errors.Is(err, strconv.ErrRange) {
		const largest = time.Duration(1<<63 - 1)
		if seconds > uint64(largest/time.Second) {
			return largest
		}
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		return max(0, at.Sub(now))
	}
	return 0
}

func retryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// RoundTrip invokes next separately for each attempt so its rate limiter still
// governs every dispatch. Long Retry-After values return the last response
// immediately rather than spending the entire generation budget on one provider.
func RoundTrip(req *http.Request, next func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	attempts := 1
	if (req.Method == http.MethodGet || req.Method == http.MethodHead) && (req.Body == nil || req.Body == http.NoBody) {
		attempts = MaxAttempts
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		if check, ok := req.Context().Value(attemptCheckKey{}).(func() error); ok {
			if err := check(); err != nil {
				return nil, err
			}
		}
		resp, err := next(req.Clone(req.Context()))
		if ctxErr := req.Context().Err(); ctxErr != nil {
			closeResponse(resp)
			return nil, ctxErr
		}
		if attempt == attempts-1 || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || err == nil && (resp == nil || !retryableStatus(resp.StatusCode)) {
			return resp, err
		}
		base := time.Second << attempt
		// Positive jitter prevents synchronized clients retrying together without
		// reducing either the exponential minimum or the server's Retry-After.
		delay := base + time.Duration(rand.Int64N(int64(base/4)+1)) //nolint:gosec // retry scheduling, not security randomness
		if resp != nil {
			delay = max(delay, RetryAfter(resp.Header.Get("Retry-After"), time.Now()))
		}
		if deadline, ok := req.Context().Deadline(); delay > maxWait || ok && time.Until(deadline) <= delay {
			return resp, err
		}
		closeResponse(resp)
		timer := time.NewTimer(delay)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		case <-timer.C:
		}
	}
	panic("unreachable retry loop")
}

func closeResponse(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}

type transport struct{ base http.RoundTripper }

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	return RoundTrip(req, t.base.RoundTrip)
}

// Client wraps a copy, preserving timeouts and redirect checks. Provider clients
// with custom throttles call RoundTrip directly instead of nesting retry layers.
func Client(source *http.Client) *http.Client {
	client := *source
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if _, ok := base.(*transport); !ok {
		client.Transport = &transport{base: base}
	}
	return &client
}
