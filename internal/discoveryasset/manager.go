package discoveryasset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/platten/playlistai/internal/dataset"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/localcatalog"
	"github.com/platten/playlistai/internal/ports"
)

type release struct {
	dir      string
	manifest Manifest
	managers []*librarypack.Manager
	refs     int
	retired  bool
	remove   bool
	tracks   int
}
type Manager struct {
	root       string
	mu         sync.Mutex
	installing atomic.Bool
	active     *release
	closed     bool
	problem    string
	syncDir    func(string) error
}
type activeRecord struct {
	Directory string   `json:"directory"`
	Manifest  Manifest `json:"manifest"`
}

func Open(ctx context.Context, root string) (*Manager, error) {
	abs, e := filepath.Abs(root)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(abs, 0700); e != nil {
		return nil, e
	}
	m := &Manager{root: abs}
	b, e := os.ReadFile(filepath.Join(abs, "active.json"))
	if errors.Is(e, os.ErrNotExist) {
		return m, nil
	}
	if e != nil {
		return nil, e
	}
	var record activeRecord
	if e = json.Unmarshal(b, &record); e != nil {
		m.problem = "invalid discovery activation record"
		return m, nil
	}
	if !safeName.MatchString(record.Directory) || !strings.HasPrefix(record.Directory, "release-") {
		m.problem = "invalid discovery release directory"
		return m, nil
	}
	r, e := openRelease(ctx, filepath.Join(abs, record.Directory), record.Manifest)
	if e != nil {
		m.problem = e.Error()
		return m, nil
	}
	m.active = r
	return m, nil
}
func openRelease(ctx context.Context, dir string, manifest Manifest) (*release, error) {
	if e := manifest.Validate(); e != nil {
		return nil, e
	}
	if !manifest.EmbeddedIndexes {
		if e := dataset.VerifyFile(ctx, filepath.Join(dir, manifest.Companion.Name), manifest.Companion.Size, manifest.Companion.SHA256); e != nil {
			return nil, e
		}
		if e := verifyCompanion(ctx, filepath.Join(dir, manifest.Companion.Name), manifest); e != nil {
			return nil, e
		}
	}
	r := &release{dir: dir, manifest: manifest}
	good := false
	defer func() {
		if !good {
			closeRelease(r)
		}
	}()
	for _, f := range manifest.Packs {
		pm, e := librarypack.OpenManager(ctx, filepath.Join(dir, f.PackID), packLimits())
		if e != nil {
			return nil, e
		}
		r.managers = append(r.managers, pm)
		lease, e := pm.Pin()
		if e != nil {
			return nil, e
		}
		g := lease.Generation()
		gm := g.Manifest()
		if gm.PackID != f.PackID || g.PackSHA256() != f.SHA256 {
			lease.Release()
			return nil, errors.New("discoveryasset: installed pack identity mismatch")
		}
		profilePath := filepath.Join(dir, manifest.Companion.Name)
		profileGeneration := manifest.Companion.SHA256
		profileBinding := ""
		if manifest.EmbeddedIndexes {
			if gm.Version != librarypack.IndexedFormatVersion {
				lease.Release()
				return nil, errors.New("discoveryasset: pack lacks prebuilt indexes; re-export with playlist-indexer")
			}
			if e := VerifyEmbeddedCompanion(ctx, g); e != nil {
				lease.Release()
				return nil, e
			}
			profilePath = filepath.Join(g.Directory(), "discovery.sqlite")
			profileBinding, e = embeddedMetadataHash(gm)
			if e != nil {
				lease.Release()
				return nil, e
			}
			for _, embedded := range gm.IndexFiles {
				if embedded.Path == "discovery.sqlite" {
					profileGeneration = embedded.SHA256
				}
			}
		}
		// Opening the catalog verifies that prebuilt retrieval indexes are usable.
		c, e := localcatalog.Open(lease, localcatalog.Options{SourceID: sourceID(f), Shared: true, ProfilePath: profilePath, ProfileGeneration: profileGeneration, ProfileBinding: profileBinding, RequirePrebuilt: true})
		if e != nil {
			return nil, e
		}
		_ = c.Close()
		r.tracks += gm.Coverage.Tracks
	}
	good = true
	return r, nil
}
func packLimits() librarypack.Limits {
	return librarypack.Limits{MaxArchiveBytes: MaxIndexedDownloadBytes, MaxExpandedBytes: 12_000_000_000, MaxMemberBytes: 12_000_000_000}
}
func sourceID(f File) string      { return "discovery-" + f.PackID }
func (m *Manager) Status() Status { m.mu.Lock(); defer m.mu.Unlock(); return m.statusLocked() }

