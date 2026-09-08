package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/httpretry"
)

const discogsInterval = time.Minute / 25
const discogsReleaseLimit = 8
const discogsPageSize = 100
const discogsSearchPages = 3
const discogsDiscoveryReleases = 60

type discogsClient struct {
	base                  string
	http                  *http.Client
	mu                    sync.RWMutex
	token, credentialPath string
	credentialError       bool
}

type discogsThrottle struct {
	gate               chan struct{}
	last, blockedUntil time.Time
}

var discogsThrottles sync.Map

func newDiscogs(base, credentialPath string) (*discogsClient, error) {
	if base == "" {
		base = "https://api.discogs.com"
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid Discogs endpoint")
	}
	ip := net.ParseIP(u.Hostname())
	local := u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()
	if !local && base != "https://api.discogs.com" {
		return nil, errors.New("discogs: endpoint overrides are restricted to local tests")
	}
	c := &discogsClient{base: strings.TrimRight(base, "/"), credentialPath: credentialPath}
	if credentialPath != "" {
		raw, err := os.ReadFile(credentialPath)
		if err == nil && validDiscogsToken(strings.TrimSpace(string(raw))) {
			c.token = strings.TrimSpace(string(raw))
		} else if !errors.Is(err, os.ErrNotExist) {
			c.credentialError = true
		}
	}
	limiter, _ := discogsThrottles.LoadOrStore(c.base, &discogsThrottle{gate: make(chan struct{}, 1)})
	c.http = &http.Client{Timeout: 20 * time.Second, Transport: &discogsTransport{client: c, limiter: limiter.(*discogsThrottle), base: http.DefaultTransport},
		// Never forward a credential to another origin (or follow arbitrary URLs
		// returned by the provider). API paths below are constructed locally.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	return c, nil
}

type discogsTransport struct {
	client  *discogsClient
	limiter *discogsThrottle
	base    http.RoundTripper
}

func (t *discogsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return httpretry.RoundTrip(req, func(req *http.Request) (*http.Response, error) {
		select {
		case t.limiter.gate <- struct{}{}:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
		defer func() { <-t.limiter.gate }()
		wait := max(time.Until(t.limiter.last.Add(discogsInterval)), time.Until(t.limiter.blockedUntil))
		if wait > 0 {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		}
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		t.client.mu.RLock()
		token := t.client.token
		t.client.mu.RUnlock()
		if token == "" {
			return nil, errors.New("discogs: fallback needs a personal API token in Settings")
		}
		req.Header.Set("Authorization", "Discogs token="+token)
		t.limiter.last = time.Now()
		resp, err := t.base.RoundTrip(req)
		if resp != nil && (resp.StatusCode == 429 || resp.StatusCode == 503) {
			t.limiter.blockedUntil = time.Now().Add(max(discogsInterval, httpretry.RetryAfter(resp.Header.Get("Retry-After"), time.Now())))
		}
		return resp, err
	})
}

// MetadataStatus reveals configuration, never the credential itself.
type MetadataStatus struct {
	DiscogsConfigured bool `json:"discogsConfigured"`
	CredentialError   bool `json:"credentialError"`
}

func (c *Client) MetadataStatus() MetadataStatus {
	if c.discogs == nil {
		return MetadataStatus{}
	}
	c.discogs.mu.RLock()
	defer c.discogs.mu.RUnlock()
	return MetadataStatus{DiscogsConfigured: c.discogs.token != "", CredentialError: c.discogs.credentialError}
}

func validDiscogsToken(token string) bool {
	if len(token) == 0 || len(token) > 256 {
		return false
	}
	for _, r := range token {
		allowed := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
		if !allowed {
			return false
		}
	}
	return true
}

// SetDiscogsToken persists separately from world-readable preferences. Empty
// disables fallback and removes the credential. The file is plaintext, not a
// keychain; OS account/directory protections still apply (especially Windows).
func (c *Client) SetDiscogsToken(token string) error {
	if c.discogs == nil {
		return errors.New("metadata service unavailable")
	}
	token = strings.TrimSpace(token)
	if token != "" && !validDiscogsToken(token) {
		return errors.New("invalid Discogs token: paste the personal token from your developer settings")
	}
	d := c.discogs
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.credentialPath == "" {
		return errors.New("discogs: credential storage is not configured")
	}
	if token == "" {
		if err := os.Remove(d.credentialPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(d.credentialPath), 0o700); err != nil {
			return err
		}
		f, err := os.CreateTemp(filepath.Dir(d.credentialPath), ".discogs-token-*")
		if err != nil {
			return err
		}
		defer func() { _ = os.Remove(f.Name()) }()
		if _, err = f.WriteString(token); err != nil {
			_ = f.Close()
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		if err = os.Rename(f.Name(), d.credentialPath); err != nil {
			return err
		}
	}
	d.token, d.credentialError = token, false
	return nil
}

type discogsArtist struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}
type discogsTrack struct {
	Title   string          `json:"title"`
	Type    string          `json:"type_"`
	Artists []discogsArtist `json:"artists,omitempty"`
}
type discogsRelease struct {
	ID        int64           `json:"id"`
	Title     string          `json:"title"`
	MasterID  int64           `json:"master_id"`
	Artists   []discogsArtist `json:"artists"`
	Tracklist []discogsTrack  `json:"tracklist"`
}
type discogsSearch struct {
	// FetchedResults preserves page completeness when duplicate hits collapse.
	FetchedResults int `json:"fetched_results,omitempty"`
	Pagination     struct {
		Items int `json:"items"`
		Pages int `json:"pages,omitempty"`
	} `json:"pagination"`
	Results []discogsSearchResult `json:"results"`
}

type discogsSearchResult struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

func sanitizeDiscogs(path string, raw []byte) ([]byte, error) {
	if strings.HasPrefix(path, "/database/search?") {
		var page discogsSearch
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, err
		}
		// Preserve provider order and pagination totals, but store each usable
		// release once. Different searches share the same /releases/{id} entry.
		results := make([]discogsSearchResult, 0, len(page.Results))
		page.FetchedResults = len(page.Results)
		seen := make(map[int64]bool)
		for _, result := range page.Results {
			if result.ID <= 0 || result.Type != "release" || seen[result.ID] {
				continue
			}
			seen[result.ID] = true
			results = append(results, result)
		}
		page.Results = results
		return json.Marshal(page)
	}
	var release discogsRelease
	if err := json.Unmarshal(raw, &release); err != nil {
		return nil, err
	}
	if release.ID <= 0 || release.Tracklist == nil {
		return nil, errors.New("invalid Discogs release")
	}
	if path != fmt.Sprintf("/releases/%d", release.ID) {
		return nil, errors.New("discogs: returned a different release ID")
	}
	return json.Marshal(release)
}

func (c *Client) discogsGet(ctx context.Context, path string, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.expireDiscogs(ctx)
	if !c.MetadataStatus().DiscogsConfigured {
		return errors.New("musicbrainz unavailable; enable Discogs fallback with a personal API token in Settings")
	}
	raw, err := c.metadataGet(ctx, c.discogs.base, path, "discogs-v1:", c.discogs.http, false)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, value)
}

