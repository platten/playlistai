package audio

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestDSPStandaloneCachesWithoutModel(t *testing.T) {
	s, _, resolver, dir := testService(t)
	s.DSPStore = s.Store.(*Store).DSP()
	s.Analyzer, s.Store, s.ParityValidated = nil, nil, false
	ref := core.TrackRef{ID: "123", Artist: "Synthetic", Title: "Silence"}
	ctx := context.Background()
	first, bytes, err := s.AnalyzeDSPPreview(ctx, ref, "catalog")
	if err != nil || bytes == 0 || first.ID == "" || first.SampleRate != 44100 {
		t.Fatalf("standalone: %+v bytes=%d err=%v", first, bytes, err)
	}
	second, bytes, err := s.AnalyzeDSPPreview(ctx, ref, "catalog")
	if err != nil || bytes != 0 || resolver.calls != 1 || !reflect.DeepEqual(first, second) {
		t.Fatalf("cache did work again: bytes=%d calls=%d err=%v", bytes, resolver.calls, err)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Name() != "audio-analysis.sqlite" {
			t.Fatalf("unexpected retained file: %s", f.Name())
		}
	}
	// Only JSON feature data and identity/coverage are exposed by the store.
	if _, err := os.Stat(filepath.Join(dir, "preview.mp3")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preview persisted")
	}
	s.Authorized = false
	if _, _, err := s.AnalyzeDSPPreview(ctx, ref, "catalog"); err == nil {
		t.Fatal("authorization bypassed")
	}
	if resolver.calls != 1 {
		t.Fatal("unauthorized resolver request")
	}
}

func TestDSPCombinedPathSharesFetchAndPreservesCLAP(t *testing.T) {
	s, analyzer, _, _ := testService(t)
	store := s.Store.(*Store)
	ref := core.TrackRef{ID: "123", Artist: "Synthetic", Title: "Silence"}
	ctx := context.Background()
	plain, _, err := s.AnalyzePreview(ctx, ref, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.DSP().Usage(ctx)
	if err != nil || u.Records != 0 {
		t.Fatal("disabled path populated DSP")
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); _, _ = w.Write(syntheticMP3()) }))
	defer server.Close()
	s.Resolver = &testResolver{url: server.URL}
	s.AllowPreviewURL = func(u *url.URL) bool { return u.String() == server.URL }
	s.DSPStore = store.DSP()
	combined, n, err := s.AnalyzePreview(ctx, ref, "catalog")
	if err != nil || calls.Load() != 1 || n != int64(len(syntheticMP3())) {
		t.Fatalf("combined: calls=%d n=%d err=%v", calls.Load(), n, err)
	}
	// Timestamps/IDs are intentionally different; the legacy model input and
	// all semantic/cache identity inputs remain unchanged for the same interval.
	plain.ID, combined.ID, plain.AnalyzedAt, combined.AnalyzedAt = "", "", "", ""
	if !reflect.DeepEqual(plain, combined) {
		t.Fatal("enabling DSP changed CLAP result")
	}
	dsp, ok, err := s.DSPStore.Find(ctx, "catalog", ref.ID, core.ProvisionalRecordingKey(ref), DSPAnalysisVersion)
	if err != nil || !ok || dsp.AudioSHA256 != combined.AudioSHA256 || dsp.Coverage.EndSeconds > combined.Coverage.EndSeconds {
		t.Fatalf("DSP provenance mismatch: %+v %v", dsp, err)
	}
	for _, sample := range analyzer.borrowed {
		if sample != 0 {
			t.Fatal("CLAP PCM retained")
		}
	}
	if _, n, err := s.AnalyzeDSPPreview(ctx, ref, "catalog"); err != nil || n != 0 || calls.Load() != 1 {
		t.Fatal("combined evidence was not reused")
	}
}

