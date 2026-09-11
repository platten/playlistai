package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/dataset"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/deejai"
	"github.com/platten/playlistai/internal/reco/multichannel"
	"github.com/platten/playlistai/internal/semantic"
	"github.com/platten/playlistai/internal/similarity/brute"
)

// RuntimeSnapshot is published only after all catalog-dependent services are
// ready. Callers receive a value snapshot; the service references are immutable
// for the lifetime of this container.
type RuntimeSnapshot struct {
	Catalog      ports.Catalog
	Resolver     ports.ReferenceResolver
	Sim          ports.SimilarityEngine
	Reco         ports.RecommendationEngine
	BaselineReco ports.RecommendationEngine
	Features     ports.FeatureStore
}

func (c *Container) Runtime() RuntimeSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runtime
}

// LoadCatalog opens the catalog in the configured directory and wires the
// similarity + recommendation engines. No-op if already loaded; returns an
// error without mutating the container if the directory has no valid catalog.
func (c *Container) LoadCatalog() error {
	c.catalogLoadMu.Lock()
	defer c.catalogLoadMu.Unlock()
	return c.loadCatalog()
}

func (c *Container) loadCatalog() error {
	c.mu.Lock()
	closed, loaded := c.closed, c.runtime.Catalog != nil
	c.mu.Unlock()
	if closed {
		return errors.New("application is closed")
	}
	if loaded {
		return nil
	}
	cat, err := catalog.Open(c.cfg.Catalog.Dir)
	if err != nil {
		return err
	}
	runtime := RuntimeSnapshot{Catalog: cat, Resolver: cat, Sim: brute.New(cat)}
	closers := []func() error{cat.Close}
	runtime.BaselineReco = deejai.New(cat, runtime.Sim, cat)
	{
		// Wire both engines once. Settings choose a request-local engine without
		// swapping shared services while a generation is running.
		rc := c.cfg.Recommendation
		mc := multichannel.DefaultConfig()
		mc.SeedAudioBudget = rc.SeedAudioBudget
		mc.SeedCooccurrenceBudget = rc.SeedCooccurrenceBudget
		mc.TasteClusterBudget = rc.TasteClusterBudget
		mc.MaxTasteClusters = rc.MaxTasteClusters
		mc.ExplorationPool = rc.ExplorationPool
		mc.ExplorationBudget = rc.ExplorationBudget
		mc.ExplorationMinScore = rc.ExplorationMinScore
		mc.MaxCandidates = rc.MaxCandidates
		mc.RetrievalWeight = rc.RetrievalWeight
		mc.ListenerWeight = rc.ListenerWeight
		mc.NegativePenalty = rc.NegativePenalty
		mc.ExposurePenalty = rc.ExposurePenalty
		mc.NoveltyWeight = rc.NoveltyWeight
		mc.ExplorationChance = rc.ExplorationChance
		mc.ContinuationBudget = rc.ContinuationBudget
		mc.MMRMinimumLambda = rc.MMRMinimumLambda
		mc.SelectionMinimumRelevance = rc.SelectionMinimumRelevance
		mc.SelectionRelevanceWindow = rc.SelectionRelevanceWindow
		mc.EmbeddingRedundancyWeight = rc.EmbeddingRedundancyWeight
		mc.ArtistConcentrationWeight = rc.ArtistConcentrationWeight
		mc.AlbumConcentrationWeight = rc.AlbumConcentrationWeight
		mc.SoftArtistSpacingMax = rc.SoftArtistSpacingMax
		mc.TransitionRelevanceWeight = rc.TransitionRelevanceWeight
		mc.LocalImprovementPasses = rc.LocalImprovementPasses
		mc.LocalImprovementWindow = rc.LocalImprovementWindow
		mc.SemanticBudget = rc.SemanticBudget
		mc.SemanticMinimumScore = rc.SemanticMinimumScore
		mc.SemanticWeight = rc.SemanticWeight
		mc.SemanticNegativePenalty = rc.SemanticNegativePenalty
		var semanticSearch ports.SemanticSearcher
		if semanticCfg := c.cfg.Semantic; semanticCfg.SidecarPath != "" {
			store, openErr := semantic.Open(semanticCfg.SidecarPath, cat.CatalogVersion(), cat)
			if openErr != nil {
				c.log.Warn("semantic sidecar unavailable; continuing without semantic matching", "err", openErr)
			} else {
				runtime.Features = store
				if store.SearchReady() {
					semanticSearch = store
				}
				closers = append(closers, store.Close)
				info := store.Info()
				c.log.Info("semantic sidecar loaded", "tracks", info.TrackCount, "feature_version", info.FeatureVersion, "model", info.TextModel, "query_encoder", info.QueryEncoder)
			}
		}
		if runtime.Features != nil {
			// Feature-only sidecars still enforce grounded constraints even when
			// they do not contain a compatible query encoder.
			runtime.Reco = multichannel.NewWithSemantic(cat, runtime.Sim, cat, runtime.Features, semanticSearch, mc).WithAudioProvider(c.AudioService).WithAnchorProposer(c.ProposeAnchors)
		} else {
			runtime.Reco = multichannel.New(cat, runtime.Sim, cat, mc).WithAudioProvider(c.AudioService).WithAnchorProposer(c.ProposeAnchors)
		}
		if source, ok := c.Knowledge.(ports.MusicCandidateSource); ok {
			runtime.Reco.(*multichannel.Orchestrator).WithCandidateSource(source)
		}
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		for i := len(closers) - 1; i >= 0; i-- {
			_ = closers[i]()
		}
		return errors.New("application closed while loading catalog")
	}
	c.runtime = runtime
	c.closers = append(c.closers, closers...)
	c.mu.Unlock()
	c.log.Info("catalog loaded", "tracks", cat.Len(), "dim", cat.Dim())
	return nil
}

