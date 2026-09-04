// Package musicbrainz resolves an ISRC and release metadata for a track by
// searching MusicBrainz on artist + title. It rate-limits live requests to one
// per second (MusicBrainz's anonymous limit), caches results in SQLite, and
// never fails a batch because one track did not match.
package musicbrainz

import (
	"context"
	"database/sql"
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

	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// ProgressOp is the op label for enrichment progress reports.
const ProgressOp = "enrich"

const defaultBase = "https://musicbrainz.org"

// Config configures a Client.
type Config struct {
	// UserAgent identifies the app with a contact URL (MusicBrainz requirement).
	UserAgent string
	// CachePath is the SQLite file for cached lookups.
	CachePath string
	// MirrorURL overrides https://musicbrainz.org when set.
	MirrorURL string
	// MinScore: results scoring below this are Matched == false but still carry
	// whatever metadata the top hit had. Default 85.
	MinScore int
	// Interval between live requests. Default 1s; tests set it lower.
	Interval time.Duration
}

// Client implements ports.Enricher.
type Client struct {
	base     string
	ua       string
	minScore int
	interval time.Duration
	hc       *http.Client

	rlMu    sync.Mutex
	lastReq time.Time

	dbMu sync.Mutex
	db   *sql.DB
}

// New opens (creating if needed) the cache and returns a Client.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.UserAgent) == "" {
		return nil, errors.New("musicbrainz: a descriptive User-Agent is required")
	}
	base := cfg.MirrorURL
	if base == "" {
		base = defaultBase
	}
	minScore := cfg.MinScore
	if minScore <= 0 {
		minScore = 85
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = time.Second
	}

	c := &Client{
		base:     strings.TrimRight(base, "/"),
		ua:       cfg.UserAgent,
		minScore: minScore,
		interval: interval,
		hc:       &http.Client{Timeout: 20 * time.Second},
	}

	if cfg.CachePath != "" {
		db, err := sql.Open("sqlite", "file:"+cfg.CachePath+"?_pragma=busy_timeout(5000)")
		if err != nil {
			return nil, err
		}
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS mb_cache (
			key TEXT PRIMARY KEY, json TEXT NOT NULL, fetched_at INTEGER NOT NULL
		)`); err != nil {
			_ = db.Close()
			return nil, err
		}
		c.db = db
	}
	return c, nil
}

// Name implements ports.Enricher.
func (c *Client) Name() string { return "musicbrainz" }

// Close releases the cache handle.
func (c *Client) Close() error {
	if c.db != nil {
		return c.db.Close()
	}
	return nil
}

// Enrich implements ports.Enricher.
func (c *Client) Enrich(ctx context.Context, refs []core.TrackRef, p ports.Progress) ([]core.EnrichedTrack, error) {
	if p == nil {
		p = ports.NopProgress{}
	}
	out := make([]core.EnrichedTrack, 0, len(refs))
	for i, ref := range refs {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		out = append(out, c.one(ctx, ref))
		p.Report(ProgressOp, int64(i+1), int64(len(refs)), ref.Display())
	}
	return out, nil
}

func (c *Client) one(ctx context.Context, ref core.TrackRef) core.EnrichedTrack {
	key := cacheKey(ref)
	if et, ok := c.cacheGet(key); ok {
		et.Ref = ref
		return et
	}

	if err := c.rateLimit(ctx); err != nil {
		return core.EnrichedTrack{Ref: ref}
	}

	et := c.query(ctx, ref)
	c.cachePut(key, et)
	return et
}

func (c *Client) rateLimit(ctx context.Context) error {
	c.rlMu.Lock()
	wait := c.interval - time.Since(c.lastReq)
	if wait < 0 {
		wait = 0
	}
	c.lastReq = time.Now().Add(wait)
	c.rlMu.Unlock()

	if wait == 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type mbRecording struct {
	ID           string   `json:"id"`
	Score        int      `json:"score"`
	Title        string   `json:"title"`
	ISRCs        []string `json:"isrcs"`
	ArtistCredit []struct {
		Name string `json:"name"`
	} `json:"artist-credit"`
	Releases []struct {
		Title string `json:"title"`
		Date  string `json:"date"`
	} `json:"releases"`
}

// query performs one search. Any failure yields an unmatched result — the batch
// carries on.
func (c *Client) query(ctx context.Context, ref core.TrackRef) core.EnrichedTrack {
	miss := core.EnrichedTrack{Ref: ref}

	lucene := fmt.Sprintf(`artist:"%s" AND recording:"%s"`, mbEscape(ref.Artist), mbEscape(ref.Title))
	endpoint := c.base + "/ws/2/recording?" + url.Values{
		"query": {lucene},
		"fmt":   {"json"},
		"limit": {"3"},
	}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return miss
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return miss
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return miss
	}

	var body struct {
		Recordings []mbRecording `json:"recordings"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if json.Unmarshal(raw, &body) != nil || len(body.Recordings) == 0 {
		return miss
	}

	top := body.Recordings[0]
	et := core.EnrichedTrack{
		Ref:        ref,
		MatchScore: top.Score,
		Matched:    top.Score >= c.minScore,
		AllISRCs:   top.ISRCs,
	}
	if len(top.ISRCs) > 0 {
		et.ISRC = top.ISRCs[0]
	}
	for _, ac := range top.ArtistCredit {
		if ac.Name != "" {
			et.AllArtists = append(et.AllArtists, ac.Name)
		}
	}
	if len(top.Releases) > 0 {
		et.Album = top.Releases[0].Title
		et.Year = yearOf(top.Releases[0].Date)
	}
	return et
}

// --- cache -------------------------------------------------------------

func (c *Client) cacheGet(key string) (core.EnrichedTrack, bool) {
	if c.db == nil {
		return core.EnrichedTrack{}, false
	}
	c.dbMu.Lock()
	defer c.dbMu.Unlock()

	var js string
	err := c.db.QueryRow("SELECT json FROM mb_cache WHERE key = ?", key).Scan(&js)
	if err != nil {
		return core.EnrichedTrack{}, false
	}
	var et core.EnrichedTrack
	if json.Unmarshal([]byte(js), &et) != nil {
		return core.EnrichedTrack{}, false
	}
	return et, true
}

func (c *Client) cachePut(key string, et core.EnrichedTrack) {
	if c.db == nil {
		return
	}
	et.Ref = core.TrackRef{} // the ref is per-query, not cacheable
	js, err := json.Marshal(et)
	if err != nil {
		return
	}
	c.dbMu.Lock()
	defer c.dbMu.Unlock()
	_, _ = c.db.Exec(
		"INSERT OR REPLACE INTO mb_cache (key, json, fetched_at) VALUES (?, ?, ?)",
		key, string(js), time.Now().Unix(),
	)
}

// --- helpers -----------------------------------------------------------

func cacheKey(ref core.TrackRef) string {
	return strings.ToLower(strings.TrimSpace(ref.Artist)) + "\t" + strings.ToLower(strings.TrimSpace(ref.Title))
}

var mbEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

func mbEscape(s string) string { return mbEscaper.Replace(strings.TrimSpace(s)) }

func yearOf(date string) int {
	if len(date) < 4 {
		return 0
	}
	n, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0
	}
	return n
}

var _ ports.Enricher = (*Client)(nil)
