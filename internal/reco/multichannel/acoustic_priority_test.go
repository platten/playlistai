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

func TestAcousticPriorityRanksAheadOfConflictingPreview(t *testing.T) {
	cat := testCatalog()
	intent := core.MusicIntent{Preferences: core.SemanticPreferences{Styles: []core.IntentPreference{{Value: "electronic"}}}}.Normalized()
	clauses := audio.Clauses(intent)
	input := candidatesForTracks(refs(cat, "audio", "cooc"))
	previews := map[string]core.AudioAssessment{}
	for i := range input {
		score := .9
		if input[i].Track.ID == "audio" {
			score = -.9 // deliberately disagrees with the archive
		}
		a := core.AudioAssessment{TrackID: input[i].Track.ID, Clauses: []core.AudioClauseAssessment{{Clause: clauses[0], Score: score, ScoreAvailable: true, State: core.EvidenceUnknown}}}
		previews[a.TrackID] = a
		audio.ApplyScores(&input[i], a)
	}
	request := ports.RankRequest{Intent: intent, PreviewAssessments: previews}
	ranker := NewRanker(cat, DefaultConfig())
	baseline, err := ranker.Rank(context.Background(), input, request)
	if err != nil || baseline[0].Track.ID != "cooc" {
		t.Fatalf("CLAP fallback did not rank: %+v %v", baseline, err)
	}
	request.Intent.Knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{acousticFixture("audio", .95), acousticFixture("cooc", .05)}}
	got, err := ranker.Rank(context.Background(), input, request)
	if err != nil || got[0].Track.ID != "audio" || got[0].Scores.Total <= 0 || got[1].Scores.Total >= 0 {
		t.Fatalf("archive did not lead ranking: %+v %v", got, err)
	}
	for _, c := range got {
		if c.Scores.SemanticMatch != 0 || !c.Available.SemanticMatch {
			t.Fatalf("overlapping preview contribution not neutralized: %+v", c)
		}
	}
	// Re-ranking is idempotent and must not mutate raw preview evidence.
	again, err := ranker.Rank(context.Background(), got, request)
	if err != nil || !reflect.DeepEqual(got, again) || previews["audio"].Clauses[0].Score != -.9 {
		t.Fatal("re-ranking changed scores or raw evidence")
	}
	comparisons := acousticComparisons(request.Intent.Knowledge.Tracks[0], clauses)
	mergePreviewComparisons(comparisons, previews["audio"].Clauses)
	if comparisons[0].PreviewScore == nil || *comparisons[0].PreviewScore != -.9 {
		t.Fatal("explanation lost contradictory preview evidence")
	}
	request.Intent.Controls.RecommendationMode = core.CLAPFirst
	clap, err := ranker.Rank(context.Background(), input, request)
	if err != nil || clap[0].Track.ID != "cooc" || clap[0].Scores.SemanticMatch != .9 || clap[0].Scores.AcousticIntent != 0 {
		t.Fatalf("CLAP-first did not reverse source priority: %+v %v", clap, err)
	}
	// Missing CLAP remains a fallback, not a disabled archive channel.
	request.PreviewAssessments = nil
	fallback, err := ranker.Rank(context.Background(), candidatesForTracks(refs(cat, "audio", "cooc")), request)
	if err != nil || fallback[0].Track.ID != "audio" || !fallback[0].Available.AcousticIntent {
		t.Fatal("CLAP-first lost archive fallback")
	}
}

func TestCLAPPriorityRetainsUncoveredArchiveClauses(t *testing.T) {
	genre := core.AudioClause{Kind: "genre", Text: "electronic"}
	mood := core.AudioClause{Kind: "mood", Text: "relaxing"}
	comparisons := acousticComparisons(acousticFixture("a", .95), []core.AudioClause{genre, mood})
	preview := core.AudioAssessment{Clauses: []core.AudioClauseAssessment{{Clause: genre, ScoreAvailable: true, Score: -.5}}}
	filtered := preferCLAPRanking(comparisons, preview)
	score, available := acousticIntentScore(filtered)
	if !available || math.Abs(score-.4) > 1e-9 || comparisons[0].AcousticScore == nil {
		t.Fatalf("archive gap or raw evidence lost: %+v", filtered)
	}
}

