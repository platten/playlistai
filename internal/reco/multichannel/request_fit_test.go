package multichannel

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func TestRequestFitIgnoresTasteExposureAndRetrievalFrequency(t *testing.T) {
	cat := testCatalog()
	intent := enhancedIntent(1)
	intent.References = nil
	intent.Preferences.Genres = []core.IntentPreference{{Value: "ambient", Influence: core.InfluencePositive}}
	candidates := candidatesForTracks(refs(cat, "audio", "taste"))
	for i := range candidates {
		candidates[i].Available.SemanticMatch = true
		candidates[i].Scores.SemanticMatch = .8 - float64(i)*.2
	}
	ranker := NewRanker(cat, DefaultConfig())
	plain, err := ranker.Rank(context.Background(), candidates, ports.RankRequest{Intent: intent})
	if err != nil {
		t.Fatal(err)
	}
	candidates[1].Available.RetrievalFusion = true
	candidates[1].Scores.RetrievalFusion = 1
	profile := core.TasteProfile{Positive: core.EmbeddingAffinity{Audio: []float32{0, 1}, Cooccurrence: []float32{1, 0}}, RecentExposures: map[string]float64{"audio": 1}, ExposureCount: 1}
	personal, err := ranker.Rank(context.Background(), candidates, ports.RankRequest{Intent: intent, Profile: profile})
	if err != nil {
		t.Fatal(err)
	}
	for _, ranked := range [][]core.Candidate{plain, personal} {
		if ranked[0].Track.ID != "audio" || ranked[0].Scores.RequestFit <= ranked[1].Scores.RequestFit {
			t.Fatalf("request fit lost: %+v", ranked)
		}
	}
	for i := range plain {
		if plain[i].Scores.RequestFit != personal[i].Scores.RequestFit {
			t.Fatal("secondary preference changed request fit")
		}
	}
}

func TestRequestFitSelectionPrecedesTasteAndSoftDiversityWithinTier(t *testing.T) {
	cat := diversityCatalog()
	intent := enhancedIntent(2)
	intent.Controls.ArtistDiversity = 1
	candidates := []core.Candidate{selectionCandidate(cat, "a1", .1), selectionCandidate(cat, "a2", .1), selectionCandidate(cat, "b", 1)}
	for i := range candidates {
		candidates[i].Scores.RequestFit = .9 - float64(i)*.1
		candidates[i].Available.RequestFit = true
		candidates[i].FitTier = fitStrong
	}
	got, err := NewSelector(cat, DefaultConfig()).Select(context.Background(), candidates, ports.SelectionRequest{Intent: intent, Count: 2})
	if err != nil || candidateIDs(got.Candidates) != "a1,a2" {
		t.Fatalf("fit lost to artist balance/taste: %+v %v", got, err)
	}
	candidates[2].FitTier = fitClose
	candidates[2].Scores.RequestFit = 1
	got, err = NewSelector(cat, DefaultConfig()).Select(context.Background(), candidates, ports.SelectionRequest{Intent: intent, Count: 2})
	if err != nil || candidateIDs(got.Candidates) != "a1,a2" {
		t.Fatalf("evidence tier lost: %+v %v", got, err)
	}
}

func TestRequestFitJourneyReservationsPreferBestEvidenceTier(t *testing.T) {
	cat := testCatalog()
	intent := enhancedIntent(2)
	intent.References = nil
	intent.Mode = core.ModeJourney
	intent.Preferences.Moods = []core.IntentPreference{{Value: "calm", Influence: core.InfluencePositive}}
	intent.EssentialCriteria = []core.MusicalCriterion{
		{Kind: "genre", Value: "rock", Scope: "journey_start"},
		{Kind: "genre", Value: "classical", Scope: "journey_end"},
	}
	features := &semanticFixture{features: map[string]core.TrackFeatures{}}
	for _, id := range []string{"audio", "cooc", "taste", "last"} {
		style := "rock"
		if id == "last" {
			style = "classical"
		}
		feature := completeStyleFeature(id, style)
		if id != "audio" {
			feature.Moods = []core.FeatureValue{reliableStyle(id, "calm")}
			feature.FacetCoverage = append(feature.FacetCoverage, "moods")
		}
		features.features[id] = feature
	}
	o := NewWithSemantic(cat, nil, cat, features, nil, DefaultConfig())
	addCitedStyleFixture(o, features)
	o.enhanced, o.bestAvailable = true, true
	input := candidatesForTracks(refs(cat, "audio", "taste", "cooc", "last"))
	for i, score := range []float64{.9, .4, .6, .5} {
		input[i].Scores.SemanticMatch, input[i].Available.SemanticMatch = score, true
	}
	ranked, err := o.rankCandidates(context.Background(), input, ports.RankRequest{Intent: intent})
	if err != nil {
		t.Fatal(err)
	}
	close, _ := findCandidate(ranked, "audio")
	strong, _ := findCandidate(ranked, "cooc")
	weakerStrong, _ := findCandidate(ranked, "taste")
	if close.FitTier != fitClose || strong.FitTier != fitStrong || weakerStrong.FitTier != fitStrong || close.Scores.RequestFit <= strong.Scores.RequestFit || strong.Scores.RequestFit <= weakerStrong.Scores.RequestFit {
		t.Fatalf("fixture does not distinguish evidence tiers and request fit: %+v", ranked)
	}
	reserved, _, reasons, err := o.reserveJourneyStages(context.Background(), ranked, nil, intent)
	if err != nil || len(reasons) != 0 || candidateIDs(reserved) != "cooc,last" {
		t.Fatalf("journey reservation ignored evidence tier or request fit: %s, %+v, %v", candidateIDs(reserved), reasons, err)
	}
}

