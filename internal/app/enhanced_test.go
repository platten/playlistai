package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func TestEnhancedAnalysisOptionalPreferencesAndResponsiveStatus(t *testing.T) {
	ctx := context.Background()
	c, err := New(ctx, testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := c.GetEnhancedAnalysisStatus(ctx)
	if err != nil || s.Enabled || !s.DSPAvailable || s.Installed || s.MERTAvailable {
		t.Fatalf("optional status %+v %v", s, err)
	}
	if err := c.SetRecommendationMode(core.EnhancedHybrid); err != nil {
		t.Fatal(err)
	}
	if err := c.SetEnhancedAnalysisEnabled(true); err != nil {
		t.Fatal(err)
	}
	prefs := config.LoadPrefs(c.cfg.DataDir)
	if !prefs.EnhancedAudioEnabled || prefs.RecommendationMode != string(core.EnhancedHybrid) {
		t.Fatal("preferences lost")
	}
	c.enhanced.opMu.Lock()
	done := make(chan error, 1)
	go func() { _, err := c.GetEnhancedAnalysisStatus(ctx); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("status blocked by ongoing analysis")
	}
	c.enhanced.opMu.Unlock()
	if err := c.ClearEnhancedAnalysis(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveMERT(); err != nil {
		t.Fatal(err)
	}
	if err := c.SetEnhancedAnalysisEnabled(false); err != nil {
		t.Fatal(err)
	}
	s, _ = c.GetEnhancedAnalysisStatus(ctx)
	if s.Enabled || !s.DSPAvailable {
		t.Fatal("remove disabled DSP capability")
	}
	if snapshot, err := c.PrepareEnhancedAudio(ctx, core.MusicIntent{}, core.TasteProfile{}, nil); err != nil || snapshot != nil {
		t.Fatal("legacy mode consumed enhanced evidence")
	}
}

func TestRecommendedMERTStatusAndInstallPreconditions(t *testing.T) {
	c := &Container{cfg: testConfig(t)}
	s, err := c.GetEnhancedAnalysisStatus(context.Background())
	if err != nil || s.RecommendedManifestURL != "" || s.RecommendedDownloadBytes != 0 || s.UnsupportedReason == "" {
		t.Fatalf("unavailable storage advertised download: %+v %v", s, err)
	}
	if err = c.InstallRecommendedMERT(context.Background(), nil); err == nil {
		t.Fatal("installed without storage")
	}
	if err = c.InstallMERT(context.Background(), "does-not-exist", nil); err == nil {
		t.Fatal("local install accepted missing storage")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = c.InstallRecommendedMERT(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled recommended install: %v", err)
	}
	if err = c.InstallIntentModels(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled default intent install: %v", err)
	}
	if err = c.InstallMERT(ctx, "does-not-exist", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled local install: %v", err)
	}
	if audio.NativeInferenceAvailable() {
		return
	}
	if err = c.InstallIntentModels(context.Background(), nil); err == nil {
		t.Fatal("non-native build attempted intent installation")
	}
}

type refreshFeedbackFixture struct {
	ports.FeedbackStore
	events []core.FeedbackEvent
	reads  int
}

func (f *refreshFeedbackFixture) ListFeedback(context.Context, ports.FeedbackQuery) ([]core.FeedbackEvent, error) {
	f.reads++
	return append([]core.FeedbackEvent(nil), f.events...), nil
}

func TestEnhancedRefreshFreezesTasteWhileAddingCompletedEvidence(t *testing.T) {
	for _, cold := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing taste", true: "cold start"}[cold], func(t *testing.T) {
			ctx := context.Background()
			c, err := New(ctx, testConfig(t), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			cat := fakes.NewCatalog(2,
				fakes.CatalogTrack{ID: "a", Display: "Artist A - Track", Audio: []float32{1, 0}, Track: []float32{1, 0}},
				fakes.CatalogTrack{ID: "b", Display: "Artist B - Track", Audio: []float32{1, 0}, Track: []float32{1, 0}},
				fakes.CatalogTrack{ID: "c", Display: "Artist C - Track", Audio: []float32{0, 1}, Track: []float32{0, 1}})
			c.runtime.Catalog, c.runtime.Resolver = cat, cat
			model := core.AudioRepresentationIdentity{Model: "fixture", Revision: "1", Preprocessing: "fixture/v1", Runtime: "fixture/v1", Dimension: 2, WeightsSHA256: strings.Repeat("a", 64), Pooling: "mean-l2/v1"}
			// The inert worker supplies identity only. All representation reads
			// use fixture rows; no native process or preview acquisition runs.
			c.enhanced.enabled = true
			c.enhanced.worker = &audio.MERTWorker{Model: model}
			c.enhanced.manifest = &audio.MERTBundleManifest{Model: model}
			feedback := &refreshFeedbackFixture{FeedbackStore: c.Feedback}
			c.Feedback = feedback
			put := func(id string, vector []float32) core.TrackRef {
				t.Helper()
				meta, _ := cat.Meta(id)
				row := core.AudioRepresentation{TrackID: id, CatalogVersion: cat.CatalogVersion(), TrackKey: core.ProvisionalRecordingKey(meta.Ref),
					Identity: core.PreviewIdentity{Status: core.ResolutionResolved, Provider: "deezer", ProviderID: id}, Model: model, AudioSHA256: strings.Repeat("b", 64),
					Pooled: vector, Segments: []core.AudioRepresentationSegment{{StartSeconds: 0, EndSeconds: 5, Vector: vector}},
					Coverage: core.PreviewCoverage{Available: true, Source: "deezer", EndSeconds: 5, CoveredSeconds: 5}, AnalyzedAt: "2026-09-12T12:00:00Z"}
				row.ID = audio.Fingerprint(row)
				if err := c.analysis.store.Representations().Put(ctx, row); err != nil {
					t.Fatal(err)
				}
				return meta.Ref
			}
			put("a", []float32{1, 0})
			put("b", []float32{1, 0})
			if !cold {
				feedback.events = []core.FeedbackEvent{
					{ID: "1", TrackID: "a", Type: core.FeedbackLike, Scope: core.FeedbackScopeDurable, OccurredAt: time.Unix(1, 0)},
					{ID: "2", TrackID: "b", Type: core.FeedbackLike, Scope: core.FeedbackScopeDurable, OccurredAt: time.Unix(2, 0)},
				}
			}
			intent := core.MusicIntent{Controls: core.IntentControls{RecommendationMode: core.EnhancedHybrid}}
			profile := core.TasteProfile{CatalogVersion: cat.CatalogVersion(), AsOf: time.Unix(2, 0), ColdStart: cold}
			first, err := c.PrepareEnhancedAudio(ctx, intent, profile, nil)
			if err != nil || first == nil || feedback.reads != 1 {
				t.Fatalf("initial taste capture: %v reads=%d", err, feedback.reads)
			}
			initial := first.Input()
			if cold && len(initial.PositiveCentroid) != 0 || !cold && !reflect.DeepEqual(initial.PositiveCentroid, []float32{1, 0}) {
				t.Fatalf("wrong initial taste: %+v", initial.PositiveCentroid)
			}
			// Feedback changes after the request snapshot. A historical JSON
			// round trip must preserve both known centroids and an empty cold start.
			feedback.events = []core.FeedbackEvent{
				{ID: "3", TrackID: "a", Type: core.FeedbackDislike, Scope: core.FeedbackScopeDurable, OccurredAt: time.Unix(3, 0)},
				{ID: "4", TrackID: "b", Type: core.FeedbackDislike, Scope: core.FeedbackScopeDurable, OccurredAt: time.Unix(4, 0)},
			}
			raw, err := json.Marshal(first)
			if err != nil {
				t.Fatal(err)
			}
			var saved core.EnhancedAudioSnapshot
			if err := json.Unmarshal(raw, &saved); err != nil {
				t.Fatal(err)
			}
			ref := put("c", []float32{0, 1})
			refreshed, err := c.RefreshEnhancedAudio(ctx, intent, profile, []core.TrackRef{ref}, &saved)
			if err != nil || refreshed == nil {
				t.Fatalf("refresh: %v", err)
			}
			got := refreshed.Input()
			if feedback.reads != 1 || !reflect.DeepEqual(got.PositiveCentroid, initial.PositiveCentroid) || !reflect.DeepEqual(got.NegativeCentroid, initial.NegativeCentroid) {
				t.Fatalf("feedback changed frozen taste: reads=%d positive=%v negative=%v", feedback.reads, got.PositiveCentroid, got.NegativeCentroid)
			}
			if got.Representations["c"].TrackID != "c" || first.Fingerprint() != saved.Fingerprint() {
				t.Fatal("refresh lost completed track evidence or mutated saved input")
			}
			incompatible := saved.Input()
			incompatible.Model.Revision = "other"
			other, _ := core.NewEnhancedAudioSnapshot(incompatible)
			if _, err := c.RefreshEnhancedAudio(ctx, intent, profile, nil, other); err == nil {
				t.Fatal("refresh mixed incompatible representation spaces")
			}
		})
	}
}

type forbiddenRefreshPreview struct{ calls int }

func (r *forbiddenRefreshPreview) ResolveAudioPreview(context.Context, core.TrackRef, core.EnrichedTrack) (core.ResolvedAudioPreview, error) {
	r.calls++
	panic("cache-only refresh attempted to fetch a preview")
}

func TestEnhancedRefreshNeverAcquiresMissingEvidence(t *testing.T) {
	ctx := context.Background()
	c, err := New(ctx, testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cat := fakes.NewCatalog(1, fakes.CatalogTrack{ID: "missing", Display: "Artist - Track", Audio: []float32{1}, Track: []float32{1}})
	c.runtime.Catalog, c.runtime.Resolver = cat, cat
	r := &forbiddenRefreshPreview{}
	c.analysis.enabled = true
	c.analysis.service = &audio.Service{Resolver: r, Authorized: true, DSPStore: c.analysis.store.DSP()}
	c.enhanced.enabled = true
	intent := core.MusicIntent{Controls: core.IntentControls{RecommendationMode: core.EnhancedHybrid}}
	got, err := c.RefreshEnhancedAudio(ctx, intent, core.TasteProfile{}, []core.TrackRef{{ID: "missing", Artist: "Artist", Title: "Track"}}, nil)
	if err != nil || got == nil || r.calls != 0 || audio.EnhancedBudgetFor(ctx) != nil {
		t.Fatalf("refresh unexpectedly acquired or failed: snapshot=%v calls=%d err=%v", got, r.calls, err)
	}
}
