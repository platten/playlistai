// Package deezer implements ports.PreviewProvider over the public Deezer
// search API (https://api.deezer.com/search), which needs no API key. A miss —
// no result, no preview on the result, or the request itself failing — falls
// back to the bundled Spotify CDN preview URL shipped in the catalog rather
// than surfacing an error: a preview is a nice-to-have, never a hard
// dependency. Successful and unsuccessful lookups are both cached in memory
// for the provider's lifetime, keyed by track id (or lowercased artist+title
// when a ref carries no id), so the same track is never looked up twice.
package deezer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// DefaultBaseURL is the public Deezer API.
const DefaultBaseURL = "https://api.deezer.com"

// Config configures the provider. Every field has a usable zero value.
type Config struct {
	// BaseURL overrides DefaultBaseURL (tests point this at an httptest server).
	BaseURL string
	// HTTPClient overrides the default 8s-timeout client.
	HTTPClient *http.Client
}

// Provider implements ports.PreviewProvider.
type Provider struct {
	base string
	hc   *http.Client

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	url string
	ok  bool
}

// New returns a Deezer-backed preview provider.
func New(cfg Config) *Provider {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 8 * time.Second}
	}
	return &Provider{base: base, hc: hc, cache: make(map[string]cacheEntry)}
}

// Name implements ports.PreviewProvider.
func (*Provider) Name() string { return "deezer" }

type searchResponse struct {
	Data []struct {
		Preview string `json:"preview"`
	} `json:"data"`
}

// PreviewURL implements ports.PreviewProvider.
func (p *Provider) PreviewURL(ctx context.Context, ref core.TrackRef, bundledURL string) (string, bool, error) {
	found, ok, err := p.search(ctx, ref)
	if err == nil && ok {
		return found, true, nil
	}
	if bundledURL != "" {
		return bundledURL, true, nil
	}
	return "", false, err
}

func (p *Provider) search(ctx context.Context, ref core.TrackRef) (string, bool, error) {
	key := cacheKey(ref)
	if key != "" {
		if e, hit := p.cacheGet(key); hit {
			return e.url, e.ok, nil
		}
	}

	q := deezerQuery(ref)
	if q == "" {
		return "", false, nil
	}

	endpoint := p.base + "/search?q=" + url.QueryEscape(q) + "&limit=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", false, err
	}

	resp, err := p.hc.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("deezer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("deezer: HTTP %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", false, err
	}

	var sr searchResponse
	if err := json.Unmarshal(raw, &sr); err != nil {
		return "", false, fmt.Errorf("deezer: bad response: %w", err)
	}

	previewURL, found := "", false
	if len(sr.Data) > 0 && sr.Data[0].Preview != "" {
		previewURL, found = sr.Data[0].Preview, true
	}

	if key != "" {
		p.cachePut(key, cacheEntry{url: previewURL, ok: found})
	}
	return previewURL, found, nil
}

func (p *Provider) cacheGet(key string) (cacheEntry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.cache[key]
	return e, ok
}

func (p *Provider) cachePut(key string, e cacheEntry) {
	p.mu.Lock()
	p.cache[key] = e
	p.mu.Unlock()
}

// cacheKey prefers the stable track id; falls back to a lowercased
// artist+title pair so refs resolved without an id (rare) still dedupe.
func cacheKey(ref core.TrackRef) string {
	if ref.ID != "" {
		return ref.ID
	}
	if ref.Artist == "" && ref.Title == "" {
		return ""
	}
	return strings.ToLower(ref.Artist) + "\t" + strings.ToLower(ref.Title)
}

// deezerQuery builds Deezer's advanced-search syntax: artist:"X" track:"Y".
func deezerQuery(ref core.TrackRef) string {
	artist := strings.TrimSpace(ref.Artist)
	title := strings.TrimSpace(ref.Title)
	if artist == "" && title == "" {
		return ""
	}
	var b strings.Builder
	if artist != "" {
		b.WriteString(`artist:"`)
		b.WriteString(escapeQuotes(artist))
		b.WriteString(`" `)
	}
	if title != "" {
		b.WriteString(`track:"`)
		b.WriteString(escapeQuotes(title))
		b.WriteString(`"`)
	}
	return strings.TrimSpace(b.String())
}

func escapeQuotes(s string) string {
	return strings.ReplaceAll(s, `"`, `'`)
}

var _ ports.PreviewProvider = (*Provider)(nil)
