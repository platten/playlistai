// Package dataset fetches the shipped catalog on first launch: it reads a
// manifest, downloads the files it lists with resume + checksum verification,
// and reports byte-level progress.
package dataset

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/platten/playlistai/internal/httpretry"
)

// Catalog manifests are small, flat collections. These bounds exceed shipped
// catalogs while preventing an untrusted declaration from overflowing budgets.
const maxManifestBytes = 1 << 20
const maxCatalogBytes int64 = 128 << 30
const maxManifestFiles = 64

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
		resp, err := httpretry.Client(http.DefaultClient).Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("manifest %s: HTTP %d", location, resp.StatusCode)
		}
		raw, err = io.ReadAll(io.LimitReader(contextReader{ctx, resp.Body}, maxManifestBytes+1))
		if err != nil {
			return nil, err
		}
	} else {
		f, err := os.Open(location) //nolint:gosec // operator-supplied config path
		if err != nil {
			return nil, err
		}
		defer f.Close()
		raw, err = io.ReadAll(io.LimitReader(contextReader{ctx, f}, maxManifestBytes+1))
		if err != nil {
			return nil, err
		}
	}
	if len(raw) > maxManifestBytes {
		return nil, fmt.Errorf("manifest exceeds %d bytes", maxManifestBytes)
	}

	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("manifest %s: %w", location, err)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("manifest %s: %w", location, err)
	}
	m.baseURL = location
	return &m, nil
}

// Validate checks the complete manifest before any filesystem mutation. Names
// follow the same portable flat-file policy on every supported platform.
func (m *Manifest) Validate() error {
	if m == nil || len(m.Files) == 0 || len(m.Files) > maxManifestFiles {
		return fmt.Errorf("manifest must list 1–%d files", maxManifestFiles)
	}
	seen := make(map[string]bool, len(m.Files))
	var total int64
	for _, f := range m.Files {
		name := strings.ToLower(f.Name)
		if !validArtifactName(f.Name) || name == manifestEntryName || strings.HasSuffix(name, partSuffix) || seen[name] {
			return fmt.Errorf("invalid or duplicate manifest filename %q", f.Name)
		}
		seen[name] = true
		if f.Size < 0 || f.Size > maxCatalogBytes-total {
			return fmt.Errorf("manifest exceeds catalog size budget")
		}
		total += f.Size
		digest, err := hex.DecodeString(f.SHA256)
		if err != nil || len(digest) != 32 {
			return fmt.Errorf("invalid SHA-256 for %q", f.Name)
		}
	}
	return nil
}

func validArtifactName(name string) bool {
	if len(name) == 0 || len(name) > 255 || name[0] == '.' || strings.HasSuffix(name, ".") {
		return false
	}
	for _, c := range name {
		allowed := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.'
		if !allowed {
			return false
		}
	}
	base, _, _ := strings.Cut(strings.ToUpper(name), ".")
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CLOCK$", "CONIN$", "CONOUT$":
		return false
	}
	device := len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9'
	return !device
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
