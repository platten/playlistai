package librarysearch

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var ErrNoActiveGeneration = errors.New("librarysearch: no active generation")

type generationRef struct {
	index   *Index
	readers int
	retired bool
}

// Manager atomically swaps complete immutable generations. Handles pin their
// generation until Release; retired mapped files are closed but never deleted
// by this package, allowing the caller to apply platform-specific cleanup.
type Manager struct {
	root      string
	publishMu sync.Mutex
	mu        sync.Mutex
	active    *generationRef
	retired   []string
	closeErr  error
	closed    bool
}

type Handle struct {
	manager *Manager
	ref     *generationRef
	once    sync.Once
}

func OpenManager(ctx context.Context, root string) (*Manager, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	manager := &Manager{root: root}
	raw, err := os.ReadFile(filepath.Join(root, "active"))
	if errors.Is(err, os.ErrNotExist) {
		return manager, nil
	}
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(string(raw))
	if !safeGeneration(name) {
		return nil, errors.New("librarysearch: invalid active generation pointer")
	}
	index, err := Open(ctx, filepath.Join(root, name))
	if err != nil {
		return nil, err
	}
	manager.active = &generationRef{index: index}
	return manager, nil
}

func (m *Manager) Activate(ctx context.Context, generationDir string) error {
	m.publishMu.Lock()
	defer m.publishMu.Unlock()
	absoluteRoot, err := filepath.Abs(m.root)
	if err != nil {
		return err
	}
	absoluteGeneration, err := filepath.Abs(generationDir)
	if err != nil {
		return err
	}
	name := filepath.Base(absoluteGeneration)
	if filepath.Dir(absoluteGeneration) != absoluteRoot || !safeGeneration(name) {
		return errors.New("librarysearch: generation must be a direct managed child")
	}
	index, err := Open(ctx, absoluteGeneration)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		_ = index.Close()
		return errors.New("librarysearch: generation manager is closed")
	}
	if m.active != nil && m.active.index.manifest.Generation == index.manifest.Generation {
		_ = index.Close()
		return nil
	}
	if err := writeActivePointer(m.root, name); err != nil {
		_ = index.Close()
		return err
	}
	previous := m.active
	m.active = &generationRef{index: index}
	if previous != nil {
		previous.retired = true
		m.closeRetiredLocked(previous)
	}
	return nil
}

func writeActivePointer(root, name string) error {
	f, err := os.CreateTemp(root, ".active-")
	if err != nil {
		return err
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err = io.WriteString(f, name+"\n"); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(path, filepath.Join(root, "active")); err != nil {
		return err
	}
	return syncDirectory(root)
}

func (m *Manager) Pin() (*Handle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.active == nil {
		return nil, ErrNoActiveGeneration
	}
	m.active.readers++
	return &Handle{manager: m, ref: m.active}, nil
}

func (h *Handle) Manifest() Manifest { return h.ref.index.Manifest() }

func (h *Handle) Search(ctx context.Context, query Query) ([]Hit, error) {
	if h == nil || h.ref == nil {
		return nil, ErrNoActiveGeneration
	}
	return h.ref.index.Search(ctx, query)
}

func (h *Handle) Release() {
	if h == nil || h.manager == nil {
		return
	}
	h.once.Do(func() {
		h.manager.mu.Lock()
		defer h.manager.mu.Unlock()
		h.ref.readers--
		h.manager.closeRetiredLocked(h.ref)
	})
}

func (m *Manager) closeRetiredLocked(ref *generationRef) {
	if !ref.retired || ref.readers != 0 || ref.index == nil {
		return
	}
	path := ref.index.Path()
	m.closeErr = errors.Join(m.closeErr, ref.index.Close())
	ref.index = nil
	m.retired = append(m.retired, path)
}

// CollectRetired returns generation directories whose mapped readers have all
// closed. The caller may remove these paths; this method never deletes files.
func (m *Manager) CollectRetired() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]string(nil), m.retired...)
	m.retired = nil
	return out
}

func (m *Manager) Close() error {
	m.publishMu.Lock()
	defer m.publishMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		m.closed = true
		if m.active != nil {
			m.active.retired = true
			m.closeRetiredLocked(m.active)
			m.active = nil
		}
	}
	return m.closeErr
}
