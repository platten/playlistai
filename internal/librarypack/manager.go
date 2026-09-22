package librarypack

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

const activeRecordName = "active.json"

type activeRecord struct {
	Version       int    `json:"version"`
	PackID        string `json:"packId,omitempty"`
	PackSHA256    string `json:"packSha256,omitempty"`
	GenerationDir string `json:"generationDir,omitempty"`
}

type generationEntry struct {
	generation *Generation
	refs       int
	retired    bool
	remove     bool
}

// Manager owns one atomically published generation. Mutations are rejected
// while another Stage/Activate/Discard or Remove transaction is in progress;
// read-only Pin calls remain concurrent.
type Manager struct {
	root     string
	limits   Limits
	mutation atomic.Bool
	mu       sync.Mutex
	active   *generationEntry
	closed   bool
}

func OpenManager(ctx context.Context, root string, limits Limits) (*Manager, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(abs, "generations"), 0o700); err != nil {
		return nil, err
	}
	m := &Manager{root: abs, limits: limits.normalized()}
	raw, err := os.ReadFile(filepath.Join(abs, activeRecordName))
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	var record activeRecord
	decoderErr := json.Unmarshal(raw, &record)
	if decoderErr != nil || record.Version != 1 {
		return nil, errors.New("librarypack: invalid active generation record")
	}
	if record.PackID == "" {
		if record.PackSHA256 != "" || record.GenerationDir != "" {
			return nil, errors.New("librarypack: invalid empty active generation")
		}
		return m, nil
	}
	if !validHash(record.PackID) || !validHash(record.PackSHA256) || filepath.Base(record.GenerationDir) != record.GenerationDir || !strings.HasPrefix(record.GenerationDir, record.PackID+"-") {
		return nil, errors.New("librarypack: invalid active generation identity")
	}
	dir := filepath.Join(abs, "generations", record.GenerationDir)
	generation, err := openGeneration(ctx, dir, record.PackSHA256, m.limits)
	if err != nil {
		return nil, fmt.Errorf("librarypack: open active generation: %w", err)
	}
	if generation.manifest.PackID != record.PackID {
		_ = generation.close()
		return nil, errors.New("librarypack: active record does not match generation")
	}
	m.active = &generationEntry{generation: generation}
	return m, nil
}

// Staged is a verified generation awaiting one atomic activation. The caller
// must call Activate or Discard to release the manager's mutation slot.
type Staged struct {
	manager    *Manager
	entry      *generationEntry
	manifest   Manifest
	packSHA256 string
	dir        string
	sameActive bool
	done       atomic.Bool
}

func (s *Staged) Manifest() Manifest {
	if s == nil {
		return Manifest{}
	}
	m := s.manifest
	m.Files = append([]File(nil), m.Files...)
	m.IndexFiles = append([]IndexedFile(nil), m.IndexFiles...)
	m.RootAliases = append([]string(nil), m.RootAliases...)
	return m
}
func (s *Staged) PackSHA256() string {
	if s == nil {
		return ""
	}
	return s.packSHA256
}

// Generation returns the verified generation while it is staged so bounded
// packaged indexes can be verified before the short activation step. Offline
// tooling may also build indexes for a staged source pack. For an identical
// active generation it returns that immutable generation.
func (s *Staged) Generation() *Generation {
	if s == nil || s.entry == nil {
		return nil
	}
	return s.entry.generation
}

func (m *Manager) Stage(ctx context.Context, archivePath string) (*Staged, error) {
	if !m.mutation.CompareAndSwap(false, true) {
		return nil, ErrMutationInProgress
	}
	success := false
	defer func() {
		if !success {
			m.mutation.Store(false)
		}
	}()
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return nil, ErrManagerClosed
	}
	stageDir, err := os.MkdirTemp(m.root, ".stage-*")
	if err != nil {
		return nil, err
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.RemoveAll(stageDir)
		}
	}()
	archiveCopy := filepath.Join(stageDir, ".source.paipack")
	packSHA, err := copyArchive(ctx, archivePath, archiveCopy, m.limits)
	if err != nil {
		return nil, err
	}
	manifest, extractedSHA, err := extractArchive(ctx, archiveCopy, stageDir, m.limits)
	if err != nil {
		return nil, err
	}
	if extractedSHA != packSHA {
		return nil, errors.New("librarypack: staged archive changed while validating")
	}
	if err := os.Remove(archiveCopy); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrManagerClosed
	}
	if m.active != nil && m.active.generation.manifest.PackID == manifest.PackID && m.active.generation.packSHA256 == packSHA {
		entry := m.active
		entry.refs++ // pin while app-owned derivative validation/rebuild runs
		m.mu.Unlock()
		success = true
		return &Staged{manager: m, entry: entry, manifest: manifest, packSHA256: packSHA, dir: entry.generation.dir, sameActive: true}, nil
	}
	m.mu.Unlock()
	destination := filepath.Join(m.root, "generations", manifest.PackID+"-"+strings.TrimPrefix(filepath.Base(stageDir), ".stage-"))
	if err := os.Rename(stageDir, destination); err != nil {
		return nil, err
	}
	keepStage = true
	if err := syncDirectory(filepath.Dir(destination)); err != nil {
		_ = os.RemoveAll(destination)
		return nil, err
	}
	generation, err := openGeneration(ctx, destination, packSHA, m.limits)
	if err != nil {
		_ = os.RemoveAll(destination)
		return nil, err
	}
	if generation.manifest.PackID != manifest.PackID {
		_ = generation.close()
		_ = os.RemoveAll(destination)
		return nil, errors.New("librarypack: installed generation changed during staging")
	}
	success = true
	return &Staged{manager: m, entry: &generationEntry{generation: generation}, manifest: manifest, packSHA256: packSHA, dir: destination}, nil
}

