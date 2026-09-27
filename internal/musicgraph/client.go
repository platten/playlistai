package musicgraph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const apiBase = "https://api.listenbrainz.org"
const MaxResponseBytes = 4 << 20
const RequestTimeout = 30 * time.Second
const ForegroundTimeout = 5 * time.Second
const UserAgent = "PlaylistAI/musicgraph-v1 (https://github.com/platten/playlistai)"

// Client calls aggregate discovery metadata only, never listening-history endpoints.
// Reuse one instance for preparation; credentials are read only at dispatch.
type Client struct {
	http         *http.Client
	userAgent    string
	limiter      *requestLimiter
	maxBytes     int64
	token        func() string
	cacheMu      sync.Mutex
	topCache     map[string]topCacheEntry
	mappingCache map[string]mappingCacheEntry
}

type requestLimiter struct {
	gate     chan struct{}
	next     time.Time // protected by gate, including response header updates
	interval time.Duration
}

var publicLimiter = &requestLimiter{gate: make(chan struct{}, 1), interval: time.Second}

func NewClient(client *http.Client, userAgent string) (*Client, error) {
	if strings.TrimSpace(userAgent) == "" || strings.ContainsAny(userAgent, "\r\n") {
		return nil, errors.New("music graph User-Agent required")
	}
	if client == nil {
		client = &http.Client{}
	}
	copyClient := *client
	copyClient.Jar = nil // public endpoints never receive cookies
	copyClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return errors.New("music graph redirects disabled") }
	return &Client{http: &copyClient, userAgent: userAgent, limiter: publicLimiter, maxBytes: MaxResponseBytes}, nil
}

func topURL(id string) string { return apiBase + "/1/popularity/top-recordings-for-artist/" + id }
func radioURL(id string) string {
	q := url.Values{"mode": {"easy"}, "max_similar_artists": {strconv.Itoa(MaxNeighbors)}, "max_recordings_per_artist": {"10"}, "pop_begin": {"0"}, "pop_end": {"100"}}
	return apiBase + "/1/lb-radio/artist/" + id + "?" + q.Encode()
}

func validEndpoint(raw string) bool {
	if raw == apiBase+"/1/popularity/artist" || raw == apiBase+"/1/popularity/recording" {
		return true
	}
	for _, prefix := range []string{apiBase + "/1/popularity/top-recordings-for-artist/", apiBase + "/1/lb-radio/artist/"} {
		if !strings.HasPrefix(raw, prefix) {
			continue
		}
		id := strings.SplitN(strings.TrimPrefix(raw, prefix), "?", 2)[0]
		if validID(id) && (raw == topURL(id) || raw == radioURL(id)) {
			return true
		}
	}
	return false
}

func waitUntil(ctx context.Context, until time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d := time.Until(until)
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return ctx.Err()
	}
}

func (c *Client) request(ctx context.Context, endpoint string, body []byte) ([]byte, Source, error) {
	return c.requestAttempts(ctx, endpoint, body, 2)
}

func (c *Client) requestAttempts(ctx context.Context, endpoint string, body []byte, attempts int) ([]byte, Source, error) {
	var source Source
	if !validEndpoint(endpoint) && (c.token == nil || endpoint != apiBase+"/1/validate-token" && endpoint != apiBase+"/1/metadata/lookup/") {
		return nil, source, errors.New("unsupported music graph endpoint")
	}
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	for attempt := 0; attempt < attempts; attempt++ {
		select {
		case c.limiter.gate <- struct{}{}:
		case <-ctx.Done():
			return nil, source, ctx.Err()
		}
		b, status, err := c.dispatch(ctx, endpoint, body)
		<-c.limiter.gate
		if err != nil {
			return nil, source, err
		}
		if status == http.StatusOK {
			source = Source{Provider: "listenbrainz", URL: endpoint, RetrievedAt: time.Now().UTC(), ResponseSHA256: digest(b), License: "CC0-1.0"}
			return b, source, nil
		}
		if attempt+1 == attempts || (status != 429 && status != 502 && status != 503 && status != 504) {
			return nil, source, fmt.Errorf("ListenBrainz metadata HTTP %d", status)
		}
	}
	return nil, source, errors.New("ListenBrainz metadata unavailable")
}