func TestRequestFitAdmissionDoesNotNormalizeWeakPool(t *testing.T) {
	intent := enhancedIntent(1)
	intent.References = nil
	weak := core.Candidate{Track: core.TrackRef{ID: "weak"}, FitTier: fitClose, Scores: core.CandidateScores{SemanticMatch: .01}, Available: core.CandidateFeatures{SemanticMatch: true}}
	strong := weak
	strong.Track.ID, strong.Scores.SemanticMatch = "strong", .8
	one, many := enhancedRequestRelevances([]core.Candidate{weak}, intent), enhancedRequestRelevances([]core.Candidate{weak, strong}, intent)
	if !reflect.DeepEqual(one[0], many[0]) {
		t.Fatal("pool composition changed native suitability")
	}
	got, err := NewSelector(testCatalog(), DefaultConfig()).Select(context.Background(), []core.Candidate{weak}, ports.SelectionRequest{Intent: intent, Count: 1})
	if err != nil || len(got.Candidates) != 0 {
		t.Fatalf("weak maximum was admitted: %+v %v", got, err)
	}
}

func TestAcousticRequestGroupsApplyAlternativesPolarityAndScopeOnce(t *testing.T) {
	comparison := func(group, scope string, value float64, negative bool) core.IntentComparison {
		return core.IntentComparison{Clause: core.AudioClause{Kind: "genre", Group: group, Scope: scope, Negative: negative}, AcousticScore: &value}
	}
	for _, test := range []struct {
		name        string
		comparisons []core.IntentComparison
		want        float64
	}{
		{"OR", []core.IntentComparison{comparison("either", "playlist", .8, false), comparison("either", "playlist", -.8, false)}, .8},
		{"AND", []core.IntentComparison{comparison("", "playlist", .8, false), comparison("", "playlist", -.8, false)}, 0},
		{"negative already signed", []core.IntentComparison{comparison("", "playlist", .8, true)}, .8},
		{"stage alternatives", []core.IntentComparison{comparison("either", "journey_start", .8, false), comparison("either", "journey_start", -.8, false), comparison("", "journey_end", .2, false)}, .8},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := acousticIntentScore(test.comparisons)
			if !ok || math.Abs(got-test.want) > 1e-9 {
				t.Fatalf("got %v %v want %v", got, ok, test.want)
			}
		})
	}
}

