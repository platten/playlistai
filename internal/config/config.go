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

	Catalog        CatalogConfig        `toml:"catalog"`
	AI             AIConfig             `toml:"ai"`
	Enrich         EnrichConfig         `toml:"enrich"`
	Preview        PreviewConfig        `toml:"preview"`
	Semantic       SemanticConfig       `toml:"semantic"`
	Recommendation RecommendationConfig `toml:"recommendation"`
}

// SemanticConfig points to an optional offline-built sidecar. The sidecar
// contains the query vocabulary needed by the Go runtime; Python and model
// runtimes are build-time concerns only and are never launched by the app.
type SemanticConfig struct {
	SidecarPath string `toml:"sidecar_path"`
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

// RecommendationConfig controls the bounded exact-retrieval strategy. The
// legacy deejai strategy remains selectable as an evaluation baseline.
type RecommendationConfig struct {
	Strategy                  string  `toml:"strategy"`
	SeedAudioBudget           int     `toml:"seed_audio_budget"`
	SeedCooccurrenceBudget    int     `toml:"seed_cooccurrence_budget"`
	TasteClusterBudget        int     `toml:"taste_cluster_budget"`
	MaxTasteClusters          int     `toml:"max_taste_clusters"`
	ExplorationPool           int     `toml:"exploration_pool"`
	ExplorationBudget         int     `toml:"exploration_budget"`
	ExplorationMinScore       float64 `toml:"exploration_min_score"`
	MaxCandidates             int     `toml:"max_candidates"`
	RetrievalWeight           float64 `toml:"retrieval_weight"`
	ListenerWeight            float64 `toml:"listener_weight"`
	NegativePenalty           float64 `toml:"negative_penalty"`
	ExposurePenalty           float64 `toml:"exposure_penalty"`
	NoveltyWeight             float64 `toml:"novelty_weight"`
	ExplorationChance         float64 `toml:"exploration_chance"`
	ContinuationBudget        int     `toml:"continuation_budget"`
	MMRMinimumLambda          float64 `toml:"mmr_minimum_lambda"`
	SelectionMinimumRelevance float64 `toml:"selection_minimum_relevance"`
	SelectionRelevanceWindow  float64 `toml:"selection_relevance_window"`
	EmbeddingRedundancyWeight float64 `toml:"embedding_redundancy_weight"`
	ArtistConcentrationWeight float64 `toml:"artist_concentration_weight"`
	AlbumConcentrationWeight  float64 `toml:"album_concentration_weight"`
	SoftArtistSpacingMax      int     `toml:"soft_artist_spacing_max"`
	TransitionRelevanceWeight float64 `toml:"transition_relevance_weight"`
	LocalImprovementPasses    int     `toml:"local_improvement_passes"`
	LocalImprovementWindow    int     `toml:"local_improvement_window"`
	SemanticBudget            int     `toml:"semantic_budget"`
	SemanticMinimumScore      float64 `toml:"semantic_minimum_score"`
	SemanticWeight            float64 `toml:"semantic_weight"`
	SemanticNegativePenalty   float64 `toml:"semantic_negative_penalty"`
}

const (
	RecommendationMultichannel = "multichannel"
	RecommendationDeejAI       = "deejai"
)

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
		Recommendation: RecommendationConfig{
			Strategy:        RecommendationMultichannel,
			SeedAudioBudget: 32, SeedCooccurrenceBudget: 32,
			TasteClusterBudget: 24, MaxTasteClusters: 4,
			ExplorationPool: 160, ExplorationBudget: 24, ExplorationMinScore: .10,
			MaxCandidates: 512, RetrievalWeight: .15, ListenerWeight: .30,
			NegativePenalty: .65, ExposurePenalty: .30, NoveltyWeight: .20,
			ExplorationChance:  .35,
			ContinuationBudget: 16, MMRMinimumLambda: .55,
			SelectionMinimumRelevance: .05, SelectionRelevanceWindow: .80,
			EmbeddingRedundancyWeight: .50, ArtistConcentrationWeight: .35,
			AlbumConcentrationWeight: .15, SoftArtistSpacingMax: 3,
			TransitionRelevanceWeight: .15, LocalImprovementPasses: 3, LocalImprovementWindow: 4,
			SemanticBudget: 96, SemanticMinimumScore: .15, SemanticWeight: .35, SemanticNegativePenalty: .55,
		},
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
	if err := c.Recommendation.Validate(); err != nil {
		return err
	}
	return nil
}

func (c RecommendationConfig) Validate() error {
	if c.Strategy != RecommendationMultichannel && c.Strategy != RecommendationDeejAI {
		return fmt.Errorf("config: unknown recommendation.strategy %q", c.Strategy)
	}
	budgets := map[string]int{
		"seed_audio_budget": c.SeedAudioBudget, "seed_cooccurrence_budget": c.SeedCooccurrenceBudget,
		"taste_cluster_budget": c.TasteClusterBudget, "max_taste_clusters": c.MaxTasteClusters,
		"exploration_pool": c.ExplorationPool, "exploration_budget": c.ExplorationBudget,
		"max_candidates":      c.MaxCandidates,
		"continuation_budget": c.ContinuationBudget, "local_improvement_window": c.LocalImprovementWindow,
		"semantic_budget": c.SemanticBudget,
	}
	for name, value := range budgets {
		if value <= 0 {
			return fmt.Errorf("config: recommendation.%s must be positive", name)
		}
	}
	if c.ExplorationMinScore < -1 || c.ExplorationMinScore > 1 {
		return errors.New("config: recommendation.exploration_min_score must be between -1 and 1")
	}
	if c.SemanticMinimumScore < -1 || c.SemanticMinimumScore > 1 {
		return errors.New("config: recommendation.semantic_minimum_score must be between -1 and 1")
	}
	weights := map[string]float64{
		"retrieval_weight": c.RetrievalWeight, "listener_weight": c.ListenerWeight,
		"negative_penalty": c.NegativePenalty, "exposure_penalty": c.ExposurePenalty,
		"novelty_weight":              c.NoveltyWeight,
		"embedding_redundancy_weight": c.EmbeddingRedundancyWeight,
		"artist_concentration_weight": c.ArtistConcentrationWeight,
		"album_concentration_weight":  c.AlbumConcentrationWeight,
		"transition_relevance_weight": c.TransitionRelevanceWeight,
		"semantic_weight":             c.SemanticWeight, "semantic_negative_penalty": c.SemanticNegativePenalty,
	}
	for name, value := range weights {
		if value < 0 {
			return fmt.Errorf("config: recommendation.%s must not be negative", name)
		}
	}
	if c.ExplorationChance < 0 || c.ExplorationChance > 1 {
		return errors.New("config: recommendation.exploration_chance must be between 0 and 1")
	}
	if c.MMRMinimumLambda <= 0 || c.MMRMinimumLambda > 1 {
		return errors.New("config: recommendation.mmr_minimum_lambda must be above 0 and at most 1")
	}
	if c.SelectionMinimumRelevance < -2 || c.SelectionMinimumRelevance > 2 {
		return errors.New("config: recommendation.selection_minimum_relevance must be between -2 and 2")
	}
	if c.SelectionRelevanceWindow <= 0 {
		return errors.New("config: recommendation.selection_relevance_window must be positive")
	}
	if c.SoftArtistSpacingMax < 0 || c.LocalImprovementPasses < 0 {
		return errors.New("config: recommendation spacing and local-improvement values must not be negative")
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
