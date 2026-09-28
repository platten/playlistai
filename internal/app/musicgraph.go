package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/dataset"
	"github.com/platten/playlistai/internal/installlock"
	"github.com/platten/playlistai/internal/musicgraph"
	"github.com/platten/playlistai/internal/ports"
)

type MusicGraphArtifact struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	URL    string `json:"url"`
}
type MusicGraphManifest struct {
	Version    string             `json:"version"`
	PreparedAt time.Time          `json:"preparedAt"`
	Snapshot   MusicGraphArtifact `json:"snapshot"`
}
type MusicGraphUpdate struct {
	Manifest        MusicGraphManifest `json:"manifest"`
	UpdateAvailable bool               `json:"updateAvailable"`
}
type MusicGraphStatus struct {
	Installed  bool      `json:"installed"`
	Snapshot   string    `json:"snapshot"`
	PreparedAt time.Time `json:"preparedAt"`
	Artists    int       `json:"artists"`
	Recordings int       `json:"recordings"`
}
type graphActivation struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

func graphHashValid(hash string) bool {
	b, err := hex.DecodeString(hash)
	return err == nil && len(b) == 32 && hash == strings.ToLower(hash)
}
func graphName(hash string) string    { return "graph-" + hash + ".json" }
func (c *Container) graphDir() string { return filepath.Join(c.cfg.DataDir, "music-graph") }

