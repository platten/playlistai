package audio

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func cachedSession(t *testing.T, intent core.MusicIntent) (*Session, *Service, core.TrackRef) {
	t.Helper()
	service, _, _, _ := testService(t)
	track := core.TrackRef{ID: "one", Artist: "Fixture", Title: "Recording"}
	record := core.AudioAnalysis{TrackID: track.ID, CatalogVersion: "fixture", TrackKey: core.ProvisionalRecordingKey(track), Model: service.Analyzer.Identity(), Identity: core.PreviewIdentity{Provider: "deezer", ProviderID: track.ID, Status: core.ResolutionResolved}, AudioSHA256: strings.Repeat("0", 64), Segments: []core.AudioSegment{{StartSeconds: 0, EndSeconds: 10, Embedding: []float32{1, 0}}}}
	record.ID = Fingerprint(record)
	if err := service.Store.Put(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	session, err := service.BeginWithBudget(context.Background(), intent, "fixture", nil, 2*AnalysisBudget)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	return session, service, track
}

type sessionFailureStore struct {
	ports.AnalysisStore
	findFailure  func(context.Context) error
	writeFailure error
}

func (s sessionFailureStore) Find(ctx context.Context, catalog, track, key string, model core.AudioModelIdentity) (core.AudioAnalysis, bool, error) {
	if s.findFailure != nil {
		return core.AudioAnalysis{}, false, s.findFailure(ctx)
	}
	return s.AnalysisStore.Find(ctx, catalog, track, key, model)
}

func (s sessionFailureStore) PutAssessment(ctx context.Context, value core.AudioAssessment) error {
	if s.writeFailure != nil {
		return s.writeFailure
	}
	return s.AnalysisStore.PutAssessment(ctx, value)
}

func TestSessionReadFailuresDistinguishBudgetFromActualErrors(t *testing.T) {
	for _, kind := range []string{"budget", "store", "parent"} {
		t.Run(kind, func(t *testing.T) {
			session, service, track := cachedSession(t, audioIntent())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("synthetic store failure")
			service.Store = sessionFailureStore{AnalysisStore: service.Store, findFailure: func(budget context.Context) error {
				switch kind {
				case "budget":
					session.cancel()
					return budget.Err()
				case "parent":
					cancel()
					return ctx.Err()
				default:
					return failure
				}
			}}
			assessment, err := session.Check(ctx, track, false)
			if assessment.Eligible {
				t.Fatal("unread analysis became eligible")
			}
			switch kind {
			case "budget":
				if err != nil || !session.ShouldStop() {
					t.Fatalf("budget became fatal: %v", err)
				}
			case "parent":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("parent cancellation lost: %v", err)
				}
			default:
				if !errors.Is(err, failure) {
					t.Fatalf("real storage failure was hidden: %v", err)
				}
			}
		})
	}
}

func TestSessionAssessmentHelpersAndStorageFailures(t *testing.T) {
	criterion := core.MusicalCriterion{Kind: "style", Value: "electronic", Scope: "journey_start"}
	session, service, track := cachedSession(t, core.MusicIntent{EssentialCriteria: []core.MusicalCriterion{criterion}})
	if !session.Calibrated() || session.limit <= CandidateAnalysisLimit(0) {
		t.Fatal("extended session policy/budget not captured")
	}
	if _, ok := session.Assessment(track.ID); ok || session.Criterion(track.ID, criterion) != core.EvidenceUnknown {
		t.Fatal("unassessed recording has evidence")
	}
	if _, ok := session.StageSimilarity(track.ID, criterion); ok {
		t.Fatal("unassessed stage has a score")
	}
	assessment, err := session.Check(context.Background(), track, false)
	if err != nil || !assessment.Eligible || session.Criterion(track.ID, criterion) != core.EvidenceMatch {
		t.Fatalf("cached matching stage failed: %+v %v", assessment, err)
	}
	if score, ok := session.StageSimilarity(track.ID, criterion); !ok || score != 1 {
		t.Fatalf("stage score=%v available=%v", score, ok)
	}
	if got, ok := session.Assessment(track.ID); !ok || !reflect.DeepEqual(got, assessment) {
		t.Fatal("assessment helper changed evidence")
	}
	again, err := session.Check(context.Background(), track, true)
	if err != nil || !reflect.DeepEqual(assessment, again) || len(session.Snapshot().Assessments) != 1 {
		t.Fatal("rechecking duplicated or altered evidence", err)
	}
	fresh, err := service.Begin(context.Background(), audioIntent(), "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	failure := errors.New("assessment persistence failed")
	service.Store = sessionFailureStore{AnalysisStore: service.Store, writeFailure: failure}
	if _, err := fresh.Check(context.Background(), track, false); !errors.Is(err, failure) {
		t.Fatalf("assessment write failure hidden: %v", err)
	}
}

func TestMissingOptionalComparisonsNeverBecomeCheckedTracks(t *testing.T) {
	for _, calibrated := range []bool{false, true} {
		intent := core.MusicIntent{VerificationPolicy: core.BestAvailable, Preferences: core.SemanticPreferences{Moods: []core.IntentPreference{{Value: "relaxing", Influence: core.InfluencePositive}}}}
		session, service, track := cachedSession(t, intent)
		analyzer := service.Analyzer.(*testAnalyzer)
		service.Analyzer = &vocalEncoder{testAnalyzer: *analyzer, textFailure: true}
		if !calibrated {
			service.Policy = Policy{}
		}
		assessment, err := session.Check(context.Background(), track, false)
		if err != nil || assessment.Eligible || len(assessment.Clauses) != 1 || assessment.Clauses[0].ScoreAvailable {
			t.Fatalf("calibrated=%v missing comparison became eligible: %+v %v", calibrated, assessment, err)
		}
	}
}

func TestSessionRequiresAnyUsableClauseAndMatchingJourneyStage(t *testing.T) {
	for _, intent := range []core.MusicIntent{
		{},
		{EssentialCriteria: []core.MusicalCriterion{{Kind: "style", Value: "rock", Scope: "journey_start"}}},
	} {
		session, _, track := cachedSession(t, intent)
		assessment, err := session.Check(context.Background(), track, false)
		if err != nil || assessment.Eligible {
			t.Fatalf("absent/mismatching clauses passed: %+v %v", assessment, err)
		}
	}
	session, _, track := cachedSession(t, audioIntent())
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := session.Check(ctx, track, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("already expired parent: %v", err)
	}
}

func TestDescriptionAndHardClauseMapping(t *testing.T) {
	clauses := Clauses(core.MusicIntent{OriginalDescription: "a floating musical texture"})
	if len(clauses) != 1 || clauses[0].Kind != "description" || clauses[0].Text != "a floating musical texture" {
		t.Fatalf("description fallback lost: %+v", clauses)
	}
	clauses = Clauses(core.MusicIntent{HardConstraints: []core.HardConstraint{{Kind: "require_style", Value: "electronic"}, {Kind: "require_vocals", Value: "true"}, {Kind: "require_album", Value: "album"}}})
	if len(clauses) != 2 || clauses[0].Kind != "style" || clauses[1].Kind != "vocal" || clauses[1].Negative || !clauses[0].Strict || !clauses[1].Strict {
		t.Fatalf("strict requirements changed meaning: %+v", clauses)
	}
}
