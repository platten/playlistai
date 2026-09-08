package multichannel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/enrich/musicbrainz"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/preview/deezer"
	"github.com/platten/playlistai/internal/similarity/brute"
)

func TestInstrumentalPromptUsesCLAPWithoutGeneralCalibration(t *testing.T) {
	cat := testCatalog()
	service, resolver := cachedAudioService(t, cat)
	service.Policy = audio.Policy{}
	intent, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: "Instrumental, no vocals"})
	if err != nil {
		t.Fatal(err)
	}
	intent.Controls.TotalTrackCount = 2
	intent.Seed = "42"
	intent.VerificationPolicy = core.BestAvailable
	intent.Knowledge = &core.KnowledgeSnapshot{ID: "fixture"}
	for _, id := range []string{"audio", "cooc", "last", "other"} {
		meta, _ := cat.Meta(id)
		intent.Knowledge.Candidates = append(intent.Knowledge.Candidates, meta.Ref)
	}
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
	var checked []string
	result, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, OnChecked: func(track core.TrackRef) { checked = append(checked, track.ID) }})
	if err != nil || len(result.Tracks) != 2 || len(checked) != 2 {
		t.Fatalf("playlist=%+v checked=%v err=%v", result, checked, err)
	}
	for _, id := range checked {
		if id != "audio" && id != "last" {
			t.Fatalf("vocals reached progressive UI: %s", id)
		}
	}
	if resolver.calls != 0 || result.AudioEvidence == nil || !noticeCode(result.Notices, "vocal_preview_screening") {
		t.Fatal("missing cache/coverage evidence")
	}
	intent.RequiredTracks = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "cooc", Influence: core.InfluencePositive}}
	result, err = engine.Build(context.Background(), intent)
	if err != nil || result.Outcome.State != core.OutcomeNeedsClarification {
		t.Fatalf("required vocal track: %+v %v", result, err)
	}
	intent.RequiredTracks = nil
	result, err = New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || result.Outcome.State != core.OutcomeUnsupported || len(result.Tracks) != 0 || result.Outcome.Reasons[0].Code != "vocal_analysis_unavailable" {
		t.Fatalf("missing model: %+v %v", result, err)
	}
}

// Opt-in live smoke test. No downloaded audio survives analysis; only derived
// SQLite records are retained. This is not a listening evaluation or calibration.
func TestInstrumentalPromptLiveCLAP(t *testing.T) {
	bundle := os.Getenv("PLAYLISTAI_TEST_CLAP_BUNDLE")
	if bundle == "" {
		t.Skip("set PLAYLISTAI_TEST_CLAP_BUNDLE, PLAYLISTAI_TEST_CLAP_WORKER and PLAYLISTAI_TEST_CATALOG for live validation")
	}
	cat, err := catalog.Open(os.Getenv("PLAYLISTAI_TEST_CATALOG"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	manifest, err := audio.ReadRuntimeBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	worker := &audio.Worker{Executable: os.Getenv("PLAYLISTAI_TEST_CLAP_WORKER"), BundleDir: bundle, Model: manifest.Model}
	defer func() { _ = worker.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if err := worker.Health(ctx); err != nil {
		t.Fatal(err)
	}
	store, err := audio.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	knowledge, err := musicbrainz.New(musicbrainz.Config{UserAgent: "PlaylistAI/0.7.0 (https://github.com/platten/playlistai)", CachePath: filepath.Join(t.TempDir(), "metadata.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = knowledge.Close() }()
	intent, err := rules.New().Parse(ctx, ports.IntentInput{Prompt: "Instrumental, no vocals"})
	if err != nil {
		t.Fatal(err)
	}
	intent.Controls.TotalTrackCount = 3
	intent.Seed = "42"
	intent.VerificationPolicy = core.BestAvailable
	intent, err = knowledge.ResolveMusic(ctx, intent, cat, cat, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("online: %d candidates, %d anchors; %v", len(intent.Knowledge.Candidates), len(intent.InferredAnchors), intent.Knowledge.Notices)
	service := &audio.Service{Resolver: deezer.New(deezer.Config{}), Analyzer: worker, Store: store, Recordings: knowledge, Authorized: true, ParityValidated: manifest.Parity.Valid()}
	result, err := New(cat, brute.New(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service }).Build(ctx, intent)
	if err != nil || len(result.Tracks) == 0 {
		t.Fatalf("outcome=%+v evidence=%+v err=%v", result.Outcome, result.AudioEvidence, err)
	}
	t.Logf("%d tracks: %+v; outcome=%+v", len(result.Tracks), result.Tracks, result.Outcome)
	for _, track := range result.Tracks {
		passed := false
		for _, assessment := range result.AudioEvidence.Assessments {
			if assessment.TrackID == track.ID {
				passed = assessment.Eligible
			}
		}
		if !passed {
			t.Fatalf("unchecked selected track: %s", track.ID)
		}
	}
	t.Logf("CLAP: %d analyses, %d cache hits, %d bytes, %d ms", result.AudioEvidence.NewAnalyses, result.AudioEvidence.CacheHits, result.AudioEvidence.BytesFetched, result.AudioEvidence.ElapsedMilliseconds)
}
