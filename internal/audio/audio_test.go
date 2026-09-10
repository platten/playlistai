package audio

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/logging"
)

type testResolver struct {
	url   string
	calls int
}

func (r *testResolver) ResolveAudioPreview(_ context.Context, t core.TrackRef, _ core.EnrichedTrack) (core.ResolvedAudioPreview, error) {
	r.calls++
	return core.ResolvedAudioPreview{URL: r.url, Identity: core.PreviewIdentity{Provider: "deezer", ProviderID: t.ID, Artist: t.Artist, Title: t.Title, Status: core.ResolutionResolved, Method: "fixture"}}, nil
}

type testAnalyzer struct {
	model    core.AudioModelIdentity
	borrowed []float32
	fail     bool
	calls    int
}

func (a *testAnalyzer) Identity() core.AudioModelIdentity { return a.model }
func (a *testAnalyzer) EmbedAudio(_ context.Context, samples []float32) ([]float32, error) {
	a.borrowed = samples
	a.calls++
	if a.fail {
		return nil, errors.New("fixture failure")
	}
	return []float32{1, 0}, nil
}
func (a *testAnalyzer) EmbedText(_ context.Context, text string) ([]float32, error) {
	if text == "rock" || text == "sleepy" {
		return []float32{0, 1}, nil
	}
	return []float32{1, 0}, nil
}

// MPEG-1 Layer III zero-data frames: synthetic digital silence, no recording.
func syntheticMP3() []byte {
	var out []byte
	for i := 0; i < 12; i++ {
		frame := make([]byte, 417)
		copy(frame, []byte{0xff, 0xfb, 0x90, 0x00})
		out = append(out, frame...)
	}
	return out
}

func testService(t *testing.T) (*Service, *testAnalyzer, *testResolver, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(syntheticMP3()) }))
	t.Cleanup(server.Close)
	analyzer := &testAnalyzer{model: core.AudioModelIdentity{Model: "fixture", Revision: "1", Preprocessing: PreprocessingVersion, Runtime: "fixture", Dimension: 2}}
	resolver := &testResolver{url: server.URL}
	return &Service{Resolver: resolver, Analyzer: analyzer, Store: store, Authorized: true, ParityValidated: true, Policy: Policy{Version: "fixture/v1", DevelopmentSet: "synthetic-control-only", MinimumPositive: 0.6, MaximumNegative: 0.2}, AllowPreviewURL: func(u *url.URL) bool { return u.String() == server.URL }}, analyzer, resolver, dir
}
func audioIntent() core.MusicIntent {
	return core.MusicIntent{Version: core.CurrentIntentVersion, OriginalDescription: "ambient electronica, relaxing but not sleepy", EssentialCriteria: []core.MusicalCriterion{{Kind: "style", Value: "ambient electronica", Scope: "playlist"}}, Preferences: core.SemanticPreferences{Moods: []core.IntentPreference{{Value: "sleepy", Influence: core.InfluenceNegative}}}}.Normalized()
}

