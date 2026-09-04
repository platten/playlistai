package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Prefs are the small set of settings the app writes at runtime (as opposed to
// the read-only TOML config). Persisted to <DataDir>/prefs.json.
type Prefs struct {
	// ModelPath is the GGUF the user chose for the local parser. Empty → rules.
	ModelPath string `json:"modelPath"`
	// ModelID is the catalog id when the model came from the built-in catalog.
	ModelID string `json:"modelId"`
}

func prefsPath(dataDir string) string { return filepath.Join(dataDir, "prefs.json") }

// LoadPrefs reads prefs.json, returning a zero value on any error.
func LoadPrefs(dataDir string) Prefs {
	b, err := os.ReadFile(prefsPath(dataDir))
	if err != nil {
		return Prefs{}
	}
	var p Prefs
	if json.Unmarshal(b, &p) != nil {
		return Prefs{}
	}
	return p
}

// Save writes prefs.json (best-effort; creates the dir if needed).
func (p Prefs) Save(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(prefsPath(dataDir), append(b, '\n'), 0o644)
}
