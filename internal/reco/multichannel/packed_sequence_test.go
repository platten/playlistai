package multichannel

import (
	"context"
	"math"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type packedSequenceFixture struct {
	libraryEvidenceFixture
	clap        map[string]core.LibraryVector
	assessments map[string]core.AudioAssessment
}

func (c packedSequenceFixture) BindLibraryQueries(core.AudioModelIdentity, []core.AudioClauseVector) {
}
func (c packedSequenceFixture) LibraryAssessment(ctx context.Context, id string) (core.AudioAssessment, bool, error) {
	a, ok := c.assessments[id]
	return a, ok, ctx.Err()
}
func (c packedSequenceFixture) LibraryCLAPVector(ctx context.Context, id string) (core.LibraryVector, bool, error) {
	v, ok := c.clap[id]
	return v, ok, ctx.Err()
}

func TestPackedTransitionsContributeWithDeejAndRespectSpaces(t *testing.T) {
	vector := func(space string, values ...float32) core.LibraryVector {
		return core.LibraryVector{Source: core.LibraryEvidenceSource{SpaceID: space}, Values: values}
	}
	cat := testCatalog()
	cfg := DefaultConfig()
	cfg.LibraryEvidenceEnabled = true
	s := NewSequencer(cat, cfg)
	s.libraryVectors = map[string]core.LibraryVector{"seed": vector("mert", 1, 0), "audio": vector("mert", -1, 0)}
	s.libraryCLAPVectors = map[string]core.LibraryVector{"seed": vector("clap", 1, 0), "audio": vector("clap", 1, 0)}
	intent := testIntent(2)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	left, right := refs(cat, "seed", "audio")[0], refs(cat, "seed", "audio")[1]
	a, _ := cat.Vectors(left.ID)
	b, _ := cat.Vectors(right.ID)
	base, _ := weightedVectorSimilarity(a, b, intent.Controls.AudioWeight, intent.Controls.CooccurrenceWeight)
	got, ok := s.trackSimilarity(left, right, intent)
	want := (base - cfg.EnhancedMERTWeight + cfg.EnhancedTransitionWeight) / (1 + cfg.EnhancedMERTWeight + cfg.EnhancedTransitionWeight)
	if !ok || math.Abs(got-want) > 1e-9 {
		t.Fatalf("packed evidence ignored with Deej present: %g != %g", got, want)
	}
	s.libraryVectors["audio"] = vector("other-mert", 1, 0)
	s.libraryCLAPVectors["audio"] = vector("other-clap", 1, 0)
	got, _ = s.trackSimilarity(left, right, intent)
	want = base / (1 + cfg.EnhancedMERTWeight + cfg.EnhancedTransitionWeight)
	if math.Abs(got-want) > 1e-9 {
		t.Fatal("incompatible spaces compared", got, want)
	}
	intent.Controls.RecommendationMode = core.DeejAIOnly
	got, _ = s.trackSimilarity(left, right, intent)
	if math.Abs(got-base) > 1e-9 {
		t.Fatal("packed transition leaked into Deej mode")
	}
}

func TestEnhancedPackedCandidatesSurviveExpiredPreviewBudget(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(map[bool]string{false: "soft", true: "strict"}[strict], func(t *testing.T) {
			base, service, retriever := recommendationPoolFixture(t, 2, 0)
			store := &interruptedAnalysisStore{AnalysisStore: service.Store}
			service.Store = store
			intent := testIntent(2).Normalized()
			intent.Controls.RecommendationMode = core.EnhancedHybrid
			intent.Preferences.Moods = []core.IntentPreference{{Value: "dreamy", Influence: core.InfluencePositive, Scope: "playlist"}}
			if strict {
				intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "mood", Value: "dreamy", Scope: "playlist", Strength: "required"}}
			}
			cat := packedSequenceFixture{libraryEvidenceFixture: libraryEvidenceFixture{Catalog: base}, assessments: map[string]core.AudioAssessment{}}
			for _, id := range []string{"p000", "p001"} {
				a := core.AudioAssessment{TrackID: id, AnalysisID: "synthetic-packed", Eligible: true}
				for _, clause := range audio.Clauses(intent) {
					a.Clauses = append(a.Clauses, core.AudioClauseAssessment{Clause: clause, Score: .9, ScoreAvailable: true, State: core.EvidenceUnknown})
				}
				cat.assessments[id] = a
			}
			session, err := service.BeginWithBudget(context.Background(), intent, cat.CatalogVersion(), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			cfg := DefaultConfig()
			cfg.LibraryEvidenceEnabled = true
			engine := New(cat, fakes.NewSimilarityEngine(base), base, cfg)
			engine.audioSession, engine.retriever, engine.enhanced, engine.bestAvailable = session, retriever, true, true
			got, _, err := engine.collectIteratively(context.Background(), retriever.candidates, nil, intent, ports.RecommendationRequest{Intent: intent}, newEligibility(intent, nil, nil), nil, nil, nil, 42)
			if err != nil {
				t.Fatal(err)
			}
			if strict && len(got) != 0 {
				t.Fatal("pooled evidence certified strict request", got)
			}
			if !strict && len(got) != 2 {
				t.Fatalf("preview budget hid packed candidates: %+v", got)
			}
			if store.calls != 0 {
				t.Fatal("exhausted preview budget performed reads")
			}
		})
	}
}
