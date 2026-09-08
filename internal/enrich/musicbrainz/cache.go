package musicbrainz

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const musicBrainzTTL = 7 * 24 * time.Hour
const discogsTTL = 6 * time.Hour
const memoryCacheEntries = 256
const memoryCacheBytes = 32 << 20

type cachedResponse struct {
	body    string
	fetched int64
}

// Origin and representation are part of identity. Do not lowercase search text
// or strip filters. Legacy hostless rows intentionally miss once after upgrade.
func metadataPrefix(base, namespace string) string {
	return fmt.Sprintf("response-v2:%s%x:", namespace, sha256.Sum256([]byte(strings.TrimRight(base, "/")+"\njson,html;en")))
}

func metadataKey(base, path, namespace string) string {
	if u, err := url.Parse(path); err == nil {
		u.RawQuery = u.Query().Encode()
		path = u.String()
	}
	return metadataPrefix(base, namespace) + path
}

func (c *Client) readCache(ctx context.Context, key string) cachedResponse {
	c.dbMu.Lock()
	defer c.dbMu.Unlock()
	if c.db == nil {
		return c.memory[key]
	}
	var entry cachedResponse
	_ = c.db.QueryRowContext(ctx, "SELECT json,fetched_at FROM mb_cache WHERE key=?", key).Scan(&entry.body, &entry.fetched)
	return entry
}

// Reads always reject expired Discogs content. Prune at startup and at most
// once per minute on provider access; never delete saved catalog identities.
func (c *Client) expireDiscogs(ctx context.Context) {
	c.dbMu.Lock()
	defer c.dbMu.Unlock()
	now := time.Now()
	if now.Before(c.nextDiscogsCleanup) {
		return
	}
	cutoff := now.Add(-discogsTTL).Unix()
	if c.db != nil {
		if _, err := c.db.ExecContext(ctx, "DELETE FROM mb_cache WHERE key LIKE 'response-v2:discogs-v1:%' AND fetched_at<=?", cutoff); err != nil {
			return // retry cleanup on the next access
		}
	}
	c.nextDiscogsCleanup = now.Add(time.Minute)
	for key, entry := range c.memory {
		if strings.HasPrefix(key, "response-v2:discogs-v1:") && entry.fetched <= cutoff {
			delete(c.memory, key)
		}
	}
}

func (c *Client) writeCache(ctx context.Context, key string, raw []byte, epoch uint64) {
	c.dbMu.Lock()
	defer c.dbMu.Unlock()
	// Clearing never allows an earlier in-flight response to refill the cache.
	if epoch != c.cacheEpoch || ctx.Err() != nil {
		return
	}
	if c.db != nil {
		_, _ = c.db.ExecContext(ctx, "INSERT OR REPLACE INTO mb_cache(key,json,fetched_at) VALUES(?,?,?)", key, string(raw), time.Now().Unix())
		return
	}
	if c.memory == nil {
		c.memory = make(map[string]cachedResponse)
	}
	c.memory[key] = cachedResponse{body: string(raw), fetched: time.Now().Unix()}
	for {
		bytes, oldest := 0, ""
		for k, e := range c.memory {
			bytes += len(k) + len(e.body)
			if oldest == "" || e.fetched < c.memory[oldest].fetched || e.fetched == c.memory[oldest].fetched && k < oldest {
				oldest = k
			}
		}
		if len(c.memory) <= memoryCacheEntries && bytes <= memoryCacheBytes {
			break
		}
		delete(c.memory, oldest)
	}
}

// takeFetch coalesces identical reads. A canceled owner releases the gate so
// another caller can retry with its own context; canceled waiters don't fetch.
func (c *Client) takeFetch(key string) (bool, <-chan struct{}, uint64) {
	c.dbMu.Lock()
	defer c.dbMu.Unlock()
	if c.inflight == nil {
		c.inflight = make(map[string]chan struct{})
	}
	if done, ok := c.inflight[key]; ok {
		return false, done, c.cacheEpoch
	}
	done := make(chan struct{})
	c.inflight[key] = done
	return true, done, c.cacheEpoch
}

func (c *Client) finishFetch(key string) {
	c.dbMu.Lock()
	defer c.dbMu.Unlock()
	close(c.inflight[key])
	delete(c.inflight, key)
}

// ClearCache clears provider lookups, including legacy projections, but not
// history, audio features, feedback, models, credentials or rate-limit state.
func (c *Client) ClearCache(ctx context.Context) error {
	c.dbMu.Lock()
	defer c.dbMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.db != nil {
		if _, err := c.db.ExecContext(ctx, "DELETE FROM mb_cache"); err != nil {
			return err
		}
	}
	c.cacheEpoch++
	c.memory = nil
	return nil
}

func (c *Client) cachedAliases(ctx context.Context) map[string]string {
	c.dbMu.Lock()
	defer c.dbMu.Unlock()
	prefix := metadataPrefix(c.base, "knowledge-v1:") + "/genre/"
	entries := map[string]string{}
	add := func(key, body string) {
		if strings.HasPrefix(key, prefix) && strings.HasSuffix(key, "/aliases") {
			entries[strings.TrimSuffix(strings.TrimPrefix(key, prefix), "/aliases")] = body
		}
	}
	if c.db == nil {
		for key, e := range c.memory {
			add(key, e.body)
		}
		return entries
	}
	rows, err := c.db.QueryContext(ctx, "SELECT key,json FROM mb_cache WHERE key LIKE ?", prefix+"%/aliases")
	if err != nil {
		return entries
	}
	defer rows.Close()
	for rows.Next() {
		var key, body string
		if rows.Scan(&key, &body) == nil {
			add(key, body)
		}
	}
	return entries
}