func TestDSPCancellationAndInvalidPreviewHaveNoCacheSideEffects(t *testing.T) {
	s, _, resolver, _ := testService(t)
	s.DSPStore = s.Store.(*Store).DSP()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ref := core.TrackRef{ID: "123", Artist: "Synthetic", Title: "Silence"}
	if _, _, err := s.AnalyzeDSPPreview(ctx, ref, "catalog"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if resolver.calls != 0 {
		t.Fatal("canceled request resolved audio")
	}
	resolver.url = "https://not-deezer.invalid/preview"
	if _, _, err := s.AnalyzeDSPPreview(context.Background(), ref, "catalog"); err == nil {
		t.Fatal("foreign preview accepted")
	}
	u, err := s.DSPStore.Usage(context.Background())
	if err != nil || u.Records != 0 {
		t.Fatal("failed request persisted DSP")
	}
}

func TestDSPIntervalUsesOnlyObservedSourceFrames(t *testing.T) {
	s, _, _, _ := testService(t)
	s.DSPStore = s.Store.(*Store).DSP()
	original := DecodedPCM{Samples: make([]float32, 2*44100), SampleRate: 44100, Channels: 2}
	ref := core.TrackRef{ID: "123", Artist: "Synthetic", Title: "Silence"}
	identity := core.PreviewIdentity{Provider: "deezer", ProviderID: "123", Status: core.ResolutionResolved}
	a, err := s.storeDSPInterval(context.Background(), ref, "catalog", identity, storedRepresentation().AudioSHA256, original, 13, 47001)
	if err != nil {
		t.Fatal(err)
	}
	if a.Coverage.StartSeconds < 13.0/48000 || a.Coverage.EndSeconds > 47001.0/48000 {
		t.Fatal("DSP included unobserved/outside samples")
	}
	if a.Coverage.CoveredSeconds != a.Coverage.EndSeconds-a.Coverage.StartSeconds { // floating subtraction may differ by epsilon
		if delta := a.Coverage.CoveredSeconds - (a.Coverage.EndSeconds - a.Coverage.StartSeconds); delta > 1e-9 || delta < -1e-9 {
			t.Fatal("incorrect source coverage")
		}
	}
}

func TestCachedDSPIsIndependentOfCLAPReanalysis(t *testing.T) {
	s, analyzer, _, _ := testService(t)
	s.DSPStore = s.Store.(*Store).DSP()
	ctx := context.Background()
	ref := core.TrackRef{ID: "123", Artist: "Synthetic", Title: "Silence"}
	original, _, err := s.AnalyzeDSPPreview(ctx, ref, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	for _, revision := range []string{"first", "upgraded"} {
		analyzer.model.Revision = revision
		if _, _, err := s.AnalyzePreview(ctx, ref, "catalog"); err != nil {
			t.Fatal(err)
		}
		got, ok, err := s.DSPStore.Find(ctx, "catalog", ref.ID, core.ProvisionalRecordingKey(ref), DSPAnalysisVersion)
		if err != nil || !ok || !reflect.DeepEqual(original, got) {
			t.Fatalf("CLAP %s replaced DSP: %v", revision, err)
		}
		u, err := s.DSPStore.Usage(ctx)
		if err != nil || u.Records != 1 {
			t.Fatalf("CLAP upgrade duplicated DSP: %+v %v", u, err)
		}
	}
}

func TestDSPMeasuresOriginalChannelsAndInvalidatesChangedAudio(t *testing.T) {
	s, _, _, _ := testService(t)
	s.DSPStore = s.Store.(*Store).DSP()
	ctx := context.Background()
	original := DecodedPCM{Samples: make([]float32, 2*48000), SampleRate: 48000, Channels: 2}
	for i := 0; i < 48000; i++ {
		original.Samples[2*i] = float32(.5 * math.Sin(2*math.Pi*100*float64(i)/48000))
		original.Samples[2*i+1] = -original.Samples[2*i]
	}
	clap, err := clapFromOriginal(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range clap {
		if sample != 0 {
			t.Fatal("fixture was not anti-phase")
		}
	}
	ref := core.TrackRef{ID: "123", Artist: "Synthetic", Title: "Tone"}
	identity := core.PreviewIdentity{Provider: "deezer", ProviderID: "123", Status: core.ResolutionResolved}
	a, err := s.storeDSPInterval(ctx, ref, "catalog", identity, strings.Repeat("a", 64), original, 0, 48000)
	if err != nil || a.Features.RMSDBFS.Value == nil || *a.Features.RMSDBFS.Value < -10 {
		t.Fatalf("DSP used canceled mono power: %+v %v", a.Features.RMSDBFS, err)
	}
	// Cached coverage survives a newly chosen CLAP interval for identical bytes.
	same, err := s.storeDSPInterval(ctx, ref, "catalog", identity, a.AudioSHA256, original, 1000, 47000)
	if err != nil || !reflect.DeepEqual(a, same) {
		t.Fatal("CLAP interval replaced independently cached DSP")
	}
	changed, err := s.storeDSPInterval(ctx, ref, "catalog", identity, strings.Repeat("b", 64), original, 1000, 47000)
	if err != nil || changed.ID == a.ID || changed.Coverage.StartSeconds == a.Coverage.StartSeconds {
		t.Fatal("changed audio hash reused stale DSP")
	}
}