func TestRequestFitAudioFamilyCountsDuplicateObservationOnce(t *testing.T) {
	cat, input := enhancedFixture(t)
	intent := enhancedIntent(1)
	candidates := candidatesForTracks(refs(cat, "b"))
	candidates[0].Scores.Total = .5
	candidates[0].Scores.EnhancedMERT, candidates[0].Scores.EnhancedDSP = .8, .6
	candidates[0].Available.EnhancedMERT, candidates[0].Available.EnhancedDSP = true, true
	ranker := NewRanker(cat, DefaultConfig())
	request := ports.RankRequest{Intent: intent, EnhancedAudio: freezeEnhanced(t, input)}
	single := append([]core.Candidate(nil), candidates...)
	if err := ranker.combineEnhancedScores(context.Background(), single, request); err != nil {
		t.Fatal(err)
	}
	candidates[0].Scores.LibraryMERT, candidates[0].Scores.LibraryDSP = .8, .6
	candidates[0].Available.LibraryMERT, candidates[0].Available.LibraryDSP = true, true
	if err := ranker.combineEnhancedScores(context.Background(), candidates, request); err != nil {
		t.Fatal(err)
	}
	if candidates[0].Scores.Total != single[0].Scores.Total {
		t.Fatal("duplicate observations purchased extra weight")
	}
	preview := core.AudioAssessment{PolicyVersion: audio.QueryPolicyVersion, Coverage: &core.PreviewCoverage{Available: true, CoveredSeconds: 20}, Clauses: []core.AudioClauseAssessment{{Clause: core.AudioClause{Kind: "genre", Text: "ambient"}, Score: .2, ScoreAvailable: true}}}
	packed := preview
	packed.Coverage = nil
	packed.LibraryCoverage = &core.LibraryCLAPCoverage{CoveredSeconds: 40}
	packed.Clauses = append([]core.AudioClauseAssessment(nil), preview.Clauses...)
	packed.Clauses[0].Score = .1
	merged := combineSemanticObservations(preview, packed)
	if len(merged.Clauses) != 1 || merged.Clauses[0].Score != .1 {
		t.Fatalf("coverage did not choose observation: %+v", merged)
	}
	if !reflect.DeepEqual(merged, combineSemanticObservations(packed, preview)) {
		t.Fatal("observation input order changed combination")
	}
}

func TestRequestFitOpeningSurvivesTransitionOptimization(t *testing.T) {
	cat := testCatalog()
	intent := enhancedIntent(3)
	intent.Controls.TransitionSmoothness = 1
	candidates := candidatesForTracks(refs(cat, "far", "audio", "cooc"))
	for i := range candidates {
		candidates[i].Scores.RequestFit = .9 - float64(i)*.1
		candidates[i].Available.RequestFit = true
		candidates[i].FitTier = fitStrong
	}
	result, err := NewSequencer(cat, DefaultConfig()).Sequence(context.Background(), ports.SequenceRequest{Intent: intent, Candidates: candidates, ReferenceAnchors: refs(cat, "seed")})
	if err != nil || len(result.Tracks) != 3 || result.Tracks[0].ID != "far" {
		t.Fatalf("opening lost to transition scores: %+v %v", result, err)
	}
}

func TestRequestFitTransitionCountsMERTFamilyOnce(t *testing.T) {
	cat, input := enhancedFixture(t)
	left, right := refs(cat, "seed", "b")[0], refs(cat, "seed", "b")[1]
	intent := enhancedIntent(2)
	cfg := DefaultConfig()
	cfg.LibraryEvidenceEnabled = true
	sequencer := NewSequencer(cat, cfg)
	sequencer.enhancedInput = input
	single, known := sequencer.trackSimilarity(left, right, intent)
	if !known {
		t.Fatal("missing preview transition")
	}
	source := core.LibraryEvidenceSource{SpaceID: "separate-compatible-pack-space"}
	sequencer.libraryVectors = map[string]core.LibraryVector{
		left.ID: {Source: source, Values: []float32{1, 0}}, right.ID: {Source: source, Values: []float32{1, 0}},
	}
	combined, known := sequencer.trackSimilarity(left, right, intent)
	if !known || math.Abs(single-combined) > 1e-12 {
		t.Fatalf("duplicate transition weight: %v vs %v", single, combined)
	}
}

func TestRequestFitFreshMERTReferenceAdmitsDynamicCloseCandidate(t *testing.T) {
	intent := enhancedIntent(1)
	candidate := core.Candidate{Track: core.TrackRef{ID: "dynamic-track", Artist: "New artist", Title: "New track"}, FitTier: fitClose,
		Scores: core.CandidateScores{RequestFit: .1, CombinedMERT: .9}, Available: core.CandidateFeatures{RequestFit: true, CombinedMERT: true}}
	selector := NewSelector(testCatalog(), DefaultConfig())
	result, err := selector.Select(context.Background(), []core.Candidate{candidate}, ports.SelectionRequest{Intent: intent, Count: 1})
	if err != nil || len(result.Candidates) != 1 {
		t.Fatalf("fresh reference observation lost: %+v %v", result, err)
	}
	intent.References = nil
	result, err = selector.Select(context.Background(), []core.Candidate{candidate}, ports.SelectionRequest{Intent: intent, Count: 1})
	if err != nil || len(result.Candidates) != 0 {
		t.Fatalf("unrequested MERT admitted a candidate: %+v %v", result, err)
	}
}
