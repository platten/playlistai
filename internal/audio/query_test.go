package audio

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestQueryPlanKeepsTypedMeaningAndNegativePolaritySeparate(t *testing.T) {
	mood := core.AudioClause{Kind: "mood", Text: "dark", Scope: "journey_end", Negative: true}
	texture := core.AudioClause{Kind: "texture", Text: "dark", Scope: "playlist"}
	if reflect.DeepEqual(ClauseQueries(mood), ClauseQueries(texture)) {
		t.Fatal("mood and timbre collapsed")
	}
	if !reflect.DeepEqual(ClauseQueries(mood), []string{"dark", "Music with a dark mood."}) {
		t.Fatal("query invented wording or embedded negation")
	}
	mood.Strict = true
	if !reflect.DeepEqual(ClauseQueries(mood), []string{"dark"}) {
		t.Fatal("strict query changed")
	}
	texture.Text = strings.Repeat("long ", 30)
	if len(ClauseQueries(texture)) != 1 {
		t.Fatal("long query expanded")
	}
}

func TestTypedQueriesAreVersionedAndCalibratedPoliciesStayRaw(t *testing.T) {
	s, _, _, _ := testService(t)
	intent := audioIntent()
	intent.VerificationPolicy = core.BestAvailable
	calibrated, err := s.Begin(context.Background(), intent, "catalog", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer calibrated.Close()
	if calibrated.typedQueries() {
		t.Fatal("calibrated thresholds reused for different queries")
	}
	s2 := *s
	s2.Policy = Policy{}
	typed, err := s2.Begin(context.Background(), intent, "catalog", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer typed.Close()
	if !typed.typedQueries() || typed.fingerprint == calibrated.fingerprint || !strings.Contains(typed.Snapshot().PolicyVersion, QueryPolicyVersion) {
		t.Fatal("query policy missing from assessment identity")
	}
}

func TestRepeatedGenreClausesCannotOutvoteMood(t *testing.T) {
	makeAssessment := func(repeat int) core.AudioAssessment {
		a := core.AudioAssessment{PolicyVersion: SimilarityPolicyVersion + "+" + QueryPolicyVersion}
		for range repeat {
			a.Clauses = append(a.Clauses, core.AudioClauseAssessment{Clause: core.AudioClause{Kind: "genre", Scope: "playlist"}, Score: 1, ScoreAvailable: true})
		}
		a.Clauses = append(a.Clauses, core.AudioClauseAssessment{Clause: core.AudioClause{Kind: "mood", Scope: "playlist"}, Score: 0, ScoreAvailable: true})
		return a
	}
	var once, aliases core.Candidate
	ApplyScores(&once, makeAssessment(1))
	ApplyScores(&aliases, makeAssessment(3))
	if once.Scores.SemanticMatch != .5 || aliases.Scores.SemanticMatch != once.Scores.SemanticMatch {
		t.Fatalf("category repetition changed mood weight: %+v %+v", once, aliases)
	}
	old := makeAssessment(3)
	old.PolicyVersion = "calibrated/v1"
	ApplyScores(&aliases, old)
	if aliases.Scores.SemanticMatch != .75 {
		t.Fatal("old calibrated aggregation changed")
	}
}

func TestReviewedAliasesShareQueriesWithoutLosingPolarity(t *testing.T) {
	for _, kind := range []string{"genre", "style"} {
		first := ClauseQueries(core.AudioClause{Kind: kind, Text: "hip hop"})
		for _, alias := range []string{"hip-hop", "HIPHOP"} {
			c := core.AudioClause{Kind: kind, Text: alias, Negative: true, Scope: "journey_end", Strength: "preferred", Degree: "reduced"}
			if got := ClauseQueries(c); !reflect.DeepEqual(first, got) {
				t.Fatalf("alias changed query ensemble: %q != %q", first, got)
			}
			if !c.Negative || c.Scope != "journey_end" || c.Degree != "reduced" {
				t.Fatal("query preparation mutated intent")
			}
		}
	}
	if reflect.DeepEqual(ClauseQueries(core.AudioClause{Kind: "genre", Text: "electronic"}), ClauseQueries(core.AudioClause{Kind: "genre", Text: "electronica"})) {
		t.Fatal("broader genre substituted for electronica")
	}
	if got := ClauseQueries(core.AudioClause{Kind: "vocal", Text: "screaming", Negative: true}); !reflect.DeepEqual(got, []string{"screaming", "Music with screamed vocals."}) {
		t.Fatalf("negative vocal trait lost its positive evidence query: %q", got)
	}
}

func TestTypedScoreAlternativesAndScopedNegatives(t *testing.T) {
	makeClause := func(scope, kind, group string, negative bool, score float64) core.AudioClauseAssessment {
		return core.AudioClauseAssessment{Clause: core.AudioClause{Scope: scope, Kind: kind, Group: group, Negative: negative}, ScoreAvailable: true, Score: score}
	}
	a := core.AudioAssessment{PolicyVersion: QueryPolicyVersion, Clauses: []core.AudioClauseAssessment{
		makeClause("playlist", "genre", "genres", false, .8),
		makeClause("playlist", "genre", "genres", false, .2),
		makeClause("playlist", "mood", "", false, .4),
	}}
	var candidate core.Candidate
	ApplyScores(&candidate, a)
	if delta := candidate.Scores.SemanticMatch - .6; delta < -1e-9 || delta > 1e-9 {
		t.Fatalf("OR genres averaged into contradiction: %+v", candidate.Scores)
	}
	a.Clauses = []core.AudioClauseAssessment{
		makeClause("journey_start", "genre", "", false, .9),
		makeClause("journey_start", "vocal", "", true, .8),
		makeClause("journey_end", "genre", "", false, .6),
		makeClause("journey_end", "vocal", "", true, .1),
	}
	candidate = core.Candidate{}
	ApplyScores(&candidate, a)
	if candidate.Scores.SemanticMatch != .6 || candidate.Scores.SemanticNegativeMatch != .1 {
		t.Fatalf("positive/negative borrowed from different stages: %+v", candidate.Scores)
	}
	a.Clauses = []core.AudioClauseAssessment{makeClause("playlist", "mood", "", true, .8)}
	a.Clauses[0].Clause.Degree = "reduced"
	candidate = core.Candidate{}
	ApplyScores(&candidate, a)
	if candidate.Scores.SemanticNegativeMatch != .4 {
		t.Fatalf("less aggressive became the full exclusion penalty: %+v", candidate.Scores)
	}
}

func TestCrossFacetAlternativesKeepOneStableGroupWeight(t *testing.T) {
	for _, winner := range []string{"instrumentation", "genre", "negative"} {
		a := core.AudioAssessment{PolicyVersion: QueryPolicyVersion, Clauses: []core.AudioClauseAssessment{
			{Clause: core.AudioClause{Kind: "instrumentation", Text: "piano", Scope: "playlist", Group: "either"}, ScoreAvailable: true, Score: .1},
			{Clause: core.AudioClause{Kind: "genre", Text: "ambient", Scope: "playlist", Group: "either"}, ScoreAvailable: true, Score: .1},
			{Clause: core.AudioClause{Kind: "mood", Text: "relaxing", Scope: "playlist"}, ScoreAvailable: true, Score: .4},
		}}
		switch winner {
		case "instrumentation":
			a.Clauses[0].Score = .9
		case "genre":
			a.Clauses[1].Score = .9
		case "negative":
			a.Clauses[1].Clause.Negative = true
			a.Clauses[1].Score = -.9
		}
		var candidate core.Candidate
		ApplyScores(&candidate, a)
		if delta := candidate.Scores.SemanticMatch - .65; delta < -1e-9 || delta > 1e-9 || candidate.Available.SemanticNegativeMatch {
			t.Fatalf("%s winner changed OR group weight: %+v", winner, candidate)
		}
	}
}
