package audio

import (
	"context"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestClausesPreserveTranslatedScopeStrengthAndDegree(t *testing.T) {
	intent := core.MusicIntent{EssentialCriteria: []core.MusicalCriterion{{Kind: "genre", Value: "hip hop", Scope: "journey_start", Strength: "essential", ConceptID: "genre.hip-hop"}}, Preferences: core.SemanticPreferences{
		Styles:          []core.IntentPreference{{Value: "hip-hop", Scope: "journey_end", Strength: "preferred", Group: "end-alternatives", ConceptID: "genre.hip-hop"}},
		Moods:           []core.IntentPreference{{Value: "aggressive", Influence: core.InfluenceNegative, Scope: "journey_start", Strength: "preferred", Degree: "reduced", ConceptID: "mood.aggressive"}},
		VocalPreference: &core.IntentPreference{Value: "instrumental", Scope: "playlist", Strength: "preferred", Degree: "mostly", ConceptID: "vocal.instrumental"},
	}}
	clauses := Clauses(intent)
	if len(clauses) != 4 || clauses[0].ConceptID != "genre.hip-hop" || !clauses[0].Essential || clauses[0].Strict {
		t.Fatalf("essential criterion changed: %+v", clauses)
	}
	if clauses[1].Scope != "journey_end" || clauses[1].Group != "end-alternatives" || clauses[1].Essential || clauses[1].Strict {
		t.Fatalf("explicit second-stage preference flattened/dropped: %+v", clauses)
	}
	if !clauses[2].Negative || clauses[2].Degree != "reduced" || clauses[2].Scope != "journey_start" {
		t.Fatalf("reduced negative preference changed: %+v", clauses[2])
	}
	if clauses[3].Degree != "mostly" || instrumentalClause(clauses[3]) || core.WantsInstrumental(intent) || requiredInstrumentalScreen(clauses) {
		t.Fatalf("mostly instrumental forced vocal exclusion: %+v", clauses)
	}
}

func TestClauseEligibilityKeepsStageConjunctionAndExplicitAlternatives(t *testing.T) {
	clause := func(scope, group string, state core.EvidenceState) core.AudioClauseAssessment {
		return core.AudioClauseAssessment{Clause: core.AudioClause{Kind: "mood", Scope: scope, Group: group, Essential: true, Strict: true}, State: state}
	}
	for _, tc := range []struct {
		name    string
		clauses []core.AudioClauseAssessment
		want    bool
	}{
		{"independent requirements AND", []core.AudioClauseAssessment{clause("journey_start", "", core.EvidenceMatch), clause("journey_start", "", core.EvidenceMismatch)}, false},
		{"explicit alternatives OR", []core.AudioClauseAssessment{clause("playlist", "either", core.EvidenceMatch), clause("playlist", "either", core.EvidenceMismatch)}, true},
		{"unknown alternative is not evidence", []core.AudioClauseAssessment{clause("playlist", "either", core.EvidenceUnknown), clause("playlist", "either", core.EvidenceMismatch)}, false},
		{"can fit other stage", []core.AudioClauseAssessment{clause("journey_start", "", core.EvidenceMismatch), clause("journey_end", "", core.EvidenceMatch)}, true},
		{"global requirement applies to both", []core.AudioClauseAssessment{clause("journey_start", "", core.EvidenceMismatch), clause("journey_end", "", core.EvidenceMatch), clause("playlist", "", core.EvidenceUnknown)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := clausesEligible(tc.clauses, false); got != tc.want {
				t.Fatalf("eligible=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestMostlyInstrumentalUsesRankingWithoutStrictScreen(t *testing.T) {
	s, a, _, _ := testService(t)
	encoder := &vocalEncoder{testAnalyzer: *a}
	s.Analyzer, s.Policy = encoder, Policy{}
	intent := core.MusicIntent{VerificationPolicy: core.BestAvailable, Preferences: core.SemanticPreferences{
		VocalPreference: &core.IntentPreference{Value: "instrumental", Scope: "playlist", Strength: "preferred", Degree: "mostly"},
	}}
	session, err := s.Begin(context.Background(), intent, "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	got, err := session.Check(context.Background(), core.TrackRef{ID: "soft", Artist: "Fixture", Title: "Recording"}, false)
	if err != nil || !got.Eligible || len(got.Clauses) != 1 || got.Clauses[0].State != core.EvidenceUnknown || !got.Clauses[0].ScoreAvailable {
		t.Fatalf("soft preference was gated: %+v %v", got, err)
	}
	if strings.Contains(got.PolicyVersion, VocalPolicyVersion) || encoder.textCalls != 2 {
		t.Fatalf("soft preference invoked strict vocal screen: policy=%s calls=%d", got.PolicyVersion, encoder.textCalls)
	}
}

func TestSpecificVocalExclusionNeverBecomesAllVocals(t *testing.T) {
	intent := core.MusicIntent{HardConstraints: []core.HardConstraint{{Kind: "exclude_vocal", Value: "screamed vocals"}}}
	clauses := Clauses(intent)
	if len(clauses) != 1 || clauses[0].Kind != "vocal" || clauses[0].Text != "screaming" || clauses[0].ConceptID != "vocal.screaming" || !clauses[0].Negative || !clauses[0].Strict || clauses[0].Strength != "required" {
		t.Fatalf("specific exclusion mistranslated: %+v", clauses)
	}
	if core.WantsInstrumental(intent) || instrumentalClause(clauses[0]) || requiredInstrumentalScreen(clauses) {
		t.Fatal("screaming exclusion became all-vocal exclusion")
	}
}

func TestRequiredPreferenceCannotBeDeduplicatedByWeakerCriterion(t *testing.T) {
	intent := core.MusicIntent{EssentialCriteria: []core.MusicalCriterion{{Kind: "genre", Value: "classical", Scope: "playlist", Strength: "essential"}}, Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "classical", Scope: "playlist", Strength: "required"}}}}
	clauses := Clauses(intent)
	if len(clauses) != 2 || !clauses[1].Strict {
		t.Fatalf("required preference weakened by essential criterion: %+v", clauses)
	}
}

func TestArtistEndpointsDoNotCreateRawCLAPDescription(t *testing.T) {
	ref := core.IntentReference{Kind: core.ReferenceArtist, Query: "Nine Inch Nails"}
	for _, intent := range []core.MusicIntent{
		{OriginalDescription: "Start with Nine Inch Nails", Start: &ref},
		{OriginalDescription: "End with Nine Inch Nails", Destination: &ref},
		{OriginalDescription: "Nine Inch Nails to Marilyn Manson", Start: &ref, Destination: &core.IntentReference{Kind: core.ReferenceArtist, Query: "Marilyn Manson"}},
	} {
		if clauses := Clauses(intent); len(clauses) != 0 {
			t.Fatalf("reference identity became descriptive CLAP text: %+v", clauses)
		}
	}
}

func TestPluralVocalPreferencesPreserveStrictJourneyScopes(t *testing.T) {
	intent := core.MusicIntent{Preferences: core.SemanticPreferences{
		// A stale compatibility view must never override the complete list.
		VocalPreference: &core.IntentPreference{Value: "instrumental", Strength: "required", Scope: "playlist"},
		VocalPreferences: []core.IntentPreference{
			{Value: "harsh vocals", Influence: core.InfluenceNegative, Strength: "required", Scope: "journey_start", ConceptID: "vocal.harsh-vocals"},
			{Value: "vocals", Influence: core.InfluencePositive, Strength: "required", Scope: "journey_end", ConceptID: "vocal.vocals"},
		},
	}}
	clauses := Clauses(intent)
	if len(clauses) != 2 || clauses[0].Kind != "vocal" || clauses[0].Text != "harsh vocals" || clauses[0].Scope != "journey_start" || !clauses[0].Negative || !clauses[0].Strict || !clauses[0].Essential {
		t.Fatalf("strict starting vocal exclusion was lost or flattened: %+v", clauses)
	}
	if clauses[1].Kind != "vocal" || clauses[1].Text != "vocals" || clauses[1].Scope != "journey_end" || clauses[1].Negative || !clauses[1].Strict || !clauses[1].Essential {
		t.Fatalf("required final vocals were lost or inverted: %+v", clauses)
	}
	if core.WantsInstrumental(intent) || requiredInstrumentalScreen(clauses) {
		t.Fatal("scoped vocal subtype exclusion became a global instrumental requirement")
	}
	assessments := []core.AudioClauseAssessment{{Clause: clauses[0], State: core.EvidenceMismatch}, {Clause: clauses[1], State: core.EvidenceMatch}}
	if !clausesEligible(assessments, true) {
		t.Fatal("valid ending track was excluded by starting vocal preference")
	}
	assessments[1].State = core.EvidenceUnknown
	if clausesEligible(assessments, true) {
		t.Fatal("neither stage has required evidence but track was accepted")
	}
}

func TestMostlyInstrumentalAndNoHarshVocalsRemainIndependent(t *testing.T) {
	intent := core.MusicIntent{Preferences: core.SemanticPreferences{VocalPreferences: []core.IntentPreference{
		{Value: "instrumental", Influence: core.InfluencePositive, Strength: "preferred", Degree: "mostly", Scope: "playlist", ConceptID: "vocal.instrumental"},
		// The text/facet carries the request, regardless of an arbitrary ID.
		{Value: "harsh vocals", Influence: core.InfluenceNegative, Strength: "required", Scope: "playlist", ConceptID: "vocal.instrumental"},
	}}}
	clauses := Clauses(intent)
	if len(clauses) != 2 || clauses[0].Strict || clauses[0].Essential || clauses[0].Degree != "mostly" || clauses[0].Kind != "vocal" || clauses[0].Negative {
		t.Fatalf("mostly instrumental changed meaning: %+v", clauses)
	}
	if !clauses[1].Strict || !clauses[1].Negative || clauses[1].Text != "harsh vocals" || clauses[1].Kind != "vocal" || clauses[1].Scope != "playlist" {
		t.Fatalf("independent harsh vocal exclusion was lost: %+v", clauses)
	}
	if core.WantsInstrumental(intent) || requiredInstrumentalScreen(clauses) || instrumentalClause(clauses[1]) {
		t.Fatal("specific vocal exclusion or arbitrary concept ID forced instrumental-only screening")
	}
	queries := ClauseQueries(clauses[1])
	if len(queries) != 1 || queries[0] != "harsh vocals" {
		t.Fatalf("arbitrary concept ID changed strict evidence query: %q", queries)
	}
}

func TestPluralVocalAlternativesKeepGroupAndLegacyFallback(t *testing.T) {
	intent := core.MusicIntent{Preferences: core.SemanticPreferences{VocalPreferences: []core.IntentPreference{
		{Value: "instrumental", Scope: "journey_start", Strength: "required", Group: "opening"},
		{Value: "female vocals", Scope: "journey_start", Strength: "required", Group: "opening"},
	}}}
	clauses := Clauses(intent)
	if len(clauses) != 2 || clauses[0].Group != "opening" || clauses[1].Group != "opening" || core.WantsInstrumental(intent) {
		t.Fatalf("vocal alternatives became unconditional no-vocals: %+v", clauses)
	}
	if !clausesEligible([]core.AudioClauseAssessment{{Clause: clauses[0], State: core.EvidenceMismatch}, {Clause: clauses[1], State: core.EvidenceMatch}}, true) {
		t.Fatal("explicit vocal OR alternatives became an intersection")
	}
	intent.Preferences.VocalPreferences = nil
	intent.Preferences.VocalPreference = &core.IntentPreference{Value: "vocals", Scope: "playlist", Strength: "preferred"}
	clauses = Clauses(intent)
	if len(clauses) != 1 || clauses[0].Text != "vocals" || clauses[0].Kind != "vocal" || clauses[0].Strict {
		t.Fatalf("legacy singleton lost compatibility: %+v", clauses)
	}
}
