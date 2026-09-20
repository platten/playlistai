// Command recoeval runs versioned offline recommendation evaluation.
package main

import (
	"context"
	flagpkg "flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/discoveryasset"
	"github.com/platten/playlistai/internal/evaluation"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/localcatalog"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/semantic"
	"github.com/platten/playlistai/internal/similarity/brute"
)

func main() {
	if err := run(); err != nil && err != flagpkg.ErrHelp {
		fmt.Fprintln(os.Stderr, "recoeval:", err)
		os.Exit(1)
	}
}
func run() error {
	flag := flagpkg.NewFlagSet("recoeval", flagpkg.ContinueOnError)
	var datasetPath, catalogDir, configPath, outputPath, markdownPath, blindPath, keyPath, left, right, blindSeed string
	var k int
	var packPath, libraryMode, discoveryState string
	flag.StringVar(&discoveryState, "discovery-state", "", "installed shared discovery manager directory for offline baseline/metadata/MERT/combined comparison")
	flag.StringVar(&packPath, "paipack", "", "optional pack for production library evidence off/on comparison")
	flag.StringVar(&libraryMode, "library-mode", "combined", "paipack source policy: combined or library_only")
	flag.StringVar(&datasetPath, "dataset", "", "versioned evaluation dataset JSON")
	flag.StringVar(&catalogDir, "catalog", "", "catalog directory override")
	flag.StringVar(&configPath, "config", "", "optional app TOML for a semantic sidecar")
	flag.StringVar(&outputPath, "output", "evaluation-report.json", "JSON report path")
	flag.StringVar(&markdownPath, "markdown", "evaluation-report.md", "Markdown report path")
	flag.IntVar(&k, "k", 20, "Recall/NDCG cutoff")
	flag.StringVar(&blindPath, "blind-output", "", "optional blind comparison JSON")
	flag.StringVar(&keyPath, "blind-key", "", "separate blind identity key JSON")
	flag.StringVar(&left, "left", "blended_walk", "first blind comparison variant")
	flag.StringVar(&right, "right", "diversity_sequencing", "second blind comparison variant")
	flag.StringVar(&blindSeed, "blind-seed", "1", "deterministic blind randomization seed")
	if err := flag.Parse(os.Args[1:]); err != nil {
		return err
	}
	if datasetPath == "" {
		return fmt.Errorf("-dataset is required")
	}
	if discoveryState != "" && packPath != "" {
		return fmt.Errorf("-discovery-state and -paipack are mutually exclusive")
	}
	if packPath != "" && libraryMode != string(localcatalog.ModeCombined) && libraryMode != string(localcatalog.ModeLibraryOnly) {
		return fmt.Errorf("-library-mode must be combined or library_only")
	}
	if packPath != "" || discoveryState != "" {
		explicit := map[string]bool{}
		flag.Visit(func(f *flagpkg.Flag) { explicit[f.Name] = true })
		if !explicit["left"] {
			left = "library_evidence_off"
		}
		if !explicit["right"] {
			right = "library_evidence_on"
		}
		if discoveryState != "" {
			if !explicit["left"] {
				left = "discovery_baseline"
			}
			if !explicit["right"] {
				right = "discovery_combined"
			}
		}
	}
	cfg := config.Default()
	var err error
	if configPath != "" {
		cfg, err = config.Load(configPath)
		if err != nil {
			return err
		}
	}
	if catalogDir != "" {
		cfg.Catalog.Dir = catalogDir
	}
	if cfg.Catalog.Dir == "" {
		return fmt.Errorf("-catalog or catalog.dir is required")
	}
	cat, err := catalog.Open(cfg.Catalog.Dir)
	if err != nil {
		return err
	}
	defer cat.Close()
	sim := brute.New(cat)
	var featureStore ports.FeatureStore
	var searcher ports.SemanticSearcher
	if cfg.Semantic.SidecarPath != "" {
		store, openErr := semantic.Open(cfg.Semantic.SidecarPath, cat.CatalogVersion(), cat)
		if openErr != nil {
			return openErr
		}
		featureStore = store
		if store.SearchReady() {
			searcher = store
		}
		defer func() { _ = store.Close() }()
	}
	dataset, err := evaluation.LoadDataset(datasetPath)
	if err != nil {
		return err
	}
	runner := evaluation.Runner{Catalog: cat, Resolver: cat, Similarity: sim, Parser: rules.New(), Features: featureStore, Semantic: searcher, K: k}
	if discoveryState != "" {
		if _, err := os.Stat(filepath.Join(discoveryState, "active.json")); err != nil {
			return fmt.Errorf("installed discovery activation record: %w", err)
		}
		manager, err := discoveryasset.Open(context.Background(), discoveryState)
		if err != nil {
			return err
		}
		defer manager.Close()
		var release func()
		runner, release, err = runner.WithDiscovery(context.Background(), manager)
		if err != nil {
			return err
		}
		defer release()
	}
	if packPath != "" {
		// Evaluation owns an isolated temporary import. Never mutate the user's
		// active library, mappings, listening history, or indexing state.
		root, err := os.MkdirTemp("", "playlist-recoeval-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(root)
		manager, err := librarypack.OpenManager(context.Background(), root, librarypack.DefaultLimits())
		if err != nil {
			return err
		}
		defer manager.Close()
		staged, err := manager.Stage(context.Background(), packPath)
		if err != nil {
			return err
		}
		if err = localcatalog.BuildIndexes(context.Background(), staged.Generation(), localcatalog.IndexBuildOptions{Workers: 1}); err != nil {
			return err
		}
		if err = manager.Activate(context.Background(), staged); err != nil {
			return err
		}
		var release func()
		runner, release, err = runner.WithLibrary(context.Background(), evaluationPackProvider{manager}, localcatalog.RecommendationMode(libraryMode))
		if err != nil {
			return err
		}
		defer release()
	}
	report, err := runner.Run(context.Background(), dataset)
	if err != nil {
		return err
	}
	if err := ensureParent(outputPath); err != nil {
		return err
	}
	if err := evaluation.WriteReportJSON(outputPath, report); err != nil {
		return err
	}
	if markdownPath != "" {
		if err := ensureParent(markdownPath); err != nil {
			return err
		}
		if err := evaluation.WriteReportMarkdown(markdownPath, report); err != nil {
			return err
		}
	}
	if blindPath != "" || keyPath != "" {
		if blindPath == "" || keyPath == "" {
			return fmt.Errorf("-blind-output and -blind-key must be supplied together")
		}
		if err := ensureParent(blindPath); err != nil {
			return err
		}
		if err := ensureParent(keyPath); err != nil {
			return err
		}
		if err := evaluation.WriteBlindComparison(report, dataset, runner.Catalog, left, right, blindSeed, blindPath, keyPath); err != nil {
			return err
		}
	}
	fmt.Printf("wrote %s", outputPath)
	if markdownPath != "" {
		fmt.Printf(" and %s", markdownPath)
	}
	fmt.Println()
	return nil
}

type evaluationPackProvider struct{ manager *librarypack.Manager }

func (p evaluationPackProvider) PinLocalCatalog() (*localcatalog.Catalog, error) {
	lease, err := p.manager.Pin()
	if err != nil {
		return nil, err
	}
	return localcatalog.Open(lease, localcatalog.Options{SourceID: "library"})
}
func ensureParent(path string) error {
	parent := filepath.Dir(path)
	if parent == "." || parent == "" {
		return nil
	}
	return os.MkdirAll(parent, 0o755)
}
