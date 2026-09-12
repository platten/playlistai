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
