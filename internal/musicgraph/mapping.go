package musicgraph

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// RecordingMapping is an unverified proposal. Callers must corroborate the
// recording and version through their recording resolver before attaching it
// to any catalog track. Similar names, including exact text, are insufficient.
type RecordingMapping struct {
	RecordingMBID string   `json:"recording_mbid"`
	ArtistMBIDs   []string `json:"artist_mbids"`
	ArtistName    string   `json:"artist_credit_name"`
	Title         string   `json:"recording_name"`
	Release       string   `json:"release_name"`
}
type mappingCacheEntry struct {
	proposal RecordingMapping
	until    time.Time
}

func (c *Client) ProposeRecordingMapping(ctx context.Context, artist, title, release string) (RecordingMapping, error) {
	if err := ctx.Err(); err != nil {
		return RecordingMapping{}, err
	}
	if c.token == nil || c.token() == "" {
		return RecordingMapping{}, errors.New("ListenBrainz connection required")
	}
	artist, title, release = strings.TrimSpace(artist), strings.TrimSpace(title), strings.TrimSpace(release)
	if artist == "" || title == "" || utf8.RuneCountInString(artist+title+release) > 250 {
		return RecordingMapping{}, errors.New("invalid recording mapping query")
	}
	body, _ := json.Marshal(struct {
		Recordings []map[string]string `json:"recordings"`
	}{[]map[string]string{{"artist_name": artist, "recording_name": title, "release_name": release}}})
	key := digest(body)
	c.cacheMu.Lock()
	cached, ok := c.mappingCache[key]
	c.cacheMu.Unlock()
	if ok && time.Now().Before(cached.until) {
		cached.proposal.ArtistMBIDs = slices.Clone(cached.proposal.ArtistMBIDs)
		return cached.proposal, nil
	}
	b, _, err := c.requestAttempts(ctx, apiBase+"/1/metadata/lookup/", body, 1)
	if err != nil {
		return RecordingMapping{}, err
	}
	var rows []struct {
		RecordingMapping
		Index     int    `json:"index"`
		ArtistArg string `json:"artist_name_arg"`
		TitleArg  string `json:"recording_name_arg"`
	}
	if json.Unmarshal(b, &rows) != nil || len(rows) != 1 || rows[0].Index != 0 || rows[0].ArtistArg != artist || rows[0].TitleArg != title {
		return RecordingMapping{}, errors.New("mismatched recording mapping response")
	}
	proposal := rows[0].RecordingMapping
	if proposal.RecordingMBID != "" && (!validID(proposal.RecordingMBID) || !validIDs(proposal.ArtistMBIDs, MaxBatch) || len(proposal.ArtistMBIDs) == 0 || proposal.Title == "" || proposal.ArtistName == "") {
		return RecordingMapping{}, errors.New("invalid recording mapping proposal")
	}
	if err := ctx.Err(); err != nil {
		return RecordingMapping{}, err
	}
	c.cacheMu.Lock()
	if c.mappingCache == nil {
		c.mappingCache = map[string]mappingCacheEntry{}
	}
	if len(c.mappingCache) >= 512 {
		clear(c.mappingCache)
	}
	c.mappingCache[key] = mappingCacheEntry{proposal, time.Now().Add(10 * time.Minute)}
	c.cacheMu.Unlock()
	proposal.ArtistMBIDs = slices.Clone(proposal.ArtistMBIDs)
	return proposal, nil
}
