package audio

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestUncalibratedSimilaritiesRankWithoutProvingFit(t *testing.T) {
	service, _, resolver, _ := testService(t)
	service.Policy = Policy{}
	intent := audioIntent()
	intent.VerificationPolicy = core.BestAvailable
	session, err := service.Begin(context.Background(), intent, "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	assessment, err := session.Check(context.Background(), core.TrackRef{ID: "one", Artist: "Fixture", Title: "Recording"}, false)
	if err != nil || !assessment.Eligible || assessment.PolicyVersion != SimilarityPolicyVersion+"+"+QueryPolicyVersion {
		t.Fatalf("%+v %v", assessment, err)
	}
	for _, clause := range assessment.Clauses {
		if clause.State != core.EvidenceUnknown || !clause.ScoreAvailable {
			t.Fatalf("uncalibrated score became a categorical claim: %+v", clause)
		}
	}
	var candidate core.Candidate
	ApplyScores(&candidate, assessment)
	if !candidate.Available.SemanticMatch || candidate.Scores.SemanticMatch != 1 || !candidate.Available.SemanticNegativeMatch || candidate.Scores.SemanticNegativeMatch != 0 {
		t.Fatalf("similarities not ranked: %+v", candidate)
	}
	if session.Criterion("one", intent.EssentialCriteria[0]) != core.EvidenceUnknown || session.SupportsConstraint("exclude_style") {
		t.Fatal("ranking scores enforced strict fit")
	}
	intent.VerificationPolicy = core.VerifiedOnly
	if service.ReadyFor(intent) {
		t.Fatal("uncalibrated general model became strict verifier")
	}
	if resolver.calls != 1 {
		t.Fatal("unexpected preview requests")
	}
}

func TestMissingTextInferenceCannotProduceCheckedTrack(t *testing.T) {
	service, analyzer, _, _ := testService(t)
	service.Policy = Policy{}
	service.Analyzer = &vocalEncoder{testAnalyzer: *analyzer, textFailure: true}
	intent := audioIntent()
	intent.VerificationPolicy = core.BestAvailable
	session, err := service.Begin(context.Background(), intent, "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	assessment, err := session.Check(context.Background(), core.TrackRef{ID: "one", Artist: "Fixture", Title: "Recording"}, false)
	if err != nil || assessment.Eligible {
		t.Fatalf("failed comparison accepted: %+v %v", assessment, err)
	}
}

func TestJourneyClausesKeepScopeAndNegativePreferences(t *testing.T) {
	intent := core.MusicIntent{EssentialCriteria: []core.MusicalCriterion{{Kind: "genre", Value: "fixture one", Scope: "journey_start"}, {Kind: "genre", Value: "fixture two", Scope: "journey_end"}}, Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "fixture one", Influence: core.InfluencePositive}, {Value: "fixture two", Influence: core.InfluencePositive}, {Value: "fixture three", Influence: core.InfluenceNegative}}}}
	clauses := Clauses(intent)
	if len(clauses) != 3 || clauses[0].Scope != "journey_start" || clauses[1].Scope != "journey_end" || !clauses[2].Negative {
		t.Fatalf("scope/negative loss: %+v", clauses)
	}
}

func TestGenreAndStyleDoNotDoubleScoreTheSameCriterion(t *testing.T) {
	intent := core.MusicIntent{EssentialCriteria: []core.MusicalCriterion{{Kind: "genre", Value: "electronic", Scope: "playlist"}}, Preferences: core.SemanticPreferences{
		Genres: []core.IntentPreference{{Value: "electronic", Influence: core.InfluencePositive}},
		Styles: []core.IntentPreference{{Value: "electronic", Influence: core.InfluencePositive}, {Value: "rock", Influence: core.InfluenceNegative}},
	}}
	clauses := Clauses(intent)
	if len(clauses) != 2 || !clauses[0].Essential || !clauses[1].Negative {
		t.Fatalf("duplicate category score or lost exclusion: %+v", clauses)
	}
}

func TestJourneyRankingRewardsEitherStageRatherThanTheirAverage(t *testing.T) {
	assessment := core.AudioAssessment{Clauses: []core.AudioClauseAssessment{
		{Clause: core.AudioClause{Scope: "journey_start"}, Score: .9, ScoreAvailable: true, State: core.EvidenceUnknown},
		{Clause: core.AudioClause{Scope: "journey_end"}, Score: .1, ScoreAvailable: true, State: core.EvidenceUnknown},
	}}
	var candidate core.Candidate
	ApplyScores(&candidate, assessment)
	if candidate.Scores.SemanticMatch != .9 {
		t.Fatalf("stage-specific fit flattened: %+v", candidate.Scores)
	}
}
