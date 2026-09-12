package audio

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"testing/synctest"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type orderedDSPStore struct {
	ports.DSPStore
	before func()
	calls  int
}

func (s *orderedDSPStore) Find(ctx context.Context, catalog, id, key, version string) (core.DSPAnalysis, bool, error) {
	s.calls++
	s.before()
	return s.DSPStore.Find(ctx, catalog, id, key, version)
}

type orderedMERTAnalyzer struct {
	mertFake
	before func()
}

func (a *orderedMERTAnalyzer) EmbedAudio(ctx context.Context, pcm []float32) ([]float32, error) {
	a.before()
	// A nonzero sentinel makes post-return buffer clearing observable even
	// though the synthetic decoder fixture contains only digital silence.
	pcm[0] = .25
	return a.mertFake.EmbedAudio(ctx, pcm)
}

func TestCLAPRetainedBeforeOptionalDSPAndMERTFailureOrCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "invalid optional output", true: "parent canceled by optional work"}[canceled], func(t *testing.T) {
			preview, clap, resolver, _ := testService(t)
			store := preview.Store.(*Store)
			ref := core.TrackRef{ID: "priority", Artist: "Synthetic", Title: "Silence"}
			retained := func() {
				t.Helper()
				row, hit, err := store.Find(context.Background(), "catalog", ref.ID, core.ProvisionalRecordingKey(ref), clap.Identity())
				if err != nil || !hit || row.ID == "" || clap.calls != 1 {
					t.Fatalf("optional work ran before retained CLAP: row=%+v hit=%v calls=%d err=%v", row, hit, clap.calls, err)
				}
			}
			dsp := &orderedDSPStore{DSPStore: store.DSP(), before: retained}
			preview.DSPStore = dsp
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			fake := &orderedMERTAnalyzer{mertFake: mertFake{model: mertTestModel(), invalid: !canceled}, before: retained}
			if canceled {
				fake.cancel = cancel
			}
			preview.MERT = &MERTService{Preview: preview, Analyzer: fake, Store: store.Representations(), ParityValidated: true}
			ctx := WithLazyEnhancedBudget(parent, 1, time.Minute)
			row, n, err := preview.AnalyzePreview(ctx, ref, "catalog")
			if row.ID == "" || n == 0 || fake.calls != 1 || dsp.calls == 0 || resolver.calls != 1 {
				t.Fatalf("shared decode/fetch work lost: row=%+v n=%d MERT=%d DSP=%d fetch=%d err=%v", row, n, fake.calls, dsp.calls, resolver.calls, err)
			}
			if canceled && !errors.Is(err, context.Canceled) || !canceled && err != nil {
				t.Fatalf("parent cancellation/optional failure semantics changed: %v", err)
			}
			retained()
			usage, err := store.Representations().Usage(context.Background())
			if err != nil || usage.Records != 0 {
				t.Fatalf("failed optional result cached: %+v %v", usage, err)
			}
			for _, samples := range [][]float32{clap.borrowed, fake.borrowed} {
				for _, x := range samples {
					if x != 0 {
						t.Fatal("borrowed PCM retained after cancellation/failure")
					}
				}
			}
		})
	}
}

func TestCLAPFailureDoesNotStartOptionalWorkOrConsumeAdmission(t *testing.T) {
	preview, clap, _, _ := testService(t)
	clap.fail = true
	store := preview.Store.(*Store)
	dsp := &orderedDSPStore{DSPStore: store.DSP(), before: func() { t.Fatal("DSP ran after failed CLAP") }}
	preview.DSPStore = dsp
	fake := &mertFake{model: mertTestModel()}
	preview.MERT = &MERTService{Preview: preview, Analyzer: fake, Store: store.Representations(), ParityValidated: true}
	ctx := WithLazyEnhancedBudget(context.Background(), 1, time.Minute)
	row, _, err := preview.AnalyzePreview(ctx, core.TrackRef{ID: "failed", Artist: "Synthetic", Title: "Silence"}, "catalog")
	if err == nil || row.ID != "" || fake.calls != 0 || dsp.calls != 0 || EnhancedBudgetFor(ctx).Used() != 0 {
		t.Fatalf("failed CLAP spent optional work/admission: %+v MERT=%d DSP=%d err=%v", row, fake.calls, dsp.calls, err)
	}
	if !EnhancedBudgetFor(ctx).deadline.IsZero() {
		t.Fatal("failed CLAP started lazy optional deadline")
	}
}

func TestSessionFinishesTextAndVocalChecksBeforeOptionalDeadline(t *testing.T) {
	for _, vocals := range []bool{false, true} {
		t.Run(map[bool]string{false: "required text", true: "required vocal screen"}[vocals], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				store, err := OpenStore(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = store.Close() }()
				clap := &testAnalyzer{model: core.AudioModelIdentity{Model: "fixture", Revision: "1", Preprocessing: PreprocessingVersion, Runtime: "fixture", Dimension: 2}}
				resolver := &testResolver{url: "https://fixture.invalid/preview"}
				preview := &Service{Resolver: resolver, Analyzer: clap, Store: store, Authorized: true, ParityValidated: true,
					Policy:     Policy{Version: "fixture", DevelopmentSet: "synthetic", MinimumPositive: .6, MaximumNegative: .2},
					HTTPClient: &http.Client{Transport: enhancedDeadlineTransport{}}, AllowPreviewURL: func(u *url.URL) bool { return u.Hostname() == "fixture.invalid" }}
				intent := core.MusicIntent{EssentialCriteria: []core.MusicalCriterion{{Kind: "style", Value: "ambient", Scope: "playlist", Strength: "required"}}}
				if vocals {
					preview.Analyzer, preview.Policy = &vocalEncoder{testAnalyzer: *clap}, Policy{}
					intent = core.MusicIntent{HardConstraints: []core.HardConstraint{{Kind: "exclude_vocals", Value: "vocals"}}}
				}
				fake := &budgetExpiryAnalyzer{mertFake: mertFake{model: mertTestModel()}, before: func() {
					usage, err := store.Usage(context.Background())
					if err != nil || usage.Records != 1 || usage.Assessments != 1 {
						t.Fatalf("optional inference ran before complete CLAP assessment: %+v %v", usage, err)
					}
				}}
				preview.MERT = &MERTService{Preview: preview, Analyzer: fake, Store: store.Representations(), ParityValidated: true}
				parent := WithLazyEnhancedBudget(context.Background(), 1, time.Minute)
				session, err := preview.BeginWithBudget(parent, intent, "catalog", nil, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				defer session.Close()
				got, err := session.Check(parent, core.TrackRef{ID: "priority", Artist: "Synthetic", Title: "Silence"}, false)
				if err != nil || !got.Eligible || len(got.Clauses) != 1 || got.Clauses[0].State != core.EvidenceMatch || !got.Clauses[0].ScoreAvailable {
					t.Fatalf("completed current-generation CLAP check was lost: %+v %v", got, err)
				}
				if fake.calls != 1 || !fake.enteredBeforeDeadline || !errors.Is(fake.err, context.DeadlineExceeded) || parent.Err() != nil || resolver.calls != 1 {
					t.Fatalf("optional timeout/fetch contract changed: calls=%d entered=%v err=%v parent=%v fetches=%d", fake.calls, fake.enteredBeforeDeadline, fake.err, parent.Err(), resolver.calls)
				}
				if snapshot := session.Snapshot(); !snapshot.BudgetExhausted || len(snapshot.Assessments) != 1 || !snapshot.Assessments[0].Eligible {
					t.Fatalf("completed assessment/budget state lost from snapshot: %+v", snapshot)
				}
			})
		})
	}
}
