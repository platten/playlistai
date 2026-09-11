package multichannel

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

// These annotations are synthetic algorithm fixtures, not real music judgments.
func TestSingleGenreScreensEveryRecommendation(t *testing.T) {
	for _, mode := range []core.RecommendationMode{core.AcousticBrainzFirst, core.CLAPFirst} {
		for _, policy := range []core.VerificationPolicy{core.BestAvailable, core.VerifiedOnly} {
			t.Run(string(mode)+"/"+string(policy), func(t *testing.T) {
				cat := fakes.NewCatalog(2,
					fakes.CatalogTrack{ID: "seed", Display: "Seed - Origin", Audio: []float32{1, 0}, Track: []float32{1, 0}},
					fakes.CatalogTrack{ID: "audio", Display: "A - One", Audio: []float32{1, 0}, Track: []float32{1, 0}},
					fakes.CatalogTrack{ID: "cooc", Display: "B - Two", Audio: []float32{1, 0}, Track: []float32{1, 0}},
					fakes.CatalogTrack{ID: "other", Display: "C - Three", Audio: []float32{1, 0}, Track: []float32{1, 0}},
					fakes.CatalogTrack{ID: "taste", Display: "D - Four", Audio: []float32{0, 1}, Track: []float32{1, 0}},
					fakes.CatalogTrack{ID: "unknown", Display: "Electronic - Classical", Audio: []float32{1, 0}, Track: []float32{1, 0}},
				)
				features := &semanticFixture{info: core.FeatureStoreInfo{SupportedFacets: []string{"styles"}}, features: map[string]core.TrackFeatures{
					"audio": completeStyleFeature("audio", "electronic"),
					"cooc":  completeStyleFeature("cooc", "techno"),
					"other": completeStyleFeature("other", "rock & roll"),
					"taste": completeStyleFeature("taste", "rock"),
				}}
				engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, features, nil, DefaultConfig())
				intent := testIntent(5)
				intent.VerificationPolicy = policy
				intent.Controls.RecommendationMode = mode
				intent.Controls.Discovery, intent.Controls.ArtistDiversity = 1, 1
				intent.Preferences.Genres = []core.IntentPreference{{Value: "electronica", Influence: core.InfluencePositive}}
				profile := core.TasteProfile{Clusters: []core.TasteCluster{{ID: "rock", Weight: 1, Affinity: core.EmbeddingAffinity{Audio: []float32{0, 1}, Cooccurrence: []float32{1, 0}}}}}
				pl, err := engine.BuildWithProfile(context.Background(), intent, profile)
				if err != nil || len(pl.Tracks) != 2 || pl.Outcome.State != core.OutcomePartial {
					t.Fatalf("genre screening: tracks=%v outcome=%+v err=%v", pl.Tracks, pl.Outcome, err)
				}
				for _, track := range pl.Tracks {
					if track.ID != "audio" && track.ID != "cooc" {
						t.Fatalf("unverified recording escaped: %+v", track)
					}
				}
				intent.RequiredTracks = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "other", Influence: core.InfluencePositive}}
				pl, err = engine.Build(context.Background(), intent)
				if err != nil || len(pl.Tracks) != 0 || pl.Outcome.State != core.OutcomeNeedsClarification {
					t.Fatalf("required mismatch accepted: %+v %v", pl, err)
				}
			})
		}
	}
}

func TestSingleGenreUnknownIsNotBestAvailableFallback(t *testing.T) {
	cat := testCatalog()
	intent := testIntent(3)
	intent.VerificationPolicy = core.BestAvailable
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "classical", Scope: "playlist"}}
	pl, err := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(pl.Tracks) != 0 || pl.Outcome.State == core.OutcomeFulfilled {
		t.Fatalf("unknown tracks returned: %+v %v", pl, err)
	}
	found := false
	for _, r := range pl.Outcome.Reasons {
		found = found || r.Code == "single_genre_evidence_exhausted"
	}
	if !found {
		t.Fatal("missing actionable genre coverage explanation", pl.Outcome)
	}
}

func TestSingleGenreMetadataRequiresIdentityAndProvenance(t *testing.T) {
	cat := testCatalog()
	o := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	o.bestAvailable = true
	o.knowledge = &core.KnowledgeSnapshot{}
	for _, id := range []string{"audio", "cooc", "other"} {
		meta, _ := cat.Meta(id)
		source := "reviewed-fixture"
		if id == "cooc" {
			source = ""
		}
		status := core.ResolutionResolved
		if id == "other" {
			status = core.ResolutionAmbiguous
		}
		o.knowledge.Tracks = append(o.knowledge.Tracks, core.EnrichedTrack{Ref: meta.Ref, IdentityStatus: status, GenreTags: []core.AttributedGenreTag{{Name: "techno", Source: source, Votes: 3}}})
	}
	got, _, err := o.filterEssential(context.Background(), candidatesForTracks(refs(cat, "audio", "cooc", "other", "last")), []core.MusicalCriterion{{Kind: "genre", Value: "electronic", Scope: "playlist"}})
	if err != nil || len(got) != 1 || got[0].Track.ID != "audio" {
		t.Fatalf("identity/provenance bypass: %+v %v", got, err)
	}
}

func TestSingleGenreEvidenceFallbacks(t *testing.T) {
	cat := testCatalog()
	for _, kind := range []string{"genre", "style"} {
		t.Run(kind, func(t *testing.T) {
			store := &semanticFixture{info: core.FeatureStoreInfo{SupportedFacets: []string{"styles"}}, features: map[string]core.TrackFeatures{"audio": {TrackID: "audio"}}, positive: []core.SemanticHit{{TrackID: "cooc", Score: .9}}}
			o := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, store, store, DefaultConfig())
			o.bestAvailable = true
			o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{{Ref: refs(cat, "audio")[0], IdentityStatus: core.ResolutionResolved, GenreTags: []core.AttributedGenreTag{{Name: "classical", Votes: 2, Source: "reviewed-fixture"}}}}}
			criteria := []core.MusicalCriterion{{Kind: kind, Value: "classical", Scope: "playlist"}}
			got, _, err := o.filterEssential(context.Background(), candidatesForTracks(refs(cat, "audio", "cooc", "other")), criteria)
			if err != nil || len(got) != 2 {
				t.Fatalf("grounded fallback lost: %+v %v", got, err)
			}
			o.bestAvailable = false
			got, _, err = o.filterEssential(context.Background(), candidatesForTracks(refs(cat, "audio", "cooc", "other")), criteria)
			if err != nil || len(got) != 2 {
				t.Fatalf("verified-only discarded grounded fallback: %+v %v", got, err)
			}
			store.coverage = &core.QueryCoverage{Complete: false}
			got, _, err = o.filterEssential(context.Background(), candidatesForTracks(refs(cat, "cooc")), criteria)
			if err != nil || len(got) != 0 {
				t.Fatal("incomplete semantic query passed genre check", got, err)
			}
		})
	}
}
