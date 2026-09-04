// Package dataset fetches the shipped catalog on first launch: it reads a
// manifest, downloads the files it lists with resume + checksum verification,
// and reports byte-level progress.
package dataset

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// File is one downloadable artifact in a Manifest.
type File struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	URL    string `json:"url,omitempty"` // absolute; when empty, resolved against the manifest URL
}

// Manifest describes a catalog build (produced by python/convert_pickles.py).
type Manifest struct {
	Name          string `json:"name"`
	FormatVersion int    `json:"format_version"`
	Dim           int    `json:"dim"`
	Spaces        int    `json:"spaces"`
	Quant         string `json:"quant"`
	TrackCount    int    `json:"track_count"`
	Source        string `json:"source"`
	Created       int64  `json:"created"`
	Files         []File `json:"files"`

	// baseURL is where the manifest was loaded from, used to resolve relative
	// file names. Not part of the JSON.
	baseURL string
}

// LoadManifest reads a manifest from an http(s) URL or a local file path.
func LoadManifest(ctx context.Context, location string) (*Manifest, error) {
	var raw []byte

	if strings.HasPrefix(location, "http://") || strings.HasPrefix(location, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("manifest %s: HTTP %d", location, resp.StatusCode)
		}
		raw, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return nil, err
		}
	} else {
		b, err := os.ReadFile(location) //nolint:gosec // operator-supplied config path
		if err != nil {
			return nil, err
		}
		raw = b
	}

	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("manifest %s: %w", location, err)
	}
	if len(m.Files) == 0 {
		return nil, fmt.Errorf("manifest %s: no files listed", location)
	}
	m.baseURL = location
	return &m, nil
}

// fileURL resolves the download URL for a file entry.
func (m *Manifest) fileURL(f File) string {
	if f.URL != "" {
		return f.URL
	}
	if i := strings.LastIndexByte(m.baseURL, '/'); i >= 0 {
		return m.baseURL[:i+1] + f.Name
	}
	return f.Name
}

// TotalBytes is the sum of all file sizes.
func (m *Manifest) TotalBytes() int64 {
	var n int64
	for _, f := range m.Files {
		n += f.Size
	}
	return n
}