func (c *Client) discogsSearch(ctx context.Context, query url.Values) (discogsSearch, error) {
	return c.discogsSearchPage(ctx, query, 1)
}

func (c *Client) discogsSearchPage(ctx context.Context, query url.Values, number int) (discogsSearch, error) {
	if number < 1 || number > discogsSearchPages {
		return discogsSearch{}, errors.New("discogs: search page limit reached")
	}
	// Own the query before adding defaults; callers may reuse it concurrently.
	values := make(url.Values, len(query)+3)
	for key, entries := range query {
		values[key] = append([]string(nil), entries...)
	}
	query = values
	query.Set("type", "release")
	query.Set("per_page", strconv.Itoa(discogsPageSize))
	query.Set("page", strconv.Itoa(number))
	var page discogsSearch
	err := c.discogsGet(ctx, "/database/search?"+query.Encode(), &page)
	return page, err
}

func (c *Client) discogsRelease(ctx context.Context, id int64) (discogsRelease, error) {
	var release discogsRelease
	if id <= 0 {
		return release, errors.New("invalid Discogs release ID")
	}
	err := c.discogsGet(ctx, fmt.Sprintf("/releases/%d", id), &release)
	if err == nil && release.ID != id {
		err = errors.New("discogs: returned a different release ID")
	}
	return release, err
}

var discogsNameSuffix = regexp.MustCompile(` \([0-9]+\)$`)

func discogsNames(artists []discogsArtist) []string {
	var names []string
	for _, artist := range artists {
		// Discogs' numeric name suffix distinguishes database entities, not
		// recording titles. It is not a license for fuzzy artist substitution.
		name := discogsNameSuffix.ReplaceAllString(artist.Name, "")
		if name != "" && name != "Various" {
			names = append(names, name)
		}
	}
	return names
}

func discogsSource(id int64) string { return fmt.Sprintf("https://www.discogs.com/release/%d", id) }
