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
	root, err := filepath.Abs(c.cfg.DataDir)
	if err != nil || filepath.Dir(root) == root {
		return errors.New("invalid application data directory")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || !sameSetupPath(root, resolved) {
		return errors.New("reset requires an application data directory without symbolic links")
	}
	if err = c.Close(); err != nil {
		return err
	}
	prefs, err := config.LoadPrefsChecked(c.cfg.DataDir)
	if err != nil {
		return err
	}
	prefs.OnboardingDone = false
	prefs.ModelPath, prefs.ModelID, prefs.IntentExtractorDir = "", "", ""
	prefs.ModelDisabled = true
	prefs.AnalysisEnabled, prefs.EnhancedAudioEnabled, prefs.IntentAssistEnabled = false, false, false
	if err = prefs.Save(c.cfg.DataDir); err != nil {
		return err
	}
	var failures []error
	for _, name := range []string{"models", "catalog", "metadata", "datasets", "music-analysis", "mert-analysis", "intent-nlu", "model-downloads", "llama", "catalog.tar.zst", "catalog.tar.zst.part"} {
		if err := os.RemoveAll(filepath.Join(c.cfg.DataDir, name)); err != nil {
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
