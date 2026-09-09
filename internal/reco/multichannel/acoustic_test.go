package multichannel

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func TestAcousticPredictionsDoNotEstablishEssentialFit(t *testing.T) {
	track := core.EnrichedTrack{Ref: core.TrackRef{ID: "track"}, Matched: true, IdentityStatus: core.ResolutionResolved,
		Acoustic: &core.AcousticCharacteristics{HighStatus: "available", Predictions: map[string]core.AcousticPrediction{
			"genre_dortmund":     {Value: "electronic", Score: 1},
			"voice_instrumental": {Value: "instrumental", Score: 1},
		}},
	}
	o := &Orchestrator{knowledge: &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}}
	for _, criterion := range []core.MusicalCriterion{{Kind: "style", Value: "electronic"}, {Kind: "vocal", Value: "instrumental"}} {
		if state := o.bestCriterion(context.Background(), "track", criterion); state != core.EvidenceUnknown {
			t.Fatalf("uncalibrated classifier established essential fit: %s", state)
		}
	}
}

func acousticFixture(id string, electronic float64) core.EnrichedTrack {
	return core.EnrichedTrack{Ref: core.TrackRef{ID: id}, Matched: true, IdentityStatus: core.ResolutionResolved, RecordingID: "recording",
		Acoustic: &core.AcousticCharacteristics{SchemaVersion: 1, RecordingID: "recording", Source: "fixture", HighStatus: "available", Predictions: map[string]core.AcousticPrediction{
			"genre_dortmund":     {Value: "electronic", Score: electronic, Classes: map[string]float64{"electronic": electronic, "rock": 1 - electronic}, Version: map[string]string{"model": "fixture"}},
			"mood_relaxed":       {Value: "relaxed", Score: .9, Classes: map[string]float64{"relaxed": .9, "not_relaxed": .1}, Version: map[string]string{"model": "fixture"}},
			"voice_instrumental": {Value: "voice", Score: .9, Classes: map[string]float64{"voice": .9, "instrumental": .1}, Version: map[string]string{"model": "fixture"}},
		}},
	}
}

func TestAcousticIntentComparisonNegationAndUnknownConcepts(t *testing.T) {
	for _, tt := range []struct {
		kind, text string
		negative   bool
		state      string
	}{
		{"style", "electronic", false, "supporting"},
		{"style", "rock", true, "supporting"},
		{"style", "electronic", true, "opposing"},
		{"mood", "relaxing", false, "supporting"},
		{"mood", "sleepy", true, "unknown"},
		{"texture", "microdetail", false, "unknown"},
		{"texture", "sparkle", false, "unknown"},
		{"genre", "electronic with rock influence", false, "unknown"},
		{"genre", "rock & roll", false, "unknown"},
		{"vocal", "instrumental", false, "opposing"},
		{"vocal", "vocals", true, "opposing"},
	} {
		t.Run(tt.text+tt.state, func(t *testing.T) {
			c := acousticComparisons(acousticFixture("a", .95), []core.AudioClause{{Kind: tt.kind, Text: tt.text, Negative: tt.negative}})[0]
			if c.AcousticState != tt.state {
				t.Fatalf("got %+v", c)
			}
			if c.Clause.Text != tt.text || c.Clause.Negative != tt.negative {
				t.Fatal("comparison changed intent")
			}
		})
	}
}

func TestAcousticUnknownIdentityAndConditionalModelCannotGrantFit(t *testing.T) {
	clause := []core.AudioClause{{Kind: "genre", Text: "electronic", Essential: true}}
	for _, mutate := range []func(*core.EnrichedTrack){
		func(t *core.EnrichedTrack) { t.IdentityStatus = core.ResolutionAmbiguous },
		func(t *core.EnrichedTrack) { t.Acoustic.RecordingID = "other" },
		func(t *core.EnrichedTrack) {
			t.Acoustic.Predictions["genre_dortmund"] = core.AcousticPrediction{Classes: map[string]float64{"electronic": .9, "rock": .1}}
		},
		func(t *core.EnrichedTrack) {
			t.Acoustic.Predictions["genre_dortmund"] = core.AcousticPrediction{Classes: map[string]float64{"electronic": math.NaN(), "rock": .1}, Version: map[string]string{"model": "fixture"}}
		},
		func(t *core.EnrichedTrack) {
			delete(t.Acoustic.Predictions, "genre_dortmund")
			t.Acoustic.Predictions["genre_electronic"] = core.AcousticPrediction{Classes: map[string]float64{"ambient": 1}, Version: map[string]string{"model": "fixture"}}
		},
	} {
		track := acousticFixture("a", .9)
		mutate(&track)
		if c := acousticComparisons(track, clause)[0]; c.AcousticState != "unknown" || c.AcousticScore != nil {
			t.Fatalf("unusable evidence scored: %+v", c)
		}
	}
	track := acousticFixture("a", .95)
	o := &Orchestrator{knowledge: &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}}
	if o.bestCriterion(context.Background(), "a", core.MusicalCriterion{Kind: "genre", Value: "electronic"}) != core.EvidenceUnknown {
		t.Fatal("prediction became verified genre evidence")
	}
}