// EnsureCatalog gets the catalog onto disk and loads it, if it is not already
// present. In order:
//
//  1. a pre-packaged catalog.tar.zst staged next to the app (bundle_path or
//     beside the executable) — decompress it, no network;
//  2. cfg.Catalog.ArchiveURL — download the compressed archive (resumable,
//     checksummed), then decompress it;
//  3. cfg.Catalog.ManifestURL — download the two raw files.
//
// Progress is reported via p under the "catalog" op throughout, so the caller
// (the first-run gate) doesn't need to know which path ran.
func (c *Container) EnsureCatalog(ctx context.Context, p ports.Progress) error {
	ctx, release := c.OperationContext(ctx)
	defer release()
	c.catalogLoadMu.Lock()
	defer c.catalogLoadMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.Runtime().Catalog != nil {
		return nil
	}
	// Already unpacked on disk from a previous run? Load it — no download,
	// no decompress.
	if err := c.loadCatalog(); err == nil {
		return nil
	}
	cat := c.cfg.Catalog

	if archive, ok := dataset.FindBundledArchive(cat.BundlePath); ok {
		if err := dataset.Unpack(ctx, archive, cat.Dir, p); err != nil {
			return fmt.Errorf("app: unpack bundled catalog: %w", err)
		}
		return c.loadCatalog()
	}

	if cat.ArchiveURL != "" {
		archive := filepath.Join(c.cfg.DataDir, "catalog.tar.zst")
		if err := dataset.DownloadArchive(ctx, cat.ArchiveURL, archive, cat.ArchiveSize, cat.ArchiveSHA256, p); err != nil {
			return fmt.Errorf("app: download catalog: %w", err)
		}
		if err := dataset.Unpack(ctx, archive, cat.Dir, p); err != nil {
			return fmt.Errorf("app: unpack catalog: %w", err)
		}
		if err := c.loadCatalog(); err != nil {
			return err
		}
		_ = os.Remove(archive) // decompressed copy is what we use from here
		return nil
	}

	if cat.ManifestURL == "" {
		return fmt.Errorf("app: no catalog source configured (set catalog.archive_url, catalog.manifest_url, or catalog.dir)")
	}
	m, err := dataset.LoadManifest(ctx, cat.ManifestURL)
	if err != nil {
		return err
	}
	if err := dataset.Fetch(ctx, cat.Dir, m, p); err != nil {
		return err
	}
	return c.loadCatalog()
}

// CatalogBundled reports whether a pre-packaged, compressed catalog is staged
// next to the app and ready to be unpacked by EnsureCatalog — used by the
// bridge to tell the frontend whether "get the catalog" means an instant
// local decompression (auto-run, no user action) or a network download
// (user-initiated, per cfg.Catalog.ManifestURL).
func (c *Container) CatalogBundled() bool {
	_, ok := dataset.FindBundledArchive(c.cfg.Catalog.BundlePath)
	return ok
}
