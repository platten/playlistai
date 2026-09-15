package dataset

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/platten/playlistai/internal/installlock"
)

const transactionName = ".catalog-transaction.json"
const stagePrefix = ".catalog-stage-"

type catalogTransaction struct {
	Stage string               `json:"stage"`
	Files []catalogReplacement `json:"files"`
}

type catalogReplacement struct {
	Name     string `json:"name"`
	Previous bool   `json:"previous"`
}

type catalogInstall struct {
	root    *os.Root
	release func() error
	stage   string
	keep    bool // a failed rollback owns the backup until recovery succeeds
}

func beginCatalogInstall(dir string) (*catalogInstall, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	f, err := r.OpenFile(".catalog.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		_ = r.Close()
		return nil, err
	}
	release, err := installlock.TryAcquireFile(f)
	if err != nil {
		_ = r.Close()
		return nil, err
	}
	c := &catalogInstall{root: r, release: release}
	if err := recoverCatalog(r); err != nil {
		c.close()
		return nil, err
	}
	if err := cleanupCatalogStages(r); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

func (c *catalogInstall) prepare() error {
	c.stage = stagePrefix + rand.Text()
	if err := c.root.Mkdir(c.stage, 0o700); err != nil {
		c.stage = ""
		return err
	}
	return writeSynced(c.root, filepath.Join(c.stage, ".owner"), []byte(stageOwnership(c.stage)))
}

func (c *catalogInstall) close() {
	if c.stage != "" && !c.keep {
		_ = c.root.RemoveAll(c.stage)
	}
	_ = c.release()
	_ = c.root.Close()
}

// Recover restores a catalog interrupted during activation. Call this before
// opening its database/vectors, so a crash cannot expose a mixed generation.
// It never follows a journal path outside the selected catalog root.
func Recover(dir string) error {
	// Healthy read-only catalogs do not need an installer lock or a write.
	if _, err := os.Lstat(filepath.Join(dir, transactionName)); errors.Is(err, os.ErrNotExist) {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		r, err := os.OpenRoot(dir)
		if err != nil {
			return err
		}
		found := false
		for _, entry := range entries {
			if ownedCatalogStage(r, entry) {
				found = true
				break
			}
		}
		_ = r.Close()
		if !found {
			return nil
		}
	} else if err != nil {
		return err
	}
	c, err := beginCatalogInstall(dir)
	if err != nil {
		return err
	}
	c.close()
	return nil
}

// ReadLease prevents activation while catalog files or pooled SQLite handles
// are in use. Every successful caller must release the lease after closing them.
func ReadLease(dir string) (func() error, error) {
	for range 3 {
		if err := Recover(dir); err != nil {
			return nil, err
		}
		r, err := os.OpenRoot(dir)
		if err != nil {
			return nil, err
		}
		f, err := r.OpenFile(".catalog.lock", os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			writeErr := err
			f, err = r.Open(".catalog.lock")
			if (errors.Is(writeErr, os.ErrPermission) || errors.Is(writeErr, syscall.EROFS)) && errors.Is(err, os.ErrNotExist) {
				// Legacy immutable catalogs may be mounted read-only without a
				// lock file. This identity cannot install into that directory.
				_, journalErr := r.Lstat(transactionName)
				_ = r.Close()
				if errors.Is(journalErr, os.ErrNotExist) {
					return func() error { return nil }, nil
				}
				return nil, fmt.Errorf("read-only catalog requires recovery: %w", writeErr)
			}
		}
		if err != nil {
			_ = r.Close()
			return nil, err
		}
		release, err := installlock.TryAcquireSharedFile(f)
		if err != nil {
			_ = r.Close()
			return nil, fmt.Errorf("catalog is being installed: %w", err)
		}
		_, journalErr := r.Lstat(transactionName)
		_ = r.Close()
		if errors.Is(journalErr, os.ErrNotExist) {
			return release, nil
		}
		_ = release()
		if journalErr != nil {
			return nil, journalErr
		}
		// A writer could have crashed between recovery and lock acquisition.
	}
	return nil, fmt.Errorf("catalog changed repeatedly during recovery")
}

func validStageName(name string) bool {
	if !strings.HasPrefix(name, stagePrefix) || len(name) != len(stagePrefix)+26 {
		return false
	}
	for _, c := range strings.TrimPrefix(name, stagePrefix) {
		letter, digit := c >= 'A' && c <= 'Z', c >= '2' && c <= '7'
		if !letter && !digit {
			return false
		}
	}
	return true
}

func stageOwnership(name string) string { return "playlistai catalog staging v1\n" + name + "\n" }

func ownedCatalogStage(r *os.Root, entry os.DirEntry) bool {
	name := entry.Name()
	if !validStageName(name) || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
		return false
	}
	marker, err := readCatalogMetadata(r, filepath.Join(name, ".owner"))
	if err == nil {
		return string(marker) == stageOwnership(name)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false
	}
	child, err := r.Open(name)
	if err != nil {
		return false
	}
	files, readErr := child.ReadDir(1)
	closeErr := child.Close()
	return len(files) == 0 && errors.Is(readErr, io.EOF) && closeErr == nil
}

