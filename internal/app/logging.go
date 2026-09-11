package app

import "github.com/platten/playlistai/internal/config"

// DebugLogging reports whether the user opted into potentially sensitive,
// memory-only recommendation diagnostics.
func (c *Container) DebugLogging() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return config.LoadPrefs(c.cfg.DataDir).DebugLogging
}

// SetDebugLogging persists the diagnostic preference. The bridge owns the
// in-memory log store and applies the setting immediately after this succeeds.
func (c *Container) SetDebugLogging(enabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefs, err := config.LoadPrefsChecked(c.cfg.DataDir)
	if err != nil {
		return err
	}
	prefs.DebugLogging = enabled
	return prefs.Save(c.cfg.DataDir)
}
