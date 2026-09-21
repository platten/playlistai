package httpretry

import (
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

func TestPermanentAdmissionFailureDoesNotRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cause := errors.New("request budget exhausted")
		req, _ := http.NewRequest(http.MethodGet, "https://example.invalid", nil)
		attempts := 0
		started := time.Now()
		_, err := RoundTrip(req, func(*http.Request) (*http.Response, error) { attempts++; return nil, Permanent(cause) })
		if !errors.Is(err, cause) || attempts != 1 || time.Since(started) != 0 {
			t.Fatalf("err=%v attempts=%d elapsed=%s", err, attempts, time.Since(started))
		}
	})
}
