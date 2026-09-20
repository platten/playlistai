package evaluation

import (
	"context"
	"errors"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/localcatalog"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/multichannel"
)

type libraryEvaluation struct {
	provider localcatalog.PinProvider
	base     ports.Catalog
	resolver ports.ReferenceResolver
	mode     localcatalog.RecommendationMode
	version  string
	feedback ports.Catalog
}

// WithLibrary pins evidence for resolution, feedback and diagnostics. Each
// production generation takes its own lease and rejects a changed snapshot.
// The returned release must run after Run and blind-output generation.
func (r Runner) WithLibrary(ctx context.Context, provider localcatalog.PinProvider, mode localcatalog.RecommendationMode) (Runner, func(), error) {
	if r.discovery != nil {
		return r, nil, errors.New("evaluation: library and discovery comparisons are mutually exclusive")
	}
	if r.Catalog == nil || r.Resolver == nil || r.Similarity == nil {
		return r, nil, errors.New("evaluation: base services required before library overlay")
	}
	base, resolver := r.Catalog, r.Resolver
	retriever := multichannel.NewRetriever(base, r.Similarity, multichannel.DefaultConfig())
	overlay, err := localcatalog.PinRecommendationOverlay(ctx, provider, base, resolver, retriever, mode, 1)
	if err != nil {
		return r, nil, err
	}
	r.Catalog, r.Resolver = overlay.Catalog, overlay.Resolver
	feedback, err := localcatalog.PinRecommendationOverlay(ctx, provider, base, resolver, retriever, localcatalog.ModeCombined, 1)
	if err != nil {
		overlay.Close()
		return r, nil, err
	}
	expected := r.Resolver.CatalogVersion()
	if mode == localcatalog.ModeLibraryOnly {
		expected = resolver.CatalogVersion() + "+" + expected
	}
	if feedback.Resolver.CatalogVersion() != expected {
		feedback.Close()
		overlay.Close()
		return r, nil, errors.New("evaluation: library snapshot changed while pinning feedback")
	}
	r.library = &libraryEvaluation{provider: provider, base: base, resolver: resolver, mode: mode, version: r.Resolver.CatalogVersion(), feedback: feedback.Catalog}
	return r, func() { feedback.Close(); overlay.Close() }, nil
}

func (r Runner) libraryVariants(parameters ParameterSet) []variant {
	var result []variant
	for _, enabled := range []bool{false, true} {
		cfg := configFromParameters(parameters)
		cfg.LibraryEvidenceEnabled = enabled
		engine := multichannel.NewWithSemantic(r.library.base, r.Similarity, r.library.resolver, r.Features, r.Semantic, cfg)
		engine.WithRequestOverlayProvider(func(ctx context.Context, base ports.Catalog, resolver ports.ReferenceResolver, retriever ports.CandidateRetriever) (multichannel.RequestOverlay, error) {
			overlay, err := localcatalog.PinRecommendationOverlay(ctx, r.library.provider, base, resolver, retriever, r.library.mode, 1)
			if err != nil {
				return multichannel.RequestOverlay{}, err
			}
			if overlay.Resolver.CatalogVersion() != r.library.version {
				overlay.Close()
				return multichannel.RequestOverlay{}, errors.New("evaluation: library snapshot changed during evaluation")
			}
			return multichannel.RequestOverlay{Catalog: overlay.Catalog, Resolver: overlay.Resolver, Retriever: overlay.Retriever, Release: overlay.Close}, nil
		})
		name := "library_evidence_off"
		if enabled {
			name = "library_evidence_on"
		}
		result = append(result, variant{name: name, engine: engine, parameters: parameters, profile: true, control: func(i core.MusicIntent) core.MusicIntent {
			i.Controls.RecommendationMode = core.EnhancedHybrid
			return i
		}})
	}
	return result
}