func (c *Container) activeGraphHash() (string, error) {
	f, err := os.Open(filepath.Join(c.graphDir(), "active.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return "", err
	}
	if len(b) > 4096 {
		return "", errors.New("oversized music graph activation")
	}
	var a graphActivation
	if err = json.Unmarshal(b, &a); err != nil || a.Version != musicgraph.Version || !graphHashValid(a.SHA256) {
		return "", errors.New("invalid music graph activation")
	}
	return a.SHA256, nil
}

// PreparedMusicGraph observes the active marker once and pins an immutable
// snapshot. Optional absence is nil,nil; invalid installed data remains an error.
func (c *Container) PreparedMusicGraph(ctx context.Context) (*musicgraph.Reader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hash, err := c.activeGraphHash()
	if err != nil || hash == "" {
		return nil, err
	}
	return c.PreparedMusicGraphSnapshot(ctx, hash)
}

// PreparedMusicGraphSnapshot loads a retained hash even after activation changes.
// It never substitutes current data when a historical snapshot is unavailable.
func (c *Container) PreparedMusicGraphSnapshot(ctx context.Context, hash string) (*musicgraph.Reader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !graphHashValid(hash) {
		return nil, errors.New("invalid prepared music snapshot identity")
	}
	c.graphMu.Lock()
	r := c.graphReader
	cached := c.graphHash
	c.graphMu.Unlock()
	if r != nil && cached == hash {
		return r, ctx.Err()
	}
	r, err := musicgraph.Open(ctx, filepath.Join(c.graphDir(), graphName(hash)), hash)
	if err != nil {
		return nil, err
	}
	// Keep the active graph warm. Historical pins remain owned by their caller.
	if active, e := c.activeGraphHash(); e == nil && active == hash {
		c.graphMu.Lock()
		c.graphReader = r
		c.graphHash = hash
		c.graphMu.Unlock()
	}
	return r, ctx.Err()
}

func graphStatus(r *musicgraph.Reader) MusicGraphStatus {
	if r == nil {
		return MusicGraphStatus{}
	}
	s := r.Info()
	return MusicGraphStatus{Installed: true, Snapshot: r.SnapshotIdentity(), PreparedAt: s.PreparedAt, Artists: s.Artists, Recordings: s.Recordings}
}
func (c *Container) GetMusicGraphStatus(ctx context.Context) (MusicGraphStatus, error) {
	r, err := c.PreparedMusicGraph(ctx)
	if err != nil {
		return MusicGraphStatus{}, err
	}
	return graphStatus(r), nil
}

// graphInstallLock excludes both another Container and another desktop process.
func (c *Container) graphInstallLock() (func() error, error) {
	if !c.graphInstalling.CompareAndSwap(false, true) {
		return nil, errors.New("music graph installation already running")
	}
	if err := os.MkdirAll(c.graphDir(), 0700); err != nil {
		c.graphInstalling.Store(false)
		return nil, err
	}
	root, err := os.OpenRoot(c.graphDir())
	if err != nil {
		c.graphInstalling.Store(false)
		return nil, err
	}
	defer root.Close()
	f, err := root.OpenFile("install.lock", os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		c.graphInstalling.Store(false)
		return nil, err
	}
	release, err := installlock.TryAcquireFile(f)
	if err != nil {
		c.graphInstalling.Store(false)
		return nil, err
	}
	return func() error { defer c.graphInstalling.Store(false); return release() }, nil
}

func (c *Container) ImportMusicGraph(ctx context.Context, path, expectedSHA256 string) (status MusicGraphStatus, err error) {
	if err = ctx.Err(); err != nil {
		return status, err
	}
	if !graphHashValid(expectedSHA256) {
		return status, errors.New("expected graph SHA256 required")
	}
	release, err := c.graphInstallLock()
	if err != nil {
		return status, err
	}
	defer func() { err = errors.Join(err, release()) }()
	return c.activateMusicGraph(ctx, path, expectedSHA256)
}

func (c *Container) activateMusicGraph(ctx context.Context, path, hash string) (MusicGraphStatus, error) {
	if _, err := musicgraph.Open(ctx, path, hash); err != nil {
		return MusicGraphStatus{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return MusicGraphStatus{}, err
	}
	if err := c.checkGraphInstalledBudget(ctx, hash, info.Size()); err != nil {
		return MusicGraphStatus{}, err
	}
	final := filepath.Join(c.graphDir(), graphName(hash))
	if _, err := os.Lstat(final); errors.Is(err, os.ErrNotExist) {
		tmp, err := os.CreateTemp(c.graphDir(), ".graph-import-*")
		if err != nil {
			return MusicGraphStatus{}, err
		}
		name := tmp.Name()
		defer os.Remove(name)
		in, err := os.Open(path)
		if err != nil {
			tmp.Close()
			return MusicGraphStatus{}, err
		}
		_, err = io.Copy(tmp, io.LimitReader(graphContextReader{ctx, in}, musicgraph.MaxSnapshotBytes+1))
		in.Close()
		if err == nil {
			err = tmp.Sync()
		}
		closeErr := tmp.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return MusicGraphStatus{}, err
		}
		if _, err = musicgraph.Open(ctx, name, hash); err != nil {
			return MusicGraphStatus{}, err
		}
		if err = os.Link(name, final); err != nil {
			return MusicGraphStatus{}, err
		}
	} else if err != nil {
		return MusicGraphStatus{}, err
	}
	r, err := musicgraph.Open(ctx, final, hash)
	if err != nil {
		return MusicGraphStatus{}, err
	}
	marker, err := json.Marshal(graphActivation{Version: musicgraph.Version, SHA256: hash})
	if err != nil {
		return MusicGraphStatus{}, err
	}
	tmp, err := os.CreateTemp(c.graphDir(), ".graph-active-*")
	if err != nil {
		return MusicGraphStatus{}, err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(marker)
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return MusicGraphStatus{}, err
	}
	if err = ctx.Err(); err != nil {
		return MusicGraphStatus{}, err
	}
	if err = os.Rename(tmp.Name(), filepath.Join(c.graphDir(), "active.json")); err != nil {
		return MusicGraphStatus{}, err
	}
	c.graphMu.Lock()
	c.graphReader = r
	c.graphHash = hash
	c.graphMu.Unlock()
	return graphStatus(r), nil
}

type graphContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r graphContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		return n, ctxErr
	}
	return n, err
}

func graphPublicURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsGlobalUnicast() && !ip.IsPrivate()
	}
	return strings.Contains(host, ".") && strings.ContainsAny(host, "abcdefghijklmnopqrstuvwxyz")
}

func graphDownloadClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return errors.New("music graph asset redirects disabled")
	}}
}

func loadGraphManifest(ctx context.Context, source string) (MusicGraphManifest, error) {
	return loadGraphManifestClient(ctx, source, graphDownloadClient())
}
func loadGraphManifestClient(ctx context.Context, source string, client *http.Client) (MusicGraphManifest, error) {
	var m MusicGraphManifest
	if !graphPublicURL(source) {
		return m, errors.New("music graph manifest requires an HTTPS public URL without credentials or query")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return m, err
	}
	req.Header.Set("User-Agent", musicgraph.UserAgent)
	res, err := client.Do(req)
	if err != nil {
		return m, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return m, fmt.Errorf("music graph manifest HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 16385))
	if err != nil {
		return m, err
	}
	if len(b) > 16384 {
		return m, errors.New("oversized music graph manifest")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&m); err != nil {
		return m, err
	}
	if d.Decode(new(any)) != io.EOF {
		return m, errors.New("trailing music graph manifest data")
	}
	if m.Version != musicgraph.Version || m.PreparedAt.IsZero() || !graphHashValid(m.Snapshot.SHA256) || m.Snapshot.Name != graphName(m.Snapshot.SHA256) || m.Snapshot.Size <= 0 || m.Snapshot.Size > musicgraph.MaxSnapshotBytes || !graphPublicURL(m.Snapshot.URL) {
		return m, errors.New("invalid music graph release manifest")
	}
	return m, ctx.Err()
}

func (c *Container) CheckMusicGraphUpdate(ctx context.Context, source string) (MusicGraphUpdate, error) {
	m, err := loadGraphManifest(ctx, source)
	if err != nil {
		return MusicGraphUpdate{}, err
	}
	active, err := c.activeGraphHash()
	if err != nil {
		return MusicGraphUpdate{}, err
	}
	return MusicGraphUpdate{Manifest: m, UpdateAvailable: active != m.Snapshot.SHA256}, nil
}

func (c *Container) InstallMusicGraph(ctx context.Context, source string, p ports.Progress) (status MusicGraphStatus, err error) {
	return c.installMusicGraph(ctx, source, p, graphDownloadClient())
}
func (c *Container) installMusicGraph(ctx context.Context, source string, p ports.Progress, client *http.Client) (status MusicGraphStatus, err error) {
	if err = ctx.Err(); err != nil {
		return status, err
	}
	m, err := loadGraphManifestClient(ctx, source, client)
	if err != nil {
		return status, err
	}
	release, err := c.graphInstallLock()
	if err != nil {
		return status, err
	}
	defer func() { err = errors.Join(err, release()) }()
	if err = c.checkGraphInstalledBudget(ctx, m.Snapshot.SHA256, m.Snapshot.Size); err != nil {
		return status, err
	}
	stage, err := os.MkdirTemp(c.graphDir(), ".graph-download-*")
	if err != nil {
		return status, err
	}
	defer os.RemoveAll(stage)
	if p == nil {
		p = ports.NopProgress{}
	}
	path := filepath.Join(stage, m.Snapshot.Name)
	_, err = dataset.DownloadWithClient(ctx, m.Snapshot.URL, path, m.Snapshot.Size, m.Snapshot.SHA256, func(done, total int64) { p.Report("music-graph", done, total, "Downloading prepared music data") }, client)
	if err != nil {
		return status, err
	}
	r, err := musicgraph.Open(ctx, path, m.Snapshot.SHA256)
	if err != nil {
		return status, err
	}
	if !r.Info().PreparedAt.Equal(m.PreparedAt) {
		return status, errors.New("music graph manifest date mismatch")
	}
	return c.activateMusicGraph(ctx, path, m.Snapshot.SHA256)
}
