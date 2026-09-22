// Package discoveryasset installs immutable shared music-discovery releases.
package discoveryasset

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const ProgressOp = "discovery-data"
const MaxIndexedDownloadBytes int64 = 6_000_000_000
const maxExpandedReleaseBytes int64 = 3 * MaxIndexedDownloadBytes
const maxGeneratedCompanionBytes int64 = 12_000_000_000
const Format = "playlist-ai-discovery"

type Config struct {
	ManifestURL string `json:"manifestUrl"`
}
type File struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	Size          int64  `json:"size"`
	SHA256        string `json:"sha256"`
	PackID        string `json:"packId,omitempty"`
	ExpandedBytes int64  `json:"expandedBytes,omitempty"`
}
type Manifest struct {
	Format          string `json:"format"`
	SchemaVersion   int    `json:"schemaVersion"`
	Version         string `json:"version"`
	Packs           []File `json:"packs"`
	Companion       File   `json:"companion"`
	Source          string `json:"source,omitempty"`
	ManifestDigest  string `json:"manifestDigest,omitempty"`
	TransportFormat string `json:"transportFormat,omitempty"`
	TransportBytes  int64  `json:"transportBytes,omitempty"`
	EmbeddedIndexes bool   `json:"embeddedIndexes,omitempty"`
}
type Status struct {
	Installed      bool     `json:"installed"`
	Version        string   `json:"version"`
	PackIDs        []string `json:"packIds"`
	Tracks         int      `json:"tracks"`
	DownloadBytes  int64    `json:"downloadBytes"`
	Error          string   `json:"error,omitempty"`
	Source         string   `json:"source"`
	ManifestDigest string   `json:"manifestDigest"`
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func validHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}
func validURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == ""
}
func (m Manifest) TotalBytes() int64 {
	n := m.Companion.Size
	for _, f := range m.Packs {
		n += f.Size
	}
	return n
}
func (m Manifest) hasGeneratedCompanion() bool {
	return validHash(m.ManifestDigest) &&
		((m.Source == "local" && m.TransportFormat == "paipack-v5") ||
			(m.Source == "hosted" && m.TransportFormat == "modelpack-v1"))
}
func (m Manifest) hasActivationFields() bool {
	return m.Source != "" || m.ManifestDigest != "" || m.TransportFormat != "" || m.TransportBytes != 0
}
func (m Manifest) Validate() error {
	if m.Format != Format || m.SchemaVersion != 1 || !safeName.MatchString(m.Version) || len(m.Packs) == 0 || len(m.Packs) > 128 {
		return errors.New("discoveryasset: invalid release manifest")
	}
	seen := map[string]bool{}
	ids := map[string]bool{}
	var total int64
	for _, f := range m.Packs {
		if !safeName.MatchString(f.Name) || seen[f.Name] || !validURL(f.URL) || f.Size <= 0 || f.Size > MaxIndexedDownloadBytes-total || !validHash(f.SHA256) {
			return errors.New("discoveryasset: invalid file or release exceeds its download limit")
		}
		seen[f.Name] = true
		total += f.Size
	}
	// Legacy curated releases download their companion under the same bounded
	// hosted-data budget as indexed releases.
	// New curated, local, and multipart releases carry profiles inside each
	// indexed paipack; already installed releases may retain an external one.
	if m.EmbeddedIndexes {
		published := m.Source == "" && m.TransportFormat == "" && m.ManifestDigest == "" && m.TransportBytes == 0
		installed := validHash(m.ManifestDigest) && ((m.Source == "local" && m.TransportFormat == "paipack-v8") || (m.Source == "hosted" && (m.TransportFormat == "modelpack-v1" || m.TransportFormat == "discovery-v8")))
		if (!published && !installed) || m.Companion != (File{}) {
			return errors.New("discoveryasset: invalid embedded-index release")
		}
	} else {
		companion := m.Companion
		companionLimit := MaxIndexedDownloadBytes - total
		if m.hasGeneratedCompanion() {
			companionLimit = maxGeneratedCompanionBytes
		}
		if !safeName.MatchString(companion.Name) || seen[companion.Name] || !validURL(companion.URL) || companion.Size <= 0 || companion.Size > companionLimit || !validHash(companion.SHA256) {
			return errors.New("discoveryasset: invalid companion or release exceeds its size limit")
		}
		if m.Companion.Name != "discovery.sqlite" || m.Companion.PackID != "" {
			return errors.New("discoveryasset: missing companion index")
		}
	}
	var expanded int64
	for _, f := range m.Packs {
		if !strings.HasSuffix(f.Name, ".paipack") || !validHash(f.PackID) || ids[f.PackID] {
			return errors.New("discoveryasset: invalid or duplicate pack identity")
		}
		if f.ExpandedBytes <= 0 || f.ExpandedBytes > maxExpandedReleaseBytes-expanded {
			return errors.New("discoveryasset: invalid expansion size or release exceeds 18 GB expanded")
		}
		expanded += f.ExpandedBytes
		ids[f.PackID] = true
	}
	return nil
}

// RequiredDiskBytes is a conservative staging estimate including downloads,
// pack extraction, and bounded verification scratch. Older curated releases
// may also retain separate derived files on disk.
func (m Manifest) RequiredDiskBytes() int64 {
	n := m.TotalBytes() + 256<<20
	for _, f := range m.Packs {
		n += 3 * f.ExpandedBytes
	}
	return n
}
func downloadClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if !validURL(r.URL.String()) || len(via) >= 5 {
			return errors.New("discoveryasset: unsafe redirect")
		}
		return nil
	}}
}
func FetchManifest(ctx context.Context, rawURL string) (Manifest, error) {
	var m Manifest
	if !validURL(rawURL) {
		return m, errors.New("discoveryasset: manifest URL must use HTTPS")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	r, e := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if e != nil {
		return m, e
	}
	resp, e := downloadClient().Do(r)
	if e != nil {
		return m, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return m, fmt.Errorf("discoveryasset: manifest HTTP %d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if e != nil {
		return m, e
	}
	if len(b) > 1<<20 {
		return m, errors.New("discoveryasset: manifest too large")
	}
	if e = json.Unmarshal(b, &m); e != nil {
		return m, e
	}
	if m.hasActivationFields() {
		return m, errors.New("discoveryasset: remote manifest contains local activation fields")
	}
	return m, m.Validate()
}
