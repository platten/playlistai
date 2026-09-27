package audio

import (
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestPlaylistCoverageAudioPreservesSeparatePoliciesAndRequirements(t *testing.T) {
	intent := core.MusicIntent{Controls: core.IntentControls{RecommendationMode: core.EnhancedHybrid}, EssentialCriteria: []core.MusicalCriterion{
		{Kind: "genre", Value: "house", Scope: "playlist", Strength: "required", CoverageGroup: "mix"},
		{Kind: "genre", Value: "techno", Scope: "playlist", Strength: "required", CoverageGroup: "mix"},
	}}
	intent.Preferences.Genres = []core.IntentPreference{{Value: "house", Influence: core.InfluencePositive, Scope: "playlist", Strength: "required", CoverageGroup: "mix"}}
	for _, mode := range []core.RecommendationMode{core.EnhancedHybrid, core.DeejAIOnly, core.AcousticBrainzFirst, core.CLAPFirst} {
		intent.Controls.RecommendationMode = mode
		clauses := Clauses(intent)
		if len(clauses) != 2 {
			t.Fatalf("duplicate preference: %+v", clauses)
		}
		observed := []core.AudioClauseAssessment{{Clause: clauses[0], State: core.EvidenceMatch, Score: .8, ScoreAvailable: true}, {Clause: clauses[1], State: core.EvidenceMismatch, Score: .1, ScoreAvailable: true}}
		if got := clausesEligible(observed, true); got != (mode == core.EnhancedHybrid) {
			t.Fatalf("%s eligibility=%t", mode, got)
		}
		if mode != core.EnhancedHybrid {
			continue
		}
		candidate := core.Candidate{}
		applyTypedScores(&candidate, core.AudioAssessment{Clauses: observed})
		if candidate.Scores.SemanticMatch != .8 {
			t.Fatal("other coverage members penalized supported genre")
		}
		observed = append(observed, core.AudioClauseAssessment{Clause: core.AudioClause{Kind: "vocal", Text: "vocals", Negative: true, Strict: true, Scope: "playlist"}, State: core.EvidenceMismatch})
		if clausesEligible(observed, true) {
			t.Fatal("genre coverage bypassed no-vocals")
		}
	}
}