func copyArchive(ctx context.Context, sourceName, targetName string, limits Limits) (string, error) {
	info, err := os.Stat(sourceName)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limits.MaxArchiveBytes {
		return "", errors.New("librarypack: archive is not a bounded regular file")
	}
	source, err := os.Open(sourceName)
	if err != nil {
		return "", err
	}
	defer source.Close()
	target, err := os.OpenFile(targetName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(target, hasher), &contextReader{ctx: ctx, reader: io.LimitReader(source, limits.MaxArchiveBytes+1)})
	syncErr := target.Sync()
	closeErr := target.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if syncErr != nil {
		return "", syncErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if written != info.Size() || written > limits.MaxArchiveBytes {
		return "", errors.New("librarypack: archive changed or exceeded its limit while staging")
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// Activate durably publishes the staged generation before exposing it to new
// Pin calls. Existing leases retain their old immutable generation.
func (m *Manager) Activate(ctx context.Context, staged *Staged) error {
	if staged == nil || staged.manager != m || !staged.done.CompareAndSwap(false, true) {
		return errors.New("librarypack: invalid or completed staged generation")
	}
	defer m.mutation.Store(false)
	if staged.sameActive {
		m.release(staged.entry)
		return nil
	}
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		_ = staged.entry.generation.close()
		_ = os.RemoveAll(staged.dir)
		return ErrManagerClosed
	}
	if err := ctx.Err(); err != nil {
		_ = staged.entry.generation.close()
		_ = os.RemoveAll(staged.dir)
		return err
	}
	if err := m.publish(activeRecord{Version: 1, PackID: staged.manifest.PackID, PackSHA256: staged.packSHA256, GenerationDir: filepath.Base(staged.dir)}); err != nil {
		_ = staged.entry.generation.close()
		_ = os.RemoveAll(staged.dir)
		return err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = staged.entry.generation.close()
		return ErrManagerClosed
	}
	old := m.active
	m.active = staged.entry
	if old != nil {
		old.retired, old.remove = true, old.generation.dir != staged.dir
	}
	m.mu.Unlock()
	m.cleanup(old)
	return nil
}

func (m *Manager) Discard(staged *Staged) error {
	if staged == nil || staged.manager != m || !staged.done.CompareAndSwap(false, true) {
		return errors.New("librarypack: invalid or completed staged generation")
	}
	defer m.mutation.Store(false)
	if staged.entry == nil {
		return nil
	}
	if staged.sameActive {
		m.release(staged.entry)
		return nil
	}
	err := staged.entry.generation.close()
	m.mu.Lock()
	activeDir := ""
	if m.active != nil {
		activeDir = m.active.generation.dir
	}
	m.mu.Unlock()
	if staged.dir != "" && staged.dir != activeDir {
		err = errors.Join(err, os.RemoveAll(staged.dir))
	}
	return err
}

type Lease struct {
	manager *Manager
	entry   *generationEntry
	once    sync.Once
}

func (l *Lease) Generation() *Generation {
	if l == nil || l.entry == nil {
		return nil
	}
	return l.entry.generation
}
func (l *Lease) Release() {
	if l != nil {
		l.once.Do(func() { l.manager.release(l.entry) })
	}
}

func (m *Manager) Pin() (*Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrManagerClosed
	}
	if m.active == nil {
		return nil, ErrNoActiveGeneration
	}
	m.active.refs++
	return &Lease{manager: m, entry: m.active}, nil
}

// Remove atomically publishes an empty active record. Pinned readers keep the
// removed generation until Release; only managed derivative files are deleted.
func (m *Manager) Remove(ctx context.Context) error {
	if !m.mutation.CompareAndSwap(false, true) {
		return ErrMutationInProgress
	}
	defer m.mutation.Store(false)
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return ErrManagerClosed
	}
	if err := m.publish(activeRecord{Version: 1}); err != nil {
		return err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrManagerClosed
	}
	old := m.active
	m.active = nil
	if old != nil {
		old.retired, old.remove = true, true
	}
	m.mu.Unlock()
	m.cleanup(old)
	return nil
}

func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	old := m.active
	m.active = nil
	if old != nil {
		old.retired, old.remove = true, false
	}
	m.mu.Unlock()
	m.cleanup(old)
	return nil
}

func (m *Manager) release(entry *generationEntry) {
	m.mu.Lock()
	if entry.refs > 0 {
		entry.refs--
	}
	ready := entry.retired && entry.refs == 0
	m.mu.Unlock()
	if ready {
		m.cleanup(entry)
	}
}

func (m *Manager) cleanup(entry *generationEntry) {
	if entry == nil {
		return
	}
	m.mu.Lock()
	if !entry.retired || entry.refs != 0 || entry.generation == nil {
		m.mu.Unlock()
		return
	}
	generation := entry.generation
	entry.generation = nil
	remove := entry.remove
	m.mu.Unlock()
	_ = generation.close()
	if remove {
		_ = os.RemoveAll(generation.dir)
	}
}

func (m *Manager) publish(record activeRecord) error {
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(m.root, ".active-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(append(raw, '\n')); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := atomicReplaceFile(name, filepath.Join(m.root, activeRecordName)); err != nil {
		return err
	}
	return syncDirectory(m.root)
}
