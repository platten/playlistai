package multichannel

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type sourceCatalogResolver struct {
	ports.ReferenceResolver
	version string
}

func (r sourceCatalogResolver) CatalogVersion() string { return r.version }

func TestLibraryEvidenceExperimentChangesGenerationVersion(t *testing.T) {
	cat, _ := enhancedFixture(t)
	cfg := DefaultConfig()
	baseline := New(cat, nil, cat, cfg).AlgorithmVersion()
	cfg.LibraryEvidenceEnabled = true
	experiment := New(cat, nil, cat, cfg).AlgorithmVersion()
	if baseline == experiment || !strings.Contains(experiment, "+library-evidence/v1") {
		t.Fatalf("experiment is absent from replay identity: baseline=%q experiment=%q", baseline, experiment)
	}
}

func TestCompositeOverlayPreservesExternalMERTAndReplay(t *testing.T) {
	cat, input := enhancedFixture(t)
	meta, _ := cat.Meta("c")
	hit := core.AudioRepresentation{ID: "mert-c", TrackID: "c", TrackKey: core.ProvisionalRecordingKey(meta.Ref), CatalogVersion: input.CatalogVersion, Model: input.Model, Pooled: []float32{1, 0}}
	input.Representations["c"] = hit
	engine := New(cat, nil, cat, DefaultConfig())
	engine.retriever = &mertRefillRetriever{cat: cat, pages: [][]string{{"a"}, {"b"}}}
	packVersion := "pack-one"
	engine.WithRequestOverlayProvider(func(_ context.Context, catalog ports.Catalog, resolver ports.ReferenceResolver, retriever ports.CandidateRetriever) (RequestOverlay, error) {
		return RequestOverlay{Catalog: catalog, Resolver: sourceCatalogResolver{resolver, resolver.CatalogVersion() + "+local-library:" + packVersion}, Retriever: retriever}, nil
	})
	searchCalls := 0
	engine.WithMERTSimilaritySearchProvider(func(_ context.Context, _ core.MusicIntent, _ core.TasteProfile, queries []core.MERTSimilarityQuery, _ map[string]struct{}, _ int) (*core.MERTSimilaritySearch, error) {
		searchCalls++
		if len(queries) != 1 || queries[0].Track.ID != "seed" {
			t.Fatalf("unexpected queries: %+v", queries)
		}
		queries[0].RepresentationID = "mert-seed"
		return &core.MERTSimilaritySearch{Recorded: true, CatalogVersion: input.CatalogVersion, Model: input.Model,
			ViewFingerprint: "external-view", SearchableTracks: 3, Queries: queries,
			Hits: []core.MERTSimilarityHit{{GroupID: queries[0].GroupID, QueryTrackID: "seed", TrackID: "c", Rank: 1, Score: .99, QueryWeight: 1, Representation: hit}}}, nil
	})
	engine.WithEnhancedAudioProvider(func(context.Context, core.MusicIntent, core.TasteProfile, []core.TrackRef) (*core.EnhancedAudioSnapshot, error) {
		return core.NewEnhancedAudioSnapshot(input)
	})
	intent := testIntent(3)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(playlist.Tracks) != 3 || playlist.EnhancedAudio == nil {
		t.Fatalf("external neighbor lost with local overlay: tracks=%v enhanced=%v", playlist.Tracks, playlist.EnhancedAudio)
	}
	if playlist.EvidenceCatalogVersion != input.CatalogVersion+"+local-library:pack-one" || playlist.EnhancedAudio.Input().CatalogVersion != input.CatalogVersion {
		t.Fatalf("request/source identities conflated: request=%q source=%q", playlist.EvidenceCatalogVersion, playlist.EnhancedAudio.Input().CatalogVersion)
	}
	found := false
	for _, reason := range playlist.Rationale {
		for _, source := range reason.Sources {
			found = found || reason.TrackID == "c" && source.Channel == ChannelMERTAudio
		}
	}
	if !found {
		t.Fatal("external MERT provenance lost")
	}
	// Pack replacement changes request identity but does not invalidate frozen
	// external evidence. History's request fingerprint separately records it.
	packVersion = "pack-two"
	replayed, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, EnhancedAudio: playlist.EnhancedAudio})
	if err != nil || searchCalls != 1 || !reflect.DeepEqual(replayed.Tracks, playlist.Tracks) {
		t.Fatalf("external evidence replay changed: err=%v searches=%d tracks=%v", err, searchCalls, replayed.Tracks)
	}
	if replayed.EvidenceCatalogVersion != input.CatalogVersion+"+local-library:pack-two" {
		t.Fatalf("replacement fingerprint missing: %q", replayed.EvidenceCatalogVersion)
	}
	if engine.sourceCatalogVersion != "" || engine.resolver.CatalogVersion() != cat.CatalogVersion() {
		t.Fatal("request-local overlay mutated reusable engine")
	}
	wrong := playlist.EnhancedAudio.Input()
	wrong.CatalogVersion = "different-base"
	_, err = engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, EnhancedAudio: freezeEnhanced(t, wrong)})
	if err == nil || !strings.Contains(err.Error(), "another catalog") {
		t.Fatalf("incompatible external evidence accepted: %v", err)
	}
}

