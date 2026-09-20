package evaluation

import (
	"context"
	"errors"
	"math"
	"sync"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/discoveryasset"
	"github.com/platten/playlistai/internal/localcatalog"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/multichannel"
)

type DiscoveryProvider interface {
	Pin(context.Context) ([]*localcatalog.Catalog, func(), error)
}

type discoveryEvaluation struct {
	provider             DiscoveryProvider
	base                 ports.Catalog
	resolver             ports.ReferenceResolver
	version, fingerprint string
}

// WithDiscovery compares one immutable shared release using production request
// overlays. The returned release must run after reporting and blind export.
// It intentionally installs no network candidate or preview providers.
func (r Runner) WithDiscovery(ctx context.Context, provider DiscoveryProvider) (Runner, func(), error) {
	if r.Catalog == nil || r.Resolver == nil || r.Similarity == nil || provider == nil {
		return r, nil, errors.New("evaluation: base services and shared discovery provider required")
	}
	if r.library != nil || r.discovery != nil {
		return r, nil, errors.New("evaluation: library and discovery comparisons are mutually exclusive")
	}
	base, resolver := r.Catalog, r.Resolver
	d := &discoveryEvaluation{provider: provider, base: base, resolver: resolver}
	overlay, release, fingerprint, err := d.pin(ctx, base, resolver, multichannel.NewRetriever(base, r.Similarity, multichannel.DefaultConfig()))
	if err != nil {
		return r, nil, err
	}
	d.version, d.fingerprint = overlay.Resolver.CatalogVersion(), fingerprint
	r.Catalog, r.Resolver, r.discovery = overlay.Catalog, overlay.Resolver, d
	return r, release, nil
}

func providerVersion(provider DiscoveryProvider) string {
	if source, ok := provider.(interface{ SnapshotID() string }); ok {
		return source.SnapshotID()
	}
	if source, ok := provider.(interface{ Status() discoveryasset.Status }); ok {
		return source.Status().Version
	}
	return ""
}

func (d *discoveryEvaluation) pin(ctx context.Context, base ports.Catalog, resolver ports.ReferenceResolver, retriever ports.CandidateRetriever) (localcatalog.RecommendationOverlay, func(), string, error) {
	version := providerVersion(d.provider)
	packs, release, err := d.provider.Pin(ctx)
	if err != nil {
		return localcatalog.RecommendationOverlay{}, nil, "", err
	}
	var once sync.Once
	closePins := func() {
		once.Do(func() {
			for _, pack := range packs {
				_ = pack.Close()
			}
			if release != nil {
				release()
			}
		})
	}
	if len(packs) == 0 {
		closePins()
		return localcatalog.RecommendationOverlay{}, nil, "", errors.New("evaluation: discovery provider returned no packs")
	}
	provenance := make([]localcatalog.Provenance, 0, len(packs))
	for _, pack := range packs {
		if pack == nil || pack.Provenance().Source != "shared_pack" {
			closePins()
			return localcatalog.RecommendationOverlay{}, nil, "", errors.New("evaluation: discovery requires shared pack catalogs")
		}
		provenance = append(provenance, pack.Provenance())
	}
	fingerprint := fingerprintJSON(struct {
		Version string
		Packs   []localcatalog.Provenance
	}{version, provenance})
	if version != providerVersion(d.provider) || d.fingerprint != "" && fingerprint != d.fingerprint {
		closePins()
		return localcatalog.RecommendationOverlay{}, nil, "", errors.New("evaluation: discovery snapshot changed during evaluation")
	}
	overlay, err := localcatalog.NewDiscoveryOverlay(ctx, packs, base, resolver, retriever, 1)
	if err != nil {
		closePins()
		return localcatalog.RecommendationOverlay{}, nil, "", err
	}
	if d.version != "" && overlay.Resolver.CatalogVersion() != d.version {
		closePins()
		return localcatalog.RecommendationOverlay{}, nil, "", errors.New("evaluation: discovery catalog version changed during evaluation")
	}
	return overlay, closePins, fingerprint, nil
}

func (r Runner) discoveryVariants(parameters ParameterSet) []variant {
	var result []variant
	for _, name := range []string{"discovery_baseline", "discovery_metadata_only", "discovery_mert_only", "discovery_combined"} {
		cfg := configFromParameters(parameters)
		cfg.LibraryEvidenceEnabled = name == "discovery_mert_only" || name == "discovery_combined"
		engine := multichannel.NewWithSemantic(r.discovery.base, r.Similarity, r.discovery.resolver, r.Features, r.Semantic, cfg)
		engine.WithRequestOverlayProvider(func(ctx context.Context, base ports.Catalog, resolver ports.ReferenceResolver, retriever ports.CandidateRetriever) (multichannel.RequestOverlay, error) {
			overlay, release, _, err := r.discovery.pin(ctx, base, resolver, retriever)
			if err != nil {
				return multichannel.RequestOverlay{}, err
			}
			catalog := discoveryAblationCatalog{Catalog: overlay.Catalog, mert: name == "discovery_mert_only" || name == "discovery_combined", dsp: name == "discovery_combined", profiles: name == "discovery_metadata_only" || name == "discovery_combined"}
			selected := overlay.Retriever
			if name == "discovery_baseline" {
				selected = retriever
			} else if name != "discovery_combined" {
				selected = discoveryAblationRetriever{base: selected, mert: name == "discovery_mert_only"}
			}
			return multichannel.RequestOverlay{Catalog: catalog, Resolver: overlay.Resolver, Retriever: selected, Release: release}, nil
		})
		result = append(result, variant{name: name, engine: engine, parameters: parameters, profile: true, control: func(i core.MusicIntent) core.MusicIntent {
			i.Controls.RecommendationMode = core.EnhancedHybrid
			return i
		}})
	}
	return result
}

