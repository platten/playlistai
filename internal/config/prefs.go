package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Prefs are the small set of settings the app writes at runtime (as opposed to
// the read-only TOML config). Persisted to <DataDir>/prefs.json.
//
// Save replaces the whole file, so any code that sets one field must load the
// current Prefs first and mutate just that field — never construct a fresh
// Prefs{} with only the field it cares about, or it silently erases the rest.
type Prefs struct {
	RecommendationMode   string `json:"recommendationMode,omitempty"`
	AnalysisEnabled      bool   `json:"analysisEnabled"`
	EnhancedAudioEnabled bool   `json:"enhancedAudioEnabled"`
	IntentAssistEnabled  bool   `json:"intentAssistEnabled,omitempty"`
	IntentExtractorDir   string `json:"intentExtractorDir,omitempty"`
	// DebugLogging opts into potentially sensitive, memory-only diagnostics.
	DebugLogging bool `json:"debugLogging,omitempty"`
	// ModelPath is the GGUF the user chose for the local parser. Empty → rules.
	ModelPath string `json:"modelPath"`
	// ModelDisabled preserves an explicit rules-parser choice over a model in
	// the read-only TOML config. Absent in legacy preferences means no override.
	ModelDisabled bool `json:"modelDisabled,omitempty"`
	// ModelID is the catalog id when the model came from the built-in catalog.
	ModelID string `json:"modelId"`
	// PreviewProvider overrides preview.provider from the TOML config when set
	// ("deezer" | "spotify" | "off"). Empty → use the TOML value.
	PreviewProvider string `json:"previewProvider"`
	// OnboardingDone marks the first-run wizard as complete (or skipped).
	OnboardingDone bool `json:"onboardingDone"`
}

func prefsPath(dataDir string) string { return filepath.Join(dataDir, "prefs.json") }

// LoadPrefs reads prefs.json, returning a zero value on any error.
func LoadPrefs(dataDir string) Prefs {
	p, _ := LoadPrefsChecked(dataDir)
	return p
}

// LoadPrefsChecked distinguishes first launch from unreadable or corrupt
// preferences so callers can avoid overwriting settings they could not load.
func LoadPrefsChecked(dataDir string) (Prefs, error) {
	b, err := os.ReadFile(prefsPath(dataDir))
	if os.IsNotExist(err) {
		return Prefs{}, nil
	}
	if err != nil {
		return Prefs{}, fmt.Errorf("read preferences: %w", err)
	}
	var p Prefs
	if err := json.Unmarshal(b, &p); err != nil {
		return Prefs{}, fmt.Errorf("decode preferences: %w", err)
	}
	return p, nil
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
	f, err := os.CreateTemp(dataDir, ".prefs-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), prefsPath(dataDir))
}