// dispatch holds the cancellable limiter gate until headers have updated the
// next dispatch time. Retry-After and exhausted quota can lengthen, never shorten,
// the mandatory one-second spacing. No response bodies enter errors or logs.
func (c *Client) dispatch(ctx context.Context, endpoint string, body []byte) ([]byte, int, error) {
	if err := waitUntil(ctx, c.limiter.next); err != nil {
		return nil, 0, err
	}
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	if c.token != nil && (strings.HasPrefix(endpoint, apiBase+"/1/popularity/top-recordings-for-artist/") || endpoint == apiBase+"/1/validate-token" || endpoint == apiBase+"/1/metadata/lookup/") {
		if token := c.token(); token != "" {
			req.Header.Set("Authorization", "Token "+token)
		}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.limiter.next = time.Now().Add(c.limiter.interval)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	now := time.Now()
	if delay := retryDelay(res.Header.Get("Retry-After"), now); delay > 0 {
		c.limiter.next = maxTime(c.limiter.next, now.Add(delay))
	}
	if res.Header.Get("X-RateLimit-Remaining") == "0" {
		if seconds, err := strconv.ParseFloat(res.Header.Get("X-RateLimit-Reset-In"), 64); err == nil && seconds > 0 {
			c.limiter.next = maxTime(c.limiter.next, now.Add(time.Duration(min(seconds, 86400)*float64(time.Second))))
		}
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, c.maxBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(b)) > c.maxBytes {
		return nil, 0, errors.New("ListenBrainz response exceeds size limit")
	}
	if err = ctx.Err(); err != nil {
		return nil, 0, err
	}
	return b, res.StatusCode, nil
}

func retryDelay(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		return time.Duration(min(seconds, 86400)) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return min(date.Sub(now), 24*time.Hour)
	}
	return 0
}
func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func (c *Client) FetchArtists(ctx context.Context, ids []string) ([]Artist, error) {
	if len(ids) == 0 || !validIDs(ids, MaxBatch) {
		return nil, errors.New("invalid artist popularity batch")
	}
	body, _ := json.Marshal(struct {
		IDs []string `json:"artist_mbids"`
	}{ids})
	b, source, err := c.request(ctx, apiBase+"/1/popularity/artist", body)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID        string `json:"artist_mbid"`
		Listens   *int64 `json:"total_listen_count"`
		Listeners *int64 `json:"total_user_count"`
	}
	if err = json.Unmarshal(b, &rows); err != nil {
		return nil, errors.New("invalid artist popularity JSON")
	}
	if len(rows) != len(ids) {
		return nil, errors.New("incomplete artist popularity batch")
	}
	wanted := map[string]bool{}
	seen := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	out := make([]Artist, 0, len(rows))
	for _, row := range rows {
		counts := Counts{UniqueListeners: row.Listeners, ListenCount: row.Listens}
		if !wanted[row.ID] || seen[row.ID] || !counts.valid() {
			return nil, errors.New("mismatched artist popularity identity or count")
		}
		seen[row.ID] = true
		out = append(out, Artist{MBID: row.ID, Counts: counts, Source: source})
	}
	return out, ctx.Err()
}

// ForegroundArtists is opt-in. A failed optional update retains cached counts,
// including their original dates, and never changes the pinned Reader or disk.
// Parent cancellation is still returned; an optional child timeout is fallback.
func (c *Client) ForegroundArtists(ctx context.Context, cached *Reader, ids []string) (map[string]Artist, error) {
	if !validIDs(ids, MaxBatch) {
		return nil, errors.New("invalid artist popularity batch")
	}
	out := map[string]Artist{}
	for _, id := range ids {
		if a, ok := cached.Artist(ctx, id); ok {
			out[id] = a
		}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	child, cancel := context.WithTimeout(ctx, ForegroundTimeout)
	defer cancel()
	rows, err := c.FetchArtists(child, ids)
	if err == nil {
		for _, a := range rows {
			out[a.MBID] = a
		}
	}
	return out, ctx.Err()
}

// FetchRecordingCounts enriches already identified public recording MBIDs. It
// cannot provide performer or version identity and never overwrites that source.
func (c *Client) FetchRecordingCounts(ctx context.Context, ids []string) (map[string]Counts, Source, error) {
	if len(ids) == 0 || !validIDs(ids, MaxBatch) {
		return nil, Source{}, errors.New("invalid recording popularity batch")
	}
	body, _ := json.Marshal(struct {
		IDs []string `json:"recording_mbids"`
	}{ids})
	b, source, err := c.request(ctx, apiBase+"/1/popularity/recording", body)
	if err != nil {
		return nil, source, err
	}
	var rows []struct {
		ID        string `json:"recording_mbid"`
		Listens   *int64 `json:"total_listen_count"`
		Listeners *int64 `json:"total_user_count"`
	}
	if err = json.Unmarshal(b, &rows); err != nil {
		return nil, source, errors.New("invalid recording popularity JSON")
	}
	if len(rows) != len(ids) {
		return nil, source, errors.New("incomplete recording popularity batch")
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	out := map[string]Counts{}
	for _, row := range rows {
		_, seen := out[row.ID]
		counts := Counts{UniqueListeners: row.Listeners, ListenCount: row.Listens}
		if !wanted[row.ID] || seen || !counts.valid() {
			return nil, source, errors.New("mismatched recording popularity identity or counts")
		}
		out[row.ID] = counts
	}
	return out, source, ctx.Err()
}
