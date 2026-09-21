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
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/deezerhttp"
	"github.com/platten/playlistai/internal/mbindex"
	"github.com/platten/playlistai/internal/ports"
)

// ProgressOp is the op label for enrichment progress reports.
const ProgressOp = "enrich"

const defaultBase = "https://musicbrainz.org"

// Config configures a Client.
type Config struct {
	// AcousticBrainzURL enables optional archived acoustic evidence; empty disables.
	AcousticBrainzURL string
	OfflineIndexPath  string // optional catalog-independent MusicBrainz dump index
	// UserAgent identifies the app with a contact URL (MusicBrainz requirement).
	UserAgent string
	// CachePath is the SQLite file for cached lookups.
	CachePath string
	// MirrorURL overrides https://musicbrainz.org when set.
	MirrorURL string
	// DeezerURL overrides the public artist/top-track endpoint for tests.
	DeezerURL string
	// WikidataURL and WikipediaURL override context endpoints for loopback tests.
	WikidataURL  string
	WikipediaURL string
	// MinScore: lower-scoring results remain unmatched; exact artist/title
	// identity is also required. Default 85.
	MinScore int
	// Interval between live requests. Default 1s; tests set it lower.
	Interval time.Duration
	// CandidatePreviewResolver is retained for source compatibility. Discovery
	// now registers authoritative recording metadata without resolving previews;
	// the separately configured audio service verifies previews when needed.
	//
	// Deprecated: configure the audio service's Resolver instead.
	CandidatePreviewResolver ports.AudioPreviewResolver
}

// Client implements ports.Enricher.
type Client struct {
	indexMu          sync.RWMutex
	offlinePath      string
	retiredOffline   []*mbindex.Store
	offline          *mbindex.Store
	offlineError     bool
	base             string
	ua               string
	minScore         int
	interval         time.Duration
	hc               *http.Client
	deezerBase       string
	deezerClient     *http.Client
	acoustic         *acousticClient
	wikidataBase     string
	wikipediaBase    string
	contextClient    *http.Client
	candidatePreview ports.AudioPreviewResolver

	limiter *requestLimiter

	dbMu       sync.Mutex
	db         *sql.DB
	cacheEpoch uint64
	memory     map[string]cachedResponse
	inflight   map[string]chan struct{}
}

type requestLimiter struct {
	priorityMu sync.Mutex
	required   int
	changed    chan struct{}
	gate       chan struct{}
	last       time.Time
}

type MetadataStatus struct {
	MusicBrainzSnapshot   string `json:"musicBrainzSnapshot"`
	MusicBrainzRecordings int64  `json:"musicBrainzRecordings"`
	MusicBrainzIndexError bool   `json:"musicBrainzIndexError"`
}