// Keep recording facts available to the hard eligibility checks while hiding
// only the optional audio ranking/sequencing inputs under evaluation.
type discoveryAblationCatalog struct {
	ports.Catalog
	mert, dsp, profiles bool
}

func (c discoveryAblationCatalog) DiscoveryProfiles(ctx context.Context, intent core.MusicIntent, limit int) ([]core.DiscoveryProfile, error) {
	if source, ok := c.Catalog.(ports.DiscoveryProfileCatalog); ok && c.profiles {
		return source.DiscoveryProfiles(ctx, intent, limit)
	}
	return nil, nil
}

func (c discoveryAblationCatalog) BindRecordingKnowledge(tracks []core.EnrichedTrack) {
	if source, ok := c.Catalog.(interface{ BindRecordingKnowledge([]core.EnrichedTrack) }); ok {
		source.BindRecordingKnowledge(tracks)
	}
}

func (c discoveryAblationCatalog) LibraryVector(ctx context.Context, id string) (core.LibraryVector, bool, error) {
	if source, ok := c.Catalog.(ports.LibraryAudioCatalog); ok && c.mert {
		return source.LibraryVector(ctx, id)
	}
	return core.LibraryVector{}, false, nil
}
func (c discoveryAblationCatalog) LibraryDSPPreference(ctx context.Context, id string, intent core.MusicIntent) (float64, bool) {
	if source, ok := c.Catalog.(ports.LibraryAudioCatalog); ok && c.dsp {
		return source.LibraryDSPPreference(ctx, id, intent)
	}
	return 0, false
}
func (c discoveryAblationCatalog) SupportsCriterion(criterion core.MusicalCriterion) bool {
	if source, ok := c.Catalog.(interface {
		SupportsCriterion(core.MusicalCriterion) bool
	}); ok {
		return source.SupportsCriterion(criterion)
	}
	return false
}
func (c discoveryAblationCatalog) CriterionEvidence(ctx context.Context, id string, criterion core.MusicalCriterion) core.EvidenceState {
	if source, ok := c.Catalog.(interface {
		CriterionEvidence(context.Context, string, core.MusicalCriterion) core.EvidenceState
	}); ok {
		return source.CriterionEvidence(ctx, id, criterion)
	}
	return core.EvidenceUnknown
}
func (c discoveryAblationCatalog) ArtistRecordings(ctx context.Context, artist string) ([]core.TrackRef, error) {
	if source, ok := c.Catalog.(ports.ArtistRecordingCatalog); ok {
		return source.ArtistRecordings(ctx, artist)
	}
	return nil, nil
}

type discoveryAblationRetriever struct {
	base ports.CandidateRetriever
	mert bool
}

func (discoveryAblationRetriever) SupportsIntentMetadata() bool { return true }
func (r discoveryAblationRetriever) Retrieve(ctx context.Context, request ports.RetrievalRequest) ([]core.Candidate, error) {
	candidates, err := r.base.Retrieve(ctx, request)
	if err != nil {
		return nil, err
	}
	result := make([]core.Candidate, 0, len(candidates))
	clusters := map[string]int{}
	for _, candidate := range candidates {
		sources := make([]core.RetrievalEvidence, 0, len(candidate.Sources))
		hadPack := false
		for _, source := range candidate.Sources {
			if source.LibrarySource != nil {
				hadPack = true
				allowed := source.Channel == localcatalog.MetadataChannel && !r.mert || (source.Channel == localcatalog.MERTChannel || source.Channel == localcatalog.ClusterChannel) && r.mert
				if !allowed {
					continue
				}
			}
			sources = append(sources, source)
			if source.Channel == localcatalog.ClusterChannel {
				clusters[source.QueryID]++
			}
		}
		if len(sources) == 0 && hadPack {
			continue
		}
		candidate.Sources = sources
		candidate.Scores.RetrievalFusion = 0
		candidate.Available.RetrievalFusion = false
		result = append(result, candidate)
	}
	var maximum float64
	for i := range result {
		for _, source := range result[i].Sources {
			weight := source.QueryWeight
			if source.Channel == localcatalog.ClusterChannel && clusters[source.QueryID] > 1 {
				weight /= math.Sqrt(float64(clusters[source.QueryID]))
			}
			result[i].Scores.RetrievalFusion += weight / float64(60+max(1, source.Rank))
		}
		maximum = max(maximum, result[i].Scores.RetrievalFusion)
	}
	if maximum > 0 {
		for i := range result {
			if result[i].Scores.RetrievalFusion > 0 {
				result[i].Available.RetrievalFusion = true
				result[i].Scores.RetrievalFusion /= maximum
			}
		}
	}
	return result, nil
}
