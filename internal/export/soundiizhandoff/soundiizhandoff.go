// Package soundiizhandoff sends a playlist to Soundiiz's tokenless handoff
// endpoint and returns a share URL to open in the browser. It never follows
// redirects and validates the returned URL against a fixed host + path prefix.
// Mechanism follows github.com/platten/playlistforge.
package soundiizhandoff

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/ports"
)

// Endpoint is the fixed Soundiiz handoff URL.
const Endpoint = "https://soundiiz.com/go/import-playlist"

// SharePrefix is the required prefix of a valid returned share URL.
const SharePrefix = "https://soundiiz.com/go/import-playlist/"

// ProgressOp is the op label for export progress reports.
const ProgressOp = "export"

// Exporter implements ports.Exporter.
type Exporter struct {
	endpoint string
	hc       *http.Client
}

// New returns a handoff exporter.
func New() *Exporter {
	return &Exporter{
		endpoint: Endpoint,
		hc: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse // never follow redirects
			},
		},
	}
}

// Name implements ports.Exporter.
func (*Exporter) Name() string { return "soundiiz-handoff" }

// Available implements ports.Exporter.
func (*Exporter) Available() bool { return true }

type track struct {
	Title   string   `json:"title"`
	Artists []string `json:"artists"`
}

type request struct {
	Title       string  `json:"title"`
	SourceName  string  `json:"sourceName"`
	Description string  `json:"description"`
	Tracklist   []track `json:"tracklist"`
}

type response struct {
	Status    string `json:"status"`
	Message   string `json:"message"`
	ShareURL  string `json:"shareUrl"`
	ExpiresAt int64  `json:"expiresAt"`
	NbTracks  int    `json:"nbTracks"`
}

// Export posts the playlist and returns ExportResult.Location = the validated
// share URL. Only the title, description and track/artist names leave the machine.
func (e *Exporter) Export(ctx context.Context, req ports.ExportRequest, p ports.Progress) (ports.ExportResult, error) {
	if p == nil {
		p = ports.NopProgress{}
	}

	body := request{Title: strings.TrimSpace(req.Name), SourceName: "Playlist AI"}
	n := int64(len(req.Tracks))
	for i, t := range req.Tracks {
		artists := t.AllArtists
		if len(artists) == 0 && t.Ref.Artist != "" {
			artists = []string{t.Ref.Artist}
		}
		body.Tracklist = append(body.Tracklist, track{Title: t.Ref.Title, Artists: artists})
		p.Report(ProgressOp, int64(i+1), n, t.Ref.Title)
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return ports.ExportResult{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(buf))
	if err != nil {
		return ports.ExportResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := e.hc.Do(httpReq)
	if err != nil {
		return ports.ExportResult{}, fmt.Errorf("soundiiz: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return ports.ExportResult{}, fmt.Errorf("soundiiz: HTTP %d", resp.StatusCode)
	}

	var r response
	if err := json.Unmarshal(raw, &r); err != nil {
		return ports.ExportResult{}, fmt.Errorf("soundiiz: bad response: %w", err)
	}
	if !strings.EqualFold(r.Status, "success") {
		msg := strings.TrimSpace(r.Message)
		if msg == "" {
			msg = "Soundiiz rejected the import"
		}
		return ports.ExportResult{}, errors.New(msg)
	}
	if err := validateShareURL(r.ShareURL); err != nil {
		return ports.ExportResult{}, err
	}

	return ports.ExportResult{
		Kind:     "soundiiz-handoff",
		Location: r.ShareURL,
		Count:    r.NbTracks,
	}, nil
}

func validateShareURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("soundiiz: unparseable share URL")
	}
	if u.Scheme != "https" || u.Host != "soundiiz.com" || !strings.HasPrefix(u.Path, "/go/import-playlist/") {
		return fmt.Errorf("soundiiz: unexpected share URL %q", s)
	}
	return nil
}

var _ ports.Exporter = (*Exporter)(nil)