func (c *Client) MetadataStatus() MetadataStatus {
	c.indexMu.RLock()
	defer c.indexMu.RUnlock()
	status := MetadataStatus{MusicBrainzIndexError: c.offlineError}
	if c.offline != nil {
		info := c.offline.Info()
		status.MusicBrainzSnapshot = info.Snapshot
		status.MusicBrainzRecordings = info.Recordings
	}
	return status
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
		base:             strings.TrimRight(base, "/"),
		ua:               cfg.UserAgent,
		minScore:         minScore,
		interval:         interval,
		hc:               &http.Client{Timeout: 20 * time.Second},
		candidatePreview: cfg.CandidatePreviewResolver,
	}
	limiterKey := strings.ToLower(parsed.Host)
	if host == "musicbrainz.org" || host == "www.musicbrainz.org" {
		limiterKey = "musicbrainz.org"
	}
	limiter, _ := applicationLimiters.LoadOrStore(limiterKey, &requestLimiter{gate: make(chan struct{}, 1)})
	c.limiter = limiter.(*requestLimiter)
	c.hc.Transport = &limitedTransport{client: c, base: http.DefaultTransport}
	if err := c.configureContext(cfg); err != nil {
		return nil, err
	}
	c.deezerBase = strings.TrimRight(cfg.DeezerURL, "/")
	if c.deezerBase == "" {
		c.deezerBase = "https://api.deezer.com"
	}
	c.deezerClient = deezerhttp.Client(&http.Client{Timeout: 8 * time.Second})
	if cfg.AcousticBrainzURL != "" {
		c.acoustic, err = newAcousticClient(cfg.AcousticBrainzURL)
		if err != nil {
			return nil, err
		}
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
		if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS mb_cache_expiry ON mb_cache(fetched_at)`); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	if cfg.OfflineIndexPath != "" {
		if filepath.Base(cfg.OfflineIndexPath) == "musicbrainz.sqlite" {
			cfg.OfflineIndexPath = mbindex.ActivePath(filepath.Dir(cfg.OfflineIndexPath))
		}
		if _, statErr := os.Stat(cfg.OfflineIndexPath); statErr == nil {
			c.offline, err = mbindex.Open(cfg.OfflineIndexPath)
			c.offlinePath = cfg.OfflineIndexPath
			c.offlineError = err != nil
		} else if !errors.Is(statErr, os.ErrNotExist) {
			c.offlineError = true
		}
	}
	return c, nil
}

// Name implements ports.Enricher.
func (c *Client) Name() string { return "musicbrainz" }

// Close releases the cache handle.
func (c *Client) Close() error {
	c.indexMu.Lock()
	defer c.indexMu.Unlock()
	var err error
	if c.offline != nil {
		err = errors.Join(err, c.offline.Close())
	}
	for _, s := range c.retiredOffline {
		err = errors.Join(err, s.Close())
	}
	if c.db != nil {
		return errors.Join(err, c.db.Close())
	}
	return err
}

func (c *Client) localMusicBrainz() *mbindex.Store {
	c.indexMu.RLock()
	defer c.indexMu.RUnlock()
	return c.offline
}

// ActivateOfflineIndex keeps in-flight requests on their prior immutable index.
func (c *Client) ActivateOfflineIndex(path string) error {
	s, err := mbindex.Open(path)
	if err != nil {
		return err
	}
	c.indexMu.Lock()
	defer c.indexMu.Unlock()
	if c.offline != nil && c.offlinePath == path {
		return s.Close()
	}
	if c.offline != nil {
		c.retiredOffline = append(c.retiredOffline, c.offline)
	}
	c.offline = s
	c.offlinePath = path
	c.offlineError = false
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
	c.acousticTracks(ctx, out, len(out))
	return out, ctx.Err()
}

func (c *Client) one(ctx context.Context, ref core.TrackRef) core.EnrichedTrack {
	return c.query(ctx, ref)
}

type mbRecording struct {
	Length           *int64           `json:"length"`
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
		ID   string `json:"id"`
		Name string `json:"name"`
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
	path := "/ws/2/recording?" + url.Values{
		"query": {lucene},
		"fmt":   {"json"},
		"limit": {"3"},
	}.Encode()

	var body struct {
		Recordings []mbRecording `json:"recordings"`
	}
	if offline := c.localMusicBrainz(); offline != nil {
		if rows, err := offline.FindRecordings(ctx, ref.Artist, ref.Title, 3); err == nil {
			for _, row := range rows {
				body.Recordings = append(body.Recordings, offlineRecording(row))
			}
		}
	}
	if len(body.Recordings) == 0 {
		raw, err := c.knowledgeGet(ctx, path, false)
		if err != nil {
			return miss
		}
		if json.Unmarshal(raw, &body) != nil {
			return miss
		}
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
		IdentityStatus:      core.ResolutionResolved,
		Alternatives:        miss.Alternatives,
		Ref:                 ref,
		MatchScore:          top.Score,
		Matched:             top.Score >= c.minScore,
		AllISRCs:            top.ISRCs,
		RecordingID:         top.ID,
		OriginalReleaseDate: top.FirstReleaseDate,
	}
	for _, group := range []struct {
		facet string
		tags  []mbTag
	}{{"genre", top.Genres}, {"tag", top.Tags}} {
		for _, tag := range group.tags {
			et.GenreTags = append(et.GenreTags, core.AttributedGenreTag{Name: tag.Name, Votes: tag.Count, Source: "musicbrainz", EntityID: top.ID, Facet: group.facet})
		}
	}
	if top.Length != nil && *top.Length > 0 && top.ID != "" {
		et.FullRecordingDuration = &core.RecordingDuration{Milliseconds: *top.Length, Source: "musicbrainz", RecordingID: top.ID}
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

// --- helpers -----------------------------------------------------------

func (c *Client) CachedRecording(ref core.TrackRef) (core.EnrichedTrack, bool) {
	// Reinterpret cached raw evidence using today's identity policy; never fetch.
	ctx := context.WithValue(context.Background(), cacheOnlyKey{}, true)
	tracks := []core.EnrichedTrack{c.query(ctx, ref)}
	c.acousticTracks(ctx, tracks, 1)
	return tracks[0], tracks[0].IdentityStatus != ""
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