func TestAcousticEssentialScreeningPreservesJourneysAndSoftPreferences(t *testing.T) {
	track := acousticFixture("a", .05)
	clauses := []core.AudioClause{{Kind: "genre", Text: "electronic", Scope: "journey_start", Essential: true}, {Kind: "genre", Text: "rock", Scope: "journey_end", Essential: true}}
	comparisons := acousticComparisons(track, clauses)
	if !acousticCompatible(comparisons) || acousticCompatible(comparisons[:1]) {
		t.Fatal("journey flattened or incompatible stage allowed")
	}
	score, ok := acousticIntentScore(comparisons)
	if !ok || score < .89 {
		t.Fatalf("journey score = %v", score)
	}
	clauses[0].Scope = "playlist"
	if acousticCompatible(acousticComparisons(track, clauses[:1])) {
		t.Fatal("strong essential opposition allowed")
	}
	clauses[0].Essential = false
	if !acousticCompatible(acousticComparisons(track, clauses[:1])) {
		t.Fatal("soft preference became hard filter")
	}
	clauses[0].Strict = true
	if acousticCompatible(acousticComparisons(track, clauses[:1])) {
		t.Fatal("strict opposition allowed")
	}
}

func TestAcousticConflictingModelsAreNotAveragedIntoVerifiedFit(t *testing.T) {
	track := acousticFixture("a", .05)
	track.Acoustic.Predictions["genre_rosamerica"] = core.AcousticPrediction{Classes: map[string]float64{"roc": .05, "jaz": .95}, Version: map[string]string{"model": "fixture"}}
	comparisons := acousticComparisons(track, []core.AudioClause{{Kind: "genre", Text: "rock", Essential: true}})
	if comparisons[0].AcousticState != "conflicting" || acousticCompatible(comparisons) {
		t.Fatalf("conflict concealed: %+v", comparisons)
	}
}

func TestAcousticRankingUnknownDenominatorAndChangedIntent(t *testing.T) {
	cat := testCatalog()
	intent := core.MusicIntent{Preferences: core.SemanticPreferences{Styles: []core.IntentPreference{{Value: "electronic"}}}, Knowledge: &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{acousticFixture("audio", .95), acousticFixture("cooc", .05)}}}.Normalized()
	input := candidatesForTracks(refs(cat, "audio", "cooc", "other"))
	ranker := NewRanker(cat, DefaultConfig())
	ranked, err := ranker.Rank(context.Background(), input, ports.RankRequest{Intent: intent})
	if err != nil || ranked[0].Track.ID != "audio" || ranked[1].Track.ID != "other" || ranked[2].Track.ID != "cooc" {
		t.Fatalf("ranking: %+v %v", ranked, err)
	}
	intent.Preferences.Styles[0].Value = "rock"
	ranked, err = ranker.Rank(context.Background(), input, ports.RankRequest{Intent: intent})
	if err != nil || ranked[0].Track.ID != "cooc" {
		t.Fatal("changed musical input did not change ranking")
	}
	clauses := []core.AudioClause{{Kind: "mood", Text: "relaxing"}, {Kind: "mood", Text: "sleepy", Negative: true}}
	comparisons := acousticComparisons(acousticFixture("a", .9), clauses)
	score, ok := acousticIntentScore(comparisons)
	if !ok || math.Abs(score-.4) > 1e-6 {
		t.Fatalf("missing clause changed denominator: %v", score)
	}
}