func TestAcousticPriorityRetainsUncoveredClausesAndDenominators(t *testing.T) {
	genre := core.AudioClause{Kind: "genre", Text: "electronic"}
	texture := core.AudioClause{Kind: "texture", Text: "sparkle"}
	noRock := core.AudioClause{Kind: "genre", Text: "rock", Negative: true}
	noSleep := core.AudioClause{Kind: "mood", Text: "sleepy", Negative: true}
	for _, tt := range []struct {
		name                       string
		clauses                    []core.AudioClause
		wantPositive, wantNegative float64
	}{
		{"overlap", []core.AudioClause{genre}, 0, 0},
		{"texture gap", []core.AudioClause{genre, texture}, .4, 0},
		{"negation gap", []core.AudioClause{noRock, noSleep}, 0, .4},
		{"all clauses", []core.AudioClause{genre, texture, noRock, noSleep}, .4, .4},
		{"journey", []core.AudioClause{
			{Kind: "genre", Text: "electronic", Scope: "journey_start"},
			{Kind: "texture", Text: "sparkle", Scope: "journey_start"},
			{Kind: "genre", Text: "rock", Scope: "journey_end"},
		}, .4, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			preview := core.AudioAssessment{}
			for _, clause := range tt.clauses {
				preview.Clauses = append(preview.Clauses, core.AudioClauseAssessment{Clause: clause, Score: .8, ScoreAvailable: true})
			}
			var candidate core.Candidate
			audio.ApplyScores(&candidate, preview)
			preferAcousticRanking(&candidate, acousticComparisons(acousticFixture("a", .95), tt.clauses), preview)
			if math.Abs(candidate.Scores.SemanticMatch-tt.wantPositive) > 1e-9 || math.Abs(candidate.Scores.SemanticNegativeMatch-tt.wantNegative) > 1e-9 {
				t.Fatalf("residual = %+v", candidate.Scores)
			}
		})
	}
}

func TestAcousticPriorityDoesNotSuppressUnusableEvidence(t *testing.T) {
	clauses := []core.AudioClause{{Kind: "genre", Text: "rock"}}
	preview := core.AudioAssessment{Clauses: []core.AudioClauseAssessment{{Clause: clauses[0], Score: .8, ScoreAvailable: true}}}
	for _, state := range []string{"missing", "weak", "conflicting", "wrong recording"} {
		t.Run(state, func(t *testing.T) {
			track := acousticFixture("a", .05)
			switch state {
			case "missing":
				track.Acoustic = nil
			case "weak":
				track = acousticFixture("a", .5)
			case "conflicting":
				track.Acoustic.Predictions["genre_rosamerica"] = core.AcousticPrediction{Classes: map[string]float64{"roc": .05, "jaz": .95}, Version: map[string]string{"model": "fixture"}}
			case "wrong recording":
				track.Acoustic.RecordingID = "different"
			}
			var before core.Candidate
			audio.ApplyScores(&before, preview)
			after := before
			preferAcousticRanking(&after, acousticComparisons(track, clauses), preview)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("unusable evidence suppressed CLAP")
			}
		})
	}
}

func TestAcousticPriorityUsesSessionEvidenceAndCurrentKnowledge(t *testing.T) {
	cat := testCatalog()
	service, resolver := cachedAudioService(t, cat)
	intent := core.MusicIntent{Preferences: core.SemanticPreferences{Styles: []core.IntentPreference{{Value: "electronic"}}}}.Normalized()
	session, err := service.Begin(context.Background(), intent, cat.CatalogVersion(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	input := candidatesForTracks(refs(cat, "audio"))
	assessment, err := session.Check(context.Background(), input[0].Track, false)
	if err != nil || len(assessment.Clauses) == 0 {
		t.Fatalf("missing preview evidence: %+v %v", assessment, err)
	}
	audio.ApplyScores(&input[0], assessment)
	if input[0].Scores.SemanticMatch <= 0 {
		t.Fatal("fixture must have positive preview score")
	}
	o := &Orchestrator{ranker: NewRanker(cat, DefaultConfig()), audioSession: session, knowledge: &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{acousticFixture("audio", .95)}}}
	ranked, err := o.rankCandidates(context.Background(), input, ports.RankRequest{Intent: intent})
	if err != nil || ranked[0].Scores.SemanticMatch != 0 || ranked[0].Scores.AcousticIntent < .89 {
		t.Fatalf("session evidence not passed into ranking: %+v %v", ranked, err)
	}
	// Current request knowledge, not the stale intent snapshot, owns scoring.
	o.knowledge = nil
	ranked, err = o.rankCandidates(context.Background(), input, ports.RankRequest{Intent: intent})
	if err != nil || ranked[0].Scores.SemanticMatch != input[0].Scores.SemanticMatch || resolver.calls != 0 {
		t.Fatal("missing archive changed CLAP fallback or fetched a preview")
	}
	if saved := session.Snapshot().Assessments; len(saved) != 1 || !reflect.DeepEqual(saved[0], assessment) {
		t.Fatal("ranking changed saved evidence")
	}
}

func TestAcousticPriorityDoesNotBypassRequiredPreviewMismatch(t *testing.T) {
	cat := testCatalog()
	service, _ := cachedAudioService(t, cat)
	engine := New(cat, nil, cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
	intent := testIntent(2)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "electronic", Scope: "playlist"}}
	intent.RequiredTracks = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "cooc", Influence: core.InfluencePositive}}
	// The archive supports electronic, but the calibrated fixture preview does
	// not. Source ranking preference must never rewrite this required conflict.
	intent.Knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{acousticFixture("cooc", .95)}}
	got, err := engine.Build(context.Background(), intent)
	if err != nil || got.Outcome.State != core.OutcomeNeedsClarification {
		t.Fatalf("archive bypassed preview mismatch: %+v %v", got, err)
	}
}
