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
	"net"
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

	limiter *requestLimiter

	dbMu sync.Mutex
	db   *sql.DB
}

type requestLimiter struct {
	gate chan struct{}
	last time.Time
}

var applicationLimiters sync.Map

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
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	host := strings.ToLower(parsed.Hostname())
	ip := net.ParseIP(host)
	// Only local test servers may accelerate the provider's request limit.
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) && interval < time.Second {
		interval = time.Second
	}

	c := &Client{
		base:     strings.TrimRight(base, "/"),
		ua:       cfg.UserAgent,
		minScore: minScore,
		interval: interval,
		hc:       &http.Client{Timeout: 20 * time.Second},
	}
	limiterKey := strings.ToLower(parsed.Host)
	if host == "musicbrainz.org" || host == "www.musicbrainz.org" {
		limiterKey = "musicbrainz.org"
	}
	limiter, _ := applicationLimiters.LoadOrStore(limiterKey, &requestLimiter{gate: make(chan struct{}, 1)})
	c.limiter = limiter.(*requestLimiter)
	c.hc.Transport = &limitedTransport{client: c, base: http.DefaultTransport}

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

	et := c.query(ctx, ref)
	if ctx.Err() == nil && et.IdentityStatus != "" {
		c.cachePut(key, et)
	}
	return et
}

type mbRecording struct {
	FirstReleaseDate string           `json:"first-release-date"`
	Genres           []mbTag          `json:"genres"`
	Tags             []mbTag          `json:"tags"`
	ID               string           `json:"id"`
	Score            int              `json:"score"`
	Title            string           `json:"title"`
	ISRCs            []string         `json:"isrcs"`
	ArtistCredit     []mbArtistCredit `json:"artist-credit"`
	Releases         []mbRelease      `json:"releases"`
}
type mbTag struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type mbArtistCredit struct {
	Name   string `json:"name"`
	Artist struct {
		ID string `json:"id"`
	} `json:"artist"`
}

type mbRelease struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Date         string `json:"date"`
	ReleaseGroup struct {
		ID               string `json:"id"`
		FirstReleaseDate string `json:"first-release-date"`
	} `json:"release-group"`
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
	if json.Unmarshal(raw, &body) != nil {
		return miss
	}
	miss.IdentityStatus = core.ResolutionUnresolved
	if len(body.Recordings) == 0 {
		return miss
	}
	var matching []mbRecording
	for _, recording := range body.Recordings {
		alternative := core.RecordingAlternative{RecordingID: recording.ID, Title: recording.Title, ISRCs: recording.ISRCs, Score: recording.Score}
		artistMatches := false
		for _, credit := range recording.ArtistCredit {
			alternative.Artists = append(alternative.Artists, credit.Name)
			if core.NormalizeIdentityPart(credit.Name) == core.NormalizeIdentityPart(ref.Artist) {
				artistMatches = true
			}
		}
		miss.Alternatives = append(miss.Alternatives, alternative)
		if artistMatches && core.NormalizeIdentityPart(recording.Title) == core.NormalizeIdentityPart(ref.Title) && recording.Score >= c.minScore {
			matching = append(matching, recording)
		}
	}
	if len(matching) > 1 {
		miss.IdentityStatus = core.ResolutionAmbiguous
		return miss
	}
	if len(matching) == 0 {
		return miss
	}

	top := matching[0]
	et := core.EnrichedTrack{
		IdentityStatus: core.ResolutionResolved,
		Alternatives:   miss.Alternatives,
		Ref:            ref,
		MatchScore:     top.Score,
		Matched:        top.Score >= c.minScore,
		AllISRCs:       top.ISRCs,
		RecordingID:    top.ID,
	}
	for _, group := range []struct {
		facet string
		tags  []mbTag
	}{{"genre", top.Genres}, {"tag", top.Tags}} {
		for _, tag := range group.tags {
			et.GenreTags = append(et.GenreTags, core.AttributedGenreTag{Name: tag.Name, Votes: tag.Count, Source: "musicbrainz", EntityID: top.ID, Facet: group.facet})
		}
	}
	if len(top.ISRCs) > 0 {
		et.ISRC = top.ISRCs[0]
	}
	for _, ac := range top.ArtistCredit {
		if ac.Name != "" {
			et.AllArtists = append(et.AllArtists, ac.Name)
		}
		if ac.Artist.ID != "" {
			et.ArtistIDs = append(et.ArtistIDs, ac.Artist.ID)
		}
	}
	if len(top.Releases) > 0 {
		et.Album = top.Releases[0].Title
		et.Year = yearOf(top.Releases[0].Date)
		et.ReleaseID = top.Releases[0].ID
		et.ReleaseEditionDate = top.Releases[0].Date
		et.OriginalReleaseDate = top.Releases[0].ReleaseGroup.FirstReleaseDate
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
	return "identity-v2\t" + strings.ToLower(strings.TrimSpace(ref.Artist)) + "\t" + strings.ToLower(strings.TrimSpace(ref.Title))
}

func (c *Client) CachedRecording(ref core.TrackRef) (core.EnrichedTrack, bool) {
	result, ok := c.cacheGet(cacheKey(ref))
	result.Ref = ref
	return result, ok
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
