package musicbrainz

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestRetriesKeepMusicBrainzSpacingAndMetadataBudget(t *testing.T) {
	for _, remaining := range []int{1, 3} {
		synctest.Test(t, func(t *testing.T) {
			c := &Client{base: "https://musicbrainz.org", ua: "fixture", interval: time.Second, limiter: &requestLimiter{gate: make(chan struct{}, 1)}}
			var starts []time.Time
			c.hc = &http.Client{Transport: &limitedTransport{client: c, base: transportFunc(func(*http.Request) (*http.Response, error) {
				starts = append(starts, time.Now())
				status := http.StatusServiceUnavailable
				if len(starts) == 3 {
					status = http.StatusOK
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"recordings":[]}`))}, nil
			})}}
			budget := &knowledgeBudget{requests: KnowledgeRequests - remaining}
			ctx := context.WithValue(context.Background(), knowledgeBudgetKey{}, budget)
			_, err := c.knowledgeGet(ctx, "/ws/2/recording", false)
			if remaining == 1 && (err == nil || !strings.Contains(err.Error(), "budget exhausted")) {
				t.Fatalf("request budget ignored: %v", err)
			}
			if remaining == 3 && err != nil {
				t.Fatalf("transient outage not recovered: %v", err)
			}
			if len(starts) != remaining || budget.requests != KnowledgeRequests {
				t.Fatalf("retry budget: dispatches=%d charged=%d", len(starts), budget.requests)
			}
			for i := 1; i < len(starts); i++ {
				if starts[i].Sub(starts[i-1]) < time.Second {
					t.Fatal("retry bypassed MusicBrainz throttle")
				}
			}
		})
	}
}
