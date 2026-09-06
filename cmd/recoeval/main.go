// Command recoeval runs versioned offline recommendation evaluation.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/evaluation"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/semantic"
	"github.com/platten/playlistai/internal/similarity/brute"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "recoeval:", err)
		os.Exit(1)
	}
}
func run() error {
	var datasetPath, catalogDir, configPath, outputPath, markdownPath, blindPath, keyPath, left, right, blindSeed string
	var k int
	flag.StringVar(&datasetPath, "dataset", "", "versioned evaluation dataset JSON")
	flag.StringVar(&catalogDir, "catalog", "", "catalog directory override")
	flag.StringVar(&configPath, "config", "", "optional app TOML for semantic sidecar/model")
	flag.StringVar(&outputPath, "output", "evaluation-report.json", "JSON report path")
	flag.StringVar(&markdownPath, "markdown", "evaluation-report.md", "Markdown report path")
	flag.IntVar(&k, "k", 20, "Recall/NDCG cutoff")
	flag.StringVar(&blindPath, "blind-output", "", "optional blind comparison JSON")
	flag.StringVar(&keyPath, "blind-key", "", "separate blind identity key JSON")
	flag.StringVar(&left, "left", "blended_walk", "first blind comparison variant")
	flag.StringVar(&right, "right", "diversity_sequencing", "second blind comparison variant")
	flag.StringVar(&blindSeed, "blind-seed", "1", "deterministic blind randomization seed")
	flag.Parse()
	if datasetPath == "" {
		return fmt.Errorf("-dataset is required")
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
		var encoder ports.TextEmbedder
		if cfg.Semantic.ModelPath != "" {
			encoder = semantic.CommandEncoder{Python: cfg.Semantic.Python, Script: cfg.Semantic.QueryScript, ModelPath: cfg.Semantic.ModelPath, Name: cfg.Semantic.ModelName, Revision: cfg.Semantic.ModelRevision, Dimension: cfg.Semantic.EmbeddingDim}
		}
		store, openErr := semantic.Open(cfg.Semantic.SidecarPath, cat.CatalogVersion(), cat, encoder)
		if openErr != nil {
			return openErr
		}
		featureStore, searcher = store, store
		defer func() { _ = store.Close() }()
	}
	dataset, err := evaluation.LoadDataset(datasetPath)
	if err != nil {
		return err
	}
	runner := evaluation.Runner{Catalog: cat, Resolver: cat, Similarity: sim, Parser: rules.New(), Features: featureStore, Semantic: searcher, K: k}
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
		if err := evaluation.WriteBlindComparison(report, dataset, cat, left, right, blindSeed, blindPath, keyPath); err != nil {
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
func ensureParent(path string) error {
	parent := filepath.Dir(path)
	if parent == "." || parent == "" {
		return nil
	}
	return os.MkdirAll(parent, 0o755)
}
