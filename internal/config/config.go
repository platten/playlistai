// Package config loads and validates the single Playlist AI configuration file.
// A Config is built once at startup and treated as immutable thereafter.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the whole application configuration.
type Config struct {
	// DataDir holds downloaded assets (llama GGUF, converted catalog) and
	// caches. Defaults to <user config dir>/playlist-ai.
	DataDir string `toml:"data_dir"`

	Catalog CatalogConfig `toml:"catalog"`
	AI      AIConfig      `toml:"ai"`
	Enrich  EnrichConfig  `toml:"enrich"`
	Preview PreviewConfig `toml:"preview"`
}

// CatalogConfig points at the converted Deej-AI dataset.
type CatalogConfig struct {
	// Dir contains vectors.bin + catalog.sqlite once downloaded. Empty until
	// the first-launch download completes.
	Dir string `toml:"dir"`
	// ManifestURL is the hosted catalog-manifest.json used by the first-launch
	// downloader.
	ManifestURL string `toml:"manifest_url"`
}

// AIConfig configures the local llama.cpp intent parser.
type AIConfig struct {
	ModelID         string `toml:"model_id"`          // set after first-run download; blank => rules mode
	ModelPath       string `toml:"model_path"`        // absolute path to the GGUF
	LlamaServerPath string `toml:"llama_server_path"` // blank => look next to the app, then PATH
	NCtx            int    `toml:"n_ctx"`
	NThreads        int    `toml:"n_threads"`  // 0 => auto
	GPULayers       int    `toml:"gpu_layers"` // >0 needs a GPU llama-server build
}

// EnrichConfig configures the MusicBrainz client.
type EnrichConfig struct {
	// UserAgent is sent on every MusicBrainz request and must identify the app
	// with a contact URL (MusicBrainz API requirement).
	UserAgent string `toml:"user_agent"`
	// CachePath is the SQLite file for cached lookups.
	CachePath string `toml:"cache_path"`
	// MirrorURL optionally overrides https://musicbrainz.org.
	MirrorURL string `toml:"mirror_url"`
	// MinScore is the match-score threshold below which a row is flagged for
	// user review (0..100).
	MinScore int `toml:"min_score"`
}

// PreviewConfig selects the preview backend.
type PreviewConfig struct {
	// Provider is "deezer" (default), "spotify", or "off".
	Provider string `toml:"provider"`
}

// Preview provider identifiers.
const (
	PreviewDeezer  = "deezer"
	PreviewSpotify = "spotify"
	PreviewOff     = "off"
)

// Default returns a Config with sensible values rooted at the user's config and
// cache directories. It never returns an error; if the OS dirs are unavailable
// it falls back to "./playlist-ai-data".
func Default() Config {
	data := fallbackDataDir()

	cfg := Config{
		DataDir: data,
		Catalog: CatalogConfig{
			Dir: filepath.Join(data, "catalog"),
		},
		AI: AIConfig{
			NCtx:     4096,
			NThreads: 0,
		},
		Enrich: EnrichConfig{
			UserAgent: "PlaylistAI/0.1 (https://github.com/platten/playlistai)",
			CachePath: filepath.Join(data, "musicbrainz-cache.sqlite"),
			MinScore:  85,
		},
		Preview: PreviewConfig{Provider: PreviewDeezer},
	}
	return cfg
}

// Load reads a TOML file over the defaults. Missing keys keep their default
// value. A missing file is not an error — Default() is returned.
func Load(path string) (Config, error) {
	cfg := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := toml.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks invariants that must hold before the app starts.
func (c Config) Validate() error {
	if c.DataDir == "" {
		return errors.New("config: data_dir is empty")
	}
	switch c.Preview.Provider {
	case PreviewDeezer, PreviewSpotify, PreviewOff:
	default:
		return fmt.Errorf("config: unknown preview.provider %q", c.Preview.Provider)
	}
	if c.AI.NCtx < 512 {
		return fmt.Errorf("config: ai.n_ctx too small (%d)", c.AI.NCtx)
	}
	if c.Enrich.MinScore < 0 || c.Enrich.MinScore > 100 {
		return fmt.Errorf("config: enrich.min_score out of range (%d)", c.Enrich.MinScore)
	}
	if c.Enrich.UserAgent == "" {
		return errors.New("config: enrich.user_agent is required by the MusicBrainz API")
	}
	return nil
}

// LLMReady reports whether a local model is configured and present on disk.
func (c Config) LLMReady() bool {
	if c.AI.ModelPath == "" {
		return false
	}
	info, err := os.Stat(c.AI.ModelPath)
	return err == nil && !info.IsDir() && info.Size() > 0
}

func fallbackDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "playlist-ai")
	}
	if dir, err := os.UserHomeDir(); err == nil && dir != "" {
		return filepath.Join(dir, ".playlist-ai")
	}
	return filepath.Join(".", "playlist-ai-data")
}