func TestAcousticOrchestratorEligibilityAndRequiredConflict(t *testing.T) {
	cat := testCatalog()
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	intent := testIntent(3)
	intent.VerificationPolicy = core.BestAvailable
	intent.Controls.Discovery, intent.Controls.ArtistDiversity = 1, 1
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "electronic", Scope: "playlist"}}
	intent.Knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{acousticFixture("audio", .95), acousticFixture("cooc", .01)}}
	request := ports.RecommendationRequest{Intent: intent, Profile: core.TasteProfile{Positive: core.EmbeddingAffinity{Audio: []float32{-1, 0}, Cooccurrence: []float32{0, 1}}}}
	first, err := engine.BuildRecommendation(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, track := range first.Tracks {
		if track.ID == "cooc" {
			t.Fatal("strongly opposing candidate bypassed eligibility")
		}
	}
	second, err := engine.BuildRecommendation(context.Background(), request)
	if err != nil || !reflect.DeepEqual(first.Tracks, second.Tracks) || !reflect.DeepEqual(first.Assessments, second.Assessments) {
		t.Fatal("comparison replay was not deterministic")
	}
	intent.RequiredTracks = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "cooc", Influence: core.InfluencePositive}}
	request.Intent = intent
	result, err := engine.BuildRecommendation(context.Background(), request)
	if err != nil || result.Outcome.State != core.OutcomeNeedsClarification {
		t.Fatalf("required conflict: %+v %v", result.Outcome, err)
	}
}

func TestAcousticNuanceIsPreservedInAssessment(t *testing.T) {
	intent := core.MusicIntent{EssentialCriteria: []core.MusicalCriterion{{Kind: "genre", Value: "electronic", Scope: "playlist"}}, Preferences: core.SemanticPreferences{
		Moods:               []core.IntentPreference{{Value: "relaxing"}, {Value: "sleepy", Influence: core.InfluenceNegative}},
		TextureDescriptions: []core.IntentPreference{{Value: "microdetail"}, {Value: "sparkle"}},
	}}
	o := &Orchestrator{knowledge: &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{acousticFixture("a", .95)}}}
	playlist := core.Playlist{Intent: intent, Tracks: []core.TrackRef{{ID: "a"}}}
	o.annotateIntentComparisons(&playlist)
	if len(playlist.Assessments) != 1 || len(playlist.Assessments[0].Comparisons) != len(audio.Clauses(intent)) {
		t.Fatal("lost intent clauses")
	}
	for _, c := range playlist.Assessments[0].Comparisons {
		if (c.Clause.Text == "sleepy" || c.Clause.Text == "microdetail" || c.Clause.Text == "sparkle") && c.AcousticState != "unknown" {
			t.Fatal("invented feature support")
		}
	}
}

func TestAcousticPreviewDisagreementAndSourceScales(t *testing.T) {
	clause := core.AudioClause{Kind: "mood", Text: "relaxing", Scope: "playlist"}
	comparisons := acousticComparisons(acousticFixture("a", .95), []core.AudioClause{clause})
	mergePreviewComparisons(comparisons, []core.AudioClauseAssessment{{Clause: clause, State: core.EvidenceMismatch, Score: -.1, ScoreAvailable: true}})
	if !comparisons[0].Conflict || *comparisons[0].PreviewScore != -.1 || math.Abs(*comparisons[0].AcousticScore-.8) > 1e-6 {
		t.Fatal("sources disagreed but were averaged or hidden")
	}

	o := &Orchestrator{knowledge: &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{acousticFixture("a", .05)}}}
	playlist := core.Playlist{Tracks: []core.TrackRef{{ID: "a"}}, Intent: core.MusicIntent{Preferences: core.SemanticPreferences{Styles: []core.IntentPreference{{Value: "electronic"}}}}, Outcome: core.GenerationOutcome{State: core.OutcomeFulfilled}}
	o.annotateIntentComparisons(&playlist)
	if playlist.Outcome.State != core.OutcomePartial || len(playlist.Outcome.Reasons) == 0 || playlist.Assessments[0].Comparisons[0].AcousticState != "opposing" {
		t.Fatal("soft opposition was reported as fulfilled")
	}
}