// Called only under the OS lock after journal recovery. A valid operation
// marker (or an empty just-created random stage) establishes ownership.
// Similarly named user directories and symlinks are never recursively removed.
func cleanupCatalogStages(r *os.Root) error {
	dir, err := r.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(-1)
	if err := errors.Join(readErr, dir.Close()); err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if ownedCatalogStage(r, entry) {
			if err := r.RemoveAll(name); err != nil {
				return err
			}
		}
	}
	return nil
}

func readCatalogMetadata(r *os.Root, name string) ([]byte, error) {
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err := errors.Join(readErr, f.Close()); err != nil {
		return nil, err
	}
	if len(raw) > maxManifestBytes {
		return nil, fmt.Errorf("catalog metadata exceeds size budget")
	}
	return raw, nil
}

func regularOrAbsent(r *os.Root, name string) (bool, error) {
	fi, err := r.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !fi.Mode().IsRegular() {
		return false, fmt.Errorf("catalog target %q is not a regular file", name)
	}
	return true, nil
}

func writeSynced(r *os.Root, name string, data []byte) error {
	f, err := r.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}

// publish commits only fully verified staged files. The journal precedes every
// rename; errors and subsequent process starts restore all previous files.
func (c *catalogInstall) publish(names []string) (err error) {
	journal := catalogTransaction{Stage: c.stage}
	for _, name := range names {
		previous, err := regularOrAbsent(c.root, name)
		if err != nil {
			return err
		}
		journal.Files = append(journal.Files, catalogReplacement{Name: name, Previous: previous})
	}
	if err := c.root.Mkdir(filepath.Join(c.stage, ".previous"), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	journalStage := filepath.Join(c.stage, ".transaction.json")
	if err := writeSynced(c.root, journalStage, raw); err != nil {
		return err
	}
	if err := c.root.Rename(journalStage, transactionName); err != nil {
		return err
	}
	c.keep = true
	defer func() {
		if err == nil {
			return
		}
		if rollbackErr := recoverCatalog(c.root); rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("catalog rollback requires recovery: %w", rollbackErr))
		} else {
			c.keep = false
		}
	}()
	for _, f := range journal.Files {
		if f.Previous {
			if err := c.root.Rename(f.Name, filepath.Join(c.stage, ".previous", f.Name)); err != nil {
				return err
			}
		}
	}
	for _, f := range journal.Files {
		if err := c.root.Rename(filepath.Join(c.stage, f.Name), f.Name); err != nil {
			return err
		}
	}
	if err := c.root.Remove(transactionName); err != nil {
		return err
	}
	c.keep = false
	return nil
}

func recoverCatalog(r *os.Root) error {
	raw, err := readCatalogMetadata(r, transactionName)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var j catalogTransaction
	if len(raw) > maxManifestBytes || json.Unmarshal(raw, &j) != nil ||
		!validStageName(j.Stage) ||
		len(j.Files) == 0 || len(j.Files) > maxManifestFiles+1 {
		return fmt.Errorf("invalid catalog recovery journal")
	}
	stage, err := r.Lstat(j.Stage)
	if err != nil {
		return fmt.Errorf("invalid catalog recovery stage: %w", err)
	}
	if !stage.IsDir() || stage.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("catalog recovery stage is not a directory")
	}
	seen := make(map[string]bool)
	for _, entry := range j.Files {
		key := strings.ToLower(entry.Name)
		if !validArtifactName(entry.Name) || seen[key] {
			return fmt.Errorf("invalid recovery filename %q", entry.Name)
		}
		seen[key] = true
	}
	for _, entry := range j.Files {
		backup := filepath.Join(j.Stage, ".previous", entry.Name)
		hasBackup, err := regularOrAbsent(r, backup)
		if err != nil {
			return err
		}
		if !hasBackup && entry.Previous {
			continue
		} // not moved, or already restored
		present, err := regularOrAbsent(r, entry.Name)
		if err != nil {
			return err
		}
		if present {
			if err := r.Remove(entry.Name); err != nil {
				return err
			}
		}
		if hasBackup {
			if err := r.Rename(backup, entry.Name); err != nil {
				return err
			}
		}
	}
	if err := r.Remove(transactionName); err != nil {
		return err
	}
	return r.RemoveAll(j.Stage)
}