func TestVerticalSliceReusesFeaturesAndClearsAudio(t *testing.T) {
	s, a, r, dir := testService(t)
	diagnostics := &logging.Store{}
	diagnostics.SetDebug(true)
	ctx := logging.WithDiagnostics(context.Background(), diagnostics)
	track := core.TrackRef{ID: "1", Artist: "Synthetic", Title: "Silence"}
	first, err := s.Begin(ctx, audioIntent(), "catalog", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	assessment, err := first.Check(ctx, track, false)
	if err != nil || !assessment.Eligible {
		t.Fatalf("assessment=%+v err=%v", assessment, err)
	}
	if len(a.borrowed) != SegmentSamples {
		t.Fatal("wrong input shape")
	}
	for _, v := range a.borrowed {
		if v != 0 {
			t.Fatal("borrowed PCM survived analysis")
		}
	}
	secondIntent := audioIntent()
	secondIntent.OriginalDescription = "ambient electronica"
	secondIntent.Preferences.Moods = nil
	second, _ := s.Begin(ctx, secondIntent, "catalog", nil)
	defer second.Close()
	again, err := second.Check(ctx, track, false)
	if err != nil || !again.Eligible || r.calls != 1 || a.calls != 1 || assessment.IntentFingerprint == again.IntentFingerprint {
		t.Fatalf("cache reuse failed: %+v %v calls=%d", again, err, r.calls)
	}
	usage, _ := s.Store.Usage(ctx)
	if usage.Records != 1 || usage.Assessments != 2 {
		t.Fatalf("usage=%+v", usage)
	}
	files, _ := os.ReadDir(dir)
	for _, file := range files {
		if !strings.HasPrefix(file.Name(), "audio-analysis.sqlite") {
			t.Fatalf("unexpected persisted payload %s", file.Name())
		}
	}
	if second.Snapshot().CacheHits != 1 {
		t.Fatal("cache reuse not reported")
	}
	joined := ""
	for _, entry := range diagnostics.Read(0) {
		joined += entry.Text
	}
	if !strings.Contains(joined, `event="analysis.clap_audio_embedding"`) || !strings.Contains(joined, `"embedding":[1,0]`) {
		t.Fatalf("CLAP audio embedding missing from diagnostics: %s", joined)
	}
	if err := s.Store.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	usage, _ = s.Store.Usage(ctx)
	if usage.Records != 0 || usage.Assessments != 0 {
		t.Fatal("clear left analysis records")
	}
}

func TestFailedAnalysisReleasesPCMAndNeverPersists(t *testing.T) {
	s, a, _, _ := testService(t)
	a.fail = true
	session, _ := s.Begin(context.Background(), audioIntent(), "catalog", nil)
	defer session.Close()
	assessment, err := session.Check(context.Background(), core.TrackRef{ID: "1", Artist: "A", Title: "T"}, false)
	if err != nil || assessment.Eligible {
		t.Fatal("failed analysis became eligible")
	}
	for _, v := range a.borrowed {
		if v != 0 {
			t.Fatal("failed PCM was retained")
		}
	}
	usage, _ := s.Store.Usage(context.Background())
	if usage.Records != 0 {
		t.Fatal("failed record persisted")
	}
}

func TestStrictVocalAbsenceRemainsUnknown(t *testing.T) {
	s, _, _, _ := testService(t)
	intent := audioIntent()
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_vocals", Value: "true"}}
	session, _ := s.Begin(context.Background(), intent, "catalog", nil)
	defer session.Close()
	a, err := session.Check(context.Background(), core.TrackRef{ID: "1", Artist: "A", Title: "T"}, false)
	if err != nil || a.Eligible || a.Clauses[len(a.Clauses)-1].State != core.EvidenceUnknown {
		t.Fatalf("vocal absence was invented: %+v %v", a, err)
	}
}

func TestCacheVersionsAreNeverMixed(t *testing.T) {
	s, a, r, _ := testService(t)
	ctx := context.Background()
	track := core.TrackRef{ID: "1", Artist: "A", Title: "T"}
	for _, revision := range []string{"1", "2", "1"} {
		a.model.Revision = revision
		x, _ := s.Begin(ctx, audioIntent(), "catalog", nil)
		_, err := x.Check(ctx, track, false)
		x.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if r.calls != 2 {
		t.Fatalf("fetch count %d", r.calls)
	}
	usage, _ := s.Store.Usage(ctx)
	if usage.Records != 2 {
		t.Fatal("upgrade discarded old records")
	}
	track.Title = "T (Live)"
	x, _ := s.Begin(ctx, audioIntent(), "catalog", nil)
	defer x.Close()
	_, _ = x.Check(ctx, track, false)
	if r.calls != 3 {
		t.Fatal("changed identity reused old preview")
	}
}

func TestReplayReusesMusicalAssessmentAfterRuntimeEvidenceAdded(t *testing.T) {
	s, _, resolver, _ := testService(t)
	intent := audioIntent()
	track := core.TrackRef{ID: "one", Artist: "A", Title: "T"}
	first, err := s.Begin(context.Background(), intent, "catalog", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	assessment, err := first.Check(context.Background(), track, false)
	if err != nil {
		t.Fatal(err)
	}
	intent.Capabilities = append(intent.Capabilities, core.CapabilityStatus{Name: "audio_analysis", Status: "limited"})
	intent.AnchorAttempts = []core.InferredAnchor{{Role: "replayed", Suitability: core.AnchorSuitability{State: core.EvidenceMatch}}}
	replay, err := s.Begin(context.Background(), intent, "catalog", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	again, err := replay.Check(context.Background(), track, false)
	if err != nil || assessment.IntentFingerprint != again.IntentFingerprint || first.Snapshot().ID != replay.Snapshot().ID || resolver.calls != 1 {
		t.Fatalf("replay changed musical evidence: %+v %v", again, err)
	}
}

func TestStopAndBudgetsDoNotFetchUncheckedTracks(t *testing.T) {
	s, _, r, _ := testService(t)
	stop := make(chan struct{})
	session, _ := s.Begin(context.Background(), audioIntent(), "catalog", stop)
	defer session.Close()
	close(stop)
	a, err := session.Check(context.Background(), core.TrackRef{ID: "1", Artist: "A", Title: "T"}, false)
	if err != nil || a.Eligible || r.calls != 0 || !session.Snapshot().Stopped {
		t.Fatal("stop fetched or admitted a track")
	}
	for _, tc := range []struct{ count, want int }{{1, 40}, {10, 40}, {20, 80}, {100, 200}} {
		if got := CandidateAnalysisLimit(tc.count); got != tc.want {
			t.Fatalf("limit %d=%d", tc.count, got)
		}
	}
	x, _ := s.Begin(context.Background(), audioIntent(), "catalog", nil)
	defer x.Close()
	x.newCandidates = CandidateAnalysisLimit(x.intent.Count)
	a, err = x.Check(context.Background(), core.TrackRef{ID: "2", Artist: "A", Title: "T"}, false)
	if err != nil || a.Eligible || r.calls != 0 {
		t.Fatal("analysis count budget bypassed")
	}
}

func TestProviderGateAndRedirectDoNotLeakPayloads(t *testing.T) {
	s, _, _, _ := testService(t)
	s.Authorized = false
	if _, err := s.Begin(context.Background(), audioIntent(), "catalog", nil); err == nil {
		t.Fatal("permission gate bypassed")
	}
	s.AllowPreviewURL = nil
	if _, err := s.fetch(context.Background(), "https://example.com/preview"); err == nil {
		t.Fatal("foreign provider fetched")
	}
}

func TestPreprocessingAndCancellation(t *testing.T) {
	pcm, err := DecodeMP3(context.Background(), syntheticMP3())
	if err != nil || len(pcm) == 0 {
		t.Fatalf("synthetic MP3: %v", err)
	}
	defer clear(pcm)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DecodeMP3(ctx, syntheticMP3()); err == nil {
		t.Fatal("cancel ignored")
	}
	input := []float32{0.1, 0.2, 0.3}
	var borrowed []float32
	err = forEachSegment(context.Background(), input, func(segment []float32, start, end float64) error {
		borrowed = segment
		if start != 0 || end != 3.0/SampleRate || segment[0] != 0.1 || segment[3] != 0.1 {
			t.Fatal("coverage or repeat padding incorrect")
		}
		return errors.New("stop")
	})
	if err == nil {
		t.Fatal("visit error lost")
	}
	for _, v := range borrowed {
		if v != 0 {
			t.Fatal("segment retained")
		}
	}
}

func TestUnicodePretokenization(t *testing.T) {
	for _, input := range []string{" ambient electronica", "don't stop", "音楽 宇多田ヒカル", "  relaxed\n not sleepy", "a  b", "Bj\u00f6rk", "مرحبا بالعالم"} {
		if got := strings.Join(preTokens(input), ""); got != input {
			t.Fatalf("lost unicode/spacing: %q", got)
		}
	}
}

func TestAlbumMembershipIsNotAnAudioRequirement(t *testing.T) {
	service, _, _, _ := testService(t)
	intent := audioIntent()
	intent.HardConstraints = []core.HardConstraint{{Kind: "require_album", Value: "Resolved album"}}
	for _, clause := range Clauses(intent) {
		if clause.Kind == "require_album" {
			t.Fatal("album membership requires metadata, not acoustic proof")
		}
	}
	session, err := service.Begin(context.Background(), intent, "catalog", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	assessment, err := session.Check(context.Background(), core.TrackRef{ID: "album-member", Artist: "Synthetic", Title: "Silence"}, false)
	if err != nil || !assessment.Eligible {
		t.Fatalf("album constraint rejected audio: %+v %v", assessment, err)
	}
}