// SnapshotID binds the entire active release manifest, including its companion
// checksum. Human release labels alone need not change when an artifact does.
func (m *Manager) SnapshotID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.active == nil {
		return ""
	}
	raw, _ := json.Marshal(m.active.manifest)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (m *Manager) statusLocked() Status {
	s := Status{PackIDs: []string{}, Error: m.problem}
	if m.active == nil || m.closed {
		return s
	}
	r := m.active
	s.Installed = true
	s.Version = r.manifest.Version
	s.Tracks = r.tracks
	s.Source = r.manifest.Source
	if s.Source == "" {
		s.Source = "hosted"
	}
	s.ManifestDigest = r.manifest.ManifestDigest
	if r.manifest.Source == "local" {
		s.DownloadBytes = 0
	} else if r.manifest.TransportBytes > 0 {
		s.DownloadBytes = r.manifest.TransportBytes
	} else {
		s.DownloadBytes = r.manifest.TotalBytes()
	}
	for _, f := range r.manifest.Packs {
		s.PackIDs = append(s.PackIDs, f.PackID)
	}
	return s
}
func (m *Manager) Install(ctx context.Context, manifestURL string, p ports.Progress) (Status, error) {
	if !m.installing.CompareAndSwap(false, true) {
		return m.Status(), librarypack.ErrMutationInProgress
	}
	defer m.installing.Store(false)
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return m.Status(), librarypack.ErrManagerClosed
	}
	remote, e := fetchRemote(ctx, manifestURL)
	if e != nil {
		return m.Status(), e
	}
	if remote.multipart != nil {
		return m.installMultipart(ctx, remote, p)
	}
	manifest := *remote.discovery
	manifest.Source = "hosted"
	manifest.ManifestDigest = remote.digest
	return m.install(ctx, manifest, p)
}
func (m *Manager) install(ctx context.Context, manifest Manifest, p ports.Progress) (Status, error) {
	if e := manifest.Validate(); e != nil {
		return m.Status(), e
	}
	if p == nil {
		p = ports.NopProgress{}
	}
	if e := checkDisk(m.root, manifest.RequiredDiskBytes()); e != nil {
		return m.Status(), e
	}
	dir, e := os.MkdirTemp(m.root, "release-")
	if e != nil {
		return m.Status(), e
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()
	var done int64
	files := append([]File(nil), manifest.Packs...)
	if !manifest.EmbeddedIndexes {
		files = append(files, manifest.Companion)
	}
	for _, f := range files {
		cached := filepath.Join(m.root, "downloads", f.SHA256)
		if e = dataset.VerifyFile(ctx, cached, f.Size, f.SHA256); e != nil {
			base := done
			_, e = dataset.DownloadWithClient(ctx, f.URL, cached, f.Size, f.SHA256, func(n, _ int64) { p.Report(ProgressOp, base+n, manifest.TotalBytes(), "Downloading "+f.Name) }, downloadClient())
			if e != nil {
				return m.Status(), e
			}
		}
		done += f.Size
		p.Report(ProgressOp, done, manifest.TotalBytes(), "Verifying "+f.Name)
		if f.PackID == "" {
			if e = copyFile(ctx, cached, filepath.Join(dir, f.Name)); e != nil {
				return m.Status(), e
			}
			continue
		}
		limits := packLimits()
		limits.MaxExpandedBytes = f.ExpandedBytes + (4 << 20)
		limits.MaxMemberBytes = f.ExpandedBytes
		pm, err := librarypack.OpenManager(ctx, filepath.Join(dir, f.PackID), limits)
		if err != nil {
			return m.Status(), err
		}
		err = installPack(ctx, pm, cached, f)
		_ = pm.Close()
		if err != nil {
			return m.Status(), err
		}
	}
	status, committed, e := m.activate(ctx, dir, manifest, p)
	keep = committed
	return status, e
}

func (m *Manager) activate(ctx context.Context, dir string, manifest Manifest, p ports.Progress) (Status, bool, error) {
	if p == nil {
		p = ports.NopProgress{}
	}
	r, e := openRelease(ctx, dir, manifest)
	if e != nil {
		return m.Status(), false, e
	}
	keep := false
	defer func() {
		if !keep {
			closeRelease(r)
		}
	}()
	m.mu.Lock()
	locked := true
	defer func() {
		if locked {
			m.mu.Unlock()
		}
	}()
	if m.closed {
		return m.statusLocked(), false, librarypack.ErrManagerClosed
	}
	if e = ctx.Err(); e != nil {
		return m.statusLocked(), false, e
	}
	record := activeRecord{Directory: filepath.Base(dir), Manifest: manifest}
	committed, publishErr := publish(m.root, record, m.syncDir)
	if !committed {
		return m.statusLocked(), false, publishErr
	}
	old := m.active
	m.active = r
	m.problem = ""
	if publishErr != nil {
		m.problem = "discovery release activated but durability could not be confirmed: " + publishErr.Error()
	}
	keep = true
	if old != nil {
		old.retired = true
		// If directory durability is uncertain, either activation record may
		// survive a crash. Preserve both sets until a later verified repair.
		old.remove = publishErr == nil
		m.cleanupLocked(old)
	}
	status := m.statusLocked()
	m.mu.Unlock()
	locked = false
	total := manifest.TotalBytes()
	if manifest.TransportBytes > 0 {
		total = manifest.TransportBytes
	}
	p.Report(ProgressOp, total, total, "Ready")
	return status, true, publishErr
}
func installPack(ctx context.Context, pm *librarypack.Manager, path string, f File) error {
	s, e := pm.Stage(ctx, path)
	if e != nil {
		return e
	}
	activated := false
	defer func() {
		if !activated {
			_ = pm.Discard(s)
		}
	}()
	if s.Manifest().PackID != f.PackID || s.PackSHA256() != f.SHA256 {
		return errors.New("discoveryasset: downloaded pack identity mismatch")
	}
	var expanded int64
	for _, member := range s.Manifest().Files {
		expanded += member.Size
	}
	if expanded != f.ExpandedBytes {
		return errors.New("discoveryasset: expansion size does not match release manifest")
	}
	if e = localcatalog.VerifyPrebuilt(ctx, s.Generation()); e != nil {
		return e
	}
	if e = pm.Activate(ctx, s); e != nil {
		return e
	}
	activated = true
	return nil
}
func (m *Manager) Pin(ctx context.Context) ([]*localcatalog.Catalog, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, nil, e
	}
	if m.closed {
		return nil, nil, librarypack.ErrManagerClosed
	}
	if m.active == nil {
		return nil, nil, librarypack.ErrNoActiveGeneration
	}
	r := m.active
	catalogs := make([]*localcatalog.Catalog, 0, len(r.managers))
	for i, pm := range r.managers {
		lease, e := pm.Pin()
		if e == nil {
			var c *localcatalog.Catalog
			profilePath := filepath.Join(r.dir, r.manifest.Companion.Name)
			profileGeneration := r.manifest.Companion.SHA256
			profileBinding := ""
			if r.manifest.EmbeddedIndexes {
				profilePath = filepath.Join(lease.Generation().Directory(), "discovery.sqlite")
				packManifest := lease.Generation().Manifest()
				profileBinding, e = embeddedMetadataHash(packManifest)
				if e != nil {
					lease.Release()
				}
				for _, embedded := range packManifest.IndexFiles {
					if embedded.Path == "discovery.sqlite" {
						profileGeneration = embedded.SHA256
					}
				}
			}
			if e == nil {
				c, e = localcatalog.Open(lease, localcatalog.Options{SourceID: sourceID(r.manifest.Packs[i]), Shared: true, ProfilePath: profilePath, ProfileGeneration: profileGeneration, ProfileBinding: profileBinding, RequirePrebuilt: true})
			}
			if e == nil {
				catalogs = append(catalogs, c)
			}
		}
		if e != nil {
			for _, c := range catalogs {
				_ = c.Close()
			}
			return nil, nil, e
		}
	}
	r.refs++
	var once sync.Once
	return catalogs, func() {
		once.Do(func() {
			for _, c := range catalogs {
				_ = c.Close()
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			r.refs--
			m.cleanupLocked(r)
		})
	}, nil
}
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	if m.active != nil {
		m.active.retired = true
		m.cleanupLocked(m.active)
		m.active = nil
	}
	return nil
}
func (m *Manager) cleanupLocked(r *release) {
	if !r.retired || r.refs > 0 {
		return
	}
	closeRelease(r)
	if r.remove {
		_ = os.RemoveAll(r.dir)
	}
}
func closeRelease(r *release) {
	for _, pm := range r.managers {
		_ = pm.Close()
	}
	r.managers = nil
}
func publish(root string, record activeRecord, syncDir func(string) error) (bool, error) {
	b, e := json.Marshal(record)
	if e != nil {
		return false, e
	}
	f, e := os.CreateTemp(root, ".activate-")
	if e != nil {
		return false, e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return false, e
	}
	if e = atomicReplaceFile(f.Name(), filepath.Join(root, "active.json")); e != nil {
		return false, fmt.Errorf("discoveryasset: activate: %w", e)
	}
	if syncDir == nil {
		syncDir = syncDirectory
	}
	return true, syncDir(root)
}