type sourceCatalogHiddenTrack struct {
	ports.Catalog
	hidden string
}

func (c sourceCatalogHiddenTrack) Meta(id string) (core.TrackMeta, bool) {
	if id == c.hidden {
		return core.TrackMeta{}, false
	}
	return c.Catalog.Meta(id)
}

func TestExternalMERTSourceCompatibilityStillRequiresVisibleMembership(t *testing.T) {
	cat, input := enhancedFixture(t)
	meta, _ := cat.Meta("c")
	search := &core.MERTSimilaritySearch{Recorded: true, CatalogVersion: input.CatalogVersion, Model: input.Model,
		Queries: []core.MERTSimilarityQuery{{GroupID: "reference", Track: core.TrackRef{ID: "seed"}, Weight: 1, RepresentationID: "seed-vector"}},
		Hits: []core.MERTSimilarityHit{{GroupID: "reference", QueryTrackID: "seed", TrackID: "c", Rank: 1, Score: 1,
			Representation: core.AudioRepresentation{ID: "c-vector", TrackID: "c", TrackKey: core.ProvisionalRecordingKey(meta.Ref), CatalogVersion: input.CatalogVersion, Model: input.Model, Pooled: []float32{1, 0}}}}}
	engine := New(cat, nil, cat, DefaultConfig())
	engine.sourceCatalogVersion = input.CatalogVersion
	engine.resolver = sourceCatalogResolver{cat, "local-library:pack"}
	engine.cat = sourceCatalogHiddenTrack{cat, "c"}
	if got := engine.mertCandidates(search); len(got) != 0 {
		t.Fatalf("source-compatible external hit bypassed library membership: %+v", got)
	}
	engine.cat = cat
	engine.sourceCatalogVersion = "different-base"
	if got := engine.mertCandidates(search); len(got) != 0 {
		t.Fatalf("incompatible external source accepted: %+v", got)
	}
}

func TestCompositeOverlayReusesExternalCLAPCache(t *testing.T) {
	cat := testCatalog()
	service, previews := cachedAudioService(t, cat)
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
	engine.WithRequestOverlayProvider(func(_ context.Context, catalog ports.Catalog, resolver ports.ReferenceResolver, retriever ports.CandidateRetriever) (RequestOverlay, error) {
		return RequestOverlay{Catalog: catalog, Resolver: sourceCatalogResolver{resolver, resolver.CatalogVersion() + "+local-library:pack"}, Retriever: retriever}, nil
	})
	intent := testIntent(2)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "ambient electronica", Scope: "playlist"}}
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_style", Value: "rock"}}
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(playlist.Tracks) == 0 || playlist.AudioEvidence == nil || playlist.AudioEvidence.CacheHits == 0 || previews.calls != 0 {
		t.Fatalf("pack overlay invalidated cached external CLAP: tracks=%v audio=%+v preview calls=%d", playlist.Tracks, playlist.AudioEvidence, previews.calls)
	}
}
