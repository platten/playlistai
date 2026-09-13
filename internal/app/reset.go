package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/platten/playlistai/internal/config"
)

// ResetAssets closes leased resources before removing app-managed downloads.
// External, manually selected files and personal stores are never traversed.
func (c *Container) ResetAssets() error {
	root, err := resetRoot(c.cfg.DataDir)
	if err != nil {
		return err
	}
	if err = c.Close(); err != nil {
		return err
	}
	prefs, err := config.LoadPrefsChecked(root)
	if err != nil {
		return err
	}
	prefs.OnboardingDone = false
	prefs.ModelPath, prefs.ModelID, prefs.IntentExtractorDir = "", "", ""
	prefs.ModelDisabled = true
	prefs.AnalysisEnabled, prefs.EnhancedAudioEnabled, prefs.IntentAssistEnabled = false, false, false
	if err = prefs.Save(root); err != nil {
		return err
	}
	var failures []error
	for _, name := range []string{"models", "catalog", "metadata", "datasets", "music-analysis", "mert-analysis", "intent-nlu", "model-downloads", "llama", "catalog.tar.zst", "catalog.tar.zst.part"} {
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			failures = append(failures, err)
		}
	}
	// Older installations also kept GGUF downloads directly in the data directory.
	entries, err := os.ReadDir(root)
	if err != nil {
		failures = append(failures, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && (strings.EqualFold(filepath.Ext(entry.Name()), ".gguf") || strings.HasSuffix(strings.ToLower(entry.Name()), ".gguf.part")) {
			if err := os.Remove(filepath.Join(root, entry.Name())); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

// resetRoot permits harmless aliases in ancestor directories (for example,
// macOS /var -> /private/var and Windows short paths) while rejecting a data
// directory that is itself a symlink or junction. RemoveAll never follows
// symlinked children, so deletion remains confined to the named entries below.
func resetRoot(path string) (string, error) {
	root, err := filepath.Abs(path)
	if err != nil || filepath.Dir(root) == root {
		return "", errors.New("invalid application data directory")
	}
	entry, linkErr := os.Lstat(root)
	target, statErr := os.Stat(root)
	if linkErr != nil || statErr != nil || !entry.IsDir() || !target.IsDir() || !os.SameFile(entry, target) {
		return "", errors.New("reset requires an application data directory that is not a symbolic link")
	}
	return root, nil
}
