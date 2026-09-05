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
	// Dir contains vectors.i8 + catalog.sqlite once the catalog is set up.
	// Empty until the first-launch download + decompress completes.
	Dir string `toml:"dir"`
	// ArchiveURL is the compressed catalog (catalog.tar.zst) the app fetches
	// on first launch, then decompresses into Dir. Defaults to a hosted copy;
	// override to self-host. ArchiveSize / ArchiveSHA256 are integrity checks
	// for that download — clear them if you point ArchiveURL at a different
	// build.
	ArchiveURL    string `toml:"archive_url"`
	ArchiveSize   int64  `toml:"archive_size"`
	ArchiveSHA256 string `toml:"archive_sha256"`
	// ManifestURL is an alternative first-launch source: a hosted
	// catalog-manifest.json listing the two raw files (used when ArchiveURL
	// is empty).
	ManifestURL string `toml:"manifest_url"`
	// BundlePath overrides where to look for a pre-packaged, compressed
	// catalog.tar.zst staged next to the running executable. Blank means "look
	// next to the executable". Set only for testing or a nonstandard layout.
	BundlePath string `toml:"bundle_path"`
}

// AIConfig configures the local llama.cpp intent parser.
type AIConfig struct {
	ModelID   string `toml:"model_id"`   // set after first-run download; blank => rules mode
	ModelPath string `toml:"model_path"` // absolute path to the GGUF
	// LlamaServerPath is an explicit path to `llama-server` or the unified
	// `llama` binary. Blank => auto-detect (next to the app, ~/.local/bin,
	// ~/.llama-app, PATH). The first-run wizard installs llama.cpp via
	// ggml-org's official installer if none is found.
	LlamaServerPath string `toml:"llama_server_path"`
	NCtx            int    `toml:"n_ctx"`
	NThreads        int    `toml:"n_threads"` // 0 => auto
	// GPULayers: 0 => the runtime's default (a GPU build already offloads
	// everything); >0 => pin that many layers to the GPU; <0 => force CPU.
	GPULayers int `toml:"gpu_layers"`
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
			// Deej-AI catalog (~957k tracks), tar+zstd, ~210 MB. Hosted on
			// Cloudflare R2; the first-run wizard downloads + decompresses it.
			// Size + hash pin the exact build.
			ArchiveURL:    "https://pub-233adf724b7e476db67cf787cd301c9e.r2.dev/catalog.tar.zst",
			ArchiveSize:   219618902,
			ArchiveSHA256: "f1198694f6c63bcb891e3a8461d3da97acf66d998ecd8a2998c8b23ea9f6a7e6",
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
