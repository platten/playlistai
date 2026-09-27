package musicgraph

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

type topCacheEntry struct {
	rows   []Recording
	source Source
	until  time.Time
}

// NewAuthenticatedClient uses the same process-wide limiter and endpoint
// allowlist as public discovery. The callback allows immediate disconnection.
func NewAuthenticatedClient(token func() string) (*Client, error) {
	c, err := NewClient(nil, UserAgent)
	if err != nil {
		return nil, err
	}
	c.token = token
	return c, nil
}
func (c *Client) ValidateToken(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if !validID(token) {
		return errors.New("invalid ListenBrainz token format")
	}
	// Validation uses a private client view, without mutating the shared callback.
	validation := &Client{http: c.http, userAgent: c.userAgent, limiter: c.limiter, maxBytes: c.maxBytes, token: func() string { return token }}
	b, _, err := validation.requestAttempts(ctx, apiBase+"/1/validate-token", nil, 1)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("could not validate ListenBrainz token; check the token and connection")
	}
	var result struct {
		Valid bool `json:"valid"`
	}
	if json.Unmarshal(b, &result) != nil || !result.Valid {
		return errors.New("ListenBrainz token was not accepted")
	}
	return ctx.Err()
}
func (c *Client) fetchAuthenticatedTop(ctx context.Context, artist string) ([]Recording, Source, error) {
	if !validID(artist) {
		return nil, Source{}, errors.New("invalid seed artist MBID")
	}
	if err := ctx.Err(); err != nil {
		return nil, Source{}, err
	}
	c.cacheMu.Lock()
	cached, ok := c.topCache[artist]
	c.cacheMu.Unlock()
	if ok && time.Now().Before(cached.until) {
		return cloneRecordings(cached.rows), cached.source, nil
	}
	b, source, err := c.requestAttempts(ctx, topURL(artist), nil, 1)
	if err != nil {
		return nil, Source{}, err
	}
	var rows []struct {
		ID        string   `json:"recording_mbid"`
		Artists   []string `json:"artist_mbids"`
		Listens   *int64   `json:"total_listen_count"`
		Listeners *int64   `json:"total_user_count"`
	}
	if json.Unmarshal(b, &rows) != nil {
		return nil, source, errors.New("invalid top recordings JSON")
	}
	seen := map[string]bool{}
	out := make([]Recording, 0, min(len(rows), MaxTopRecordings))
	for _, row := range rows {
		counts := Counts{ListenCount: row.Listens, UniqueListeners: row.Listeners}
		if !validID(row.ID) || seen[row.ID] || !validIDs(row.Artists, MaxBatch) || !slices.Contains(row.Artists, artist) || !counts.valid() {
			return nil, source, errors.New("invalid top recording identity or counts")
		}
		seen[row.ID] = true
		out = append(out, Recording{MBID: row.ID, ArtistMBIDs: slices.Clone(row.Artists), Counts: counts, Source: source})
	}
	sortRecordings(out)
	out = out[:min(len(out), MaxTopRecordings)]
	if err := ctx.Err(); err != nil {
		return nil, source, err
	}
	c.cacheMu.Lock()
	if c.topCache == nil {
		c.topCache = map[string]topCacheEntry{}
	}
	// A bounded session cache contains public recording metadata, never tokens.
	if len(c.topCache) >= MaxBatch {
		clear(c.topCache)
	}
	c.topCache[artist] = topCacheEntry{cloneRecordings(out), source, time.Now().Add(10 * time.Minute)}
	c.cacheMu.Unlock()
	return out, source, nil
}
func cloneRecordings(rows []Recording) []Recording {
	out := slices.Clone(rows)
	for i := range out {
		out[i].ArtistMBIDs = slices.Clone(out[i].ArtistMBIDs)
	}
	return out
}
