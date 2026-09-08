package httpretry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }
func response(code int) (*http.Response, *trackedBody) {
	b := &trackedBody{Reader: strings.NewReader("fixture")}
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: b}, b
}

func TestExponentialRetryWithJitterClosesOnlyDiscardedResponses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example/lookup", nil)
		req.Header.Set("User-Agent", "fixture")
		var starts []time.Time
		var bodies []*trackedBody
		resp, err := RoundTrip(req, func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("User-Agent") != "fixture" {
				t.Error("request headers lost")
			}
			starts = append(starts, time.Now())
			code := http.StatusServiceUnavailable
			if len(starts) == MaxAttempts {
				code = http.StatusOK
			}
			resp, b := response(code)
			bodies = append(bodies, b)
			return resp, nil
		})
		if err != nil || resp.StatusCode != http.StatusOK || len(starts) != MaxAttempts {
			t.Fatalf("response=%+v attempts=%d err=%v", resp, len(starts), err)
		}
		for i := 1; i < len(starts); i++ {
			base := time.Second << (i - 1)
			delay := starts[i].Sub(starts[i-1])
			if delay < base || delay > base+base/4 {
				t.Fatalf("retry %d: %s", i, delay)
			}
		}
		for i, b := range bodies {
			if b.closed != (i < len(bodies)-1) {
				t.Fatal("discarded response leaked or final response closed")
			}
		}
		_ = resp.Body.Close()
	})
}

func TestNetworkFailuresRetryAndPermanentFailuresDoNot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example/a", nil)
		calls := 0
		resp, err := RoundTrip(req, func(*http.Request) (*http.Response, error) {
			calls++
			if calls < 3 {
				return nil, io.ErrUnexpectedEOF
			}
			r, _ := response(http.StatusOK)
			return r, nil
		})
		if err != nil || calls != 3 {
			t.Fatalf("network retry: %d %v", calls, err)
		}
		_ = resp.Body.Close()
		for _, code := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusNotImplemented} {
			calls = 0
			resp, err = RoundTrip(req, func(*http.Request) (*http.Response, error) { calls++; r, _ := response(code); return r, nil })
			if err != nil || calls != 1 || resp.StatusCode != code {
				t.Fatal("permanent status retried")
			}
			_ = resp.Body.Close()
		}
		req.Method = http.MethodPost
		calls = 0
		resp, err = RoundTrip(req, func(*http.Request) (*http.Response, error) {
			calls++
			r, _ := response(http.StatusServiceUnavailable)
			return r, nil
		})
		if err != nil || calls != 1 {
			t.Fatal("non-idempotent operation retried")
		}
		_ = resp.Body.Close()
	})
}

func TestRetryAfterAndAttemptLimit(t *testing.T) {
	for _, header := range []string{"3", "date", "3600", "18446744073709551615", "184467440737095516150"} {
		t.Run(header, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				value := header
				if header == "date" {
					value = start.Add(4 * time.Second).UTC().Format(http.TimeFormat)
				}
				req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example/a", nil)
				calls := 0
				resp, err := RoundTrip(req, func(*http.Request) (*http.Response, error) {
					calls++
					r, _ := response(http.StatusTooManyRequests)
					r.Header.Set("Retry-After", value)
					return r, nil
				})
				if err != nil || resp.StatusCode != http.StatusTooManyRequests {
					t.Fatal(err)
				}
				if header != "3" && header != "date" {
					if calls != 1 || time.Since(start) != 0 {
						t.Fatal("long server cooldown spent request budget")
					}
				} else if calls != MaxAttempts || time.Since(start) < 9*time.Second {
					t.Fatalf("Retry-After ignored: %d %s", calls, time.Since(start))
				}
				_ = resp.Body.Close()
			})
		})
	}
}

func TestCancellationAndAttemptBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.example/a", nil)
		go func() { time.Sleep(100 * time.Millisecond); cancel() }()
		calls := 0
		var body *trackedBody
		_, err := RoundTrip(req, func(*http.Request) (*http.Response, error) {
			calls++
			r, b := response(http.StatusServiceUnavailable)
			body = b
			return r, nil
		})
		if !errors.Is(err, context.Canceled) || calls != 1 || !body.closed {
			t.Fatalf("cancellation: %v calls=%d closed=%v", err, calls, body.closed)
		}
		sentinel := errors.New("attempt budget exhausted")
		calls = 0
		ctx = WithAttemptCheck(context.Background(), func() error {
			if calls >= 2 {
				return sentinel
			}
			return nil
		})
		req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "https://api.example/a", nil)
		_, err = RoundTrip(req, func(*http.Request) (*http.Response, error) { calls++; return nil, io.ErrUnexpectedEOF })
		if !errors.Is(err, sentinel) || calls != 2 {
			t.Fatalf("budget ignored: %v calls=%d", err, calls)
		}
	})
}

func TestDeadlinePreservesTimeForCallerFallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.example/a", nil)
		start := time.Now()
		calls := 0
		resp, err := RoundTrip(req, func(*http.Request) (*http.Response, error) {
			calls++
			r, _ := response(http.StatusTooManyRequests)
			return r, nil
		})
		if err != nil || calls != 1 || time.Since(start) != 0 {
			t.Fatal("retry consumed unusable deadline")
		}
		_ = resp.Body.Close()
	})
}

func TestClientPreservesTimeoutAndAvoidsNestedRetries(t *testing.T) {
	source := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	wrapped := Client(source)
	if wrapped.Timeout != source.Timeout || wrapped.CheckRedirect == nil || source.Transport != nil || Client(wrapped).Transport != wrapped.Transport {
		t.Fatal("client settings mutated or retries nested")
	}
}

func TestTransientStatusCoverage(t *testing.T) {
	for _, code := range []int{408, 429, 500, 502, 503, 504} {
		synctest.Test(t, func(t *testing.T) {
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodHead, "https://api.example/a", nil)
			calls := 0
			resp, err := RoundTrip(req, func(*http.Request) (*http.Response, error) {
				calls++
				status := code
				if calls > 1 {
					status = http.StatusOK
				}
				r, _ := response(status)
				return r, nil
			})
			if err != nil || calls != 2 || resp.StatusCode != http.StatusOK {
				t.Fatalf("HTTP %d not recovered: attempts=%d err=%v", code, calls, err)
			}
			_ = resp.Body.Close()
		})
	}
}
