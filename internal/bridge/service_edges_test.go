package bridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/updater"
)

func TestServiceEdgesAnalysisWrappersAndCancellation(t *testing.T) {
	for _, action := range []string{"disable", "clear", "remove", "install-invalid", "model-invalid", "download-unknown"} {
		t.Run(action, func(t *testing.T) {
			a := New(newTestContainer(t), nil)
			parse, _, finishParse := a.operations.begin(context.Background(), "intent-preview")
			defer finishParse()
			generation, _, finishGeneration := a.operations.begin(context.Background(), generationOperation)
			defer finishGeneration()
			playback, _, finishPlayback := a.operations.begin(context.Background(), "preview-playback")
			defer finishPlayback()
			var err error
			wantError := false
			switch action {
			case "disable":
				err = a.SetAnalysisEnabled(false)
			case "clear":
				err = a.ClearAnalysis(context.Background())
			case "remove":
				err = a.RemoveAnalysisModel()
			case "install-invalid":
				err, wantError = a.InstallAnalysisBundle(context.Background(), filepath.Join(t.TempDir(), "missing.json")), true
			case "model-invalid":
				err, wantError = a.UseModelFile(filepath.Join(t.TempDir(), "missing.gguf")), true
			case "download-unknown":
				err, wantError = a.DownloadModel("not-a-curated-model"), true
			}
			if (err != nil) != wantError {
				t.Fatalf("operation error=%v, wantError=%v", err, wantError)
			}
			if !errors.Is(parse.Err(), context.Canceled) || !errors.Is(generation.Err(), context.Canceled) {
				t.Fatal("service change left recommendation work active")
			}
			if playback.Err() != nil {
				t.Fatal("unrelated playback was canceled")
			}
			status, err := a.GetAnalysisStatus(context.Background())
			if err != nil || status.Installed || status.Enabled || status.Available {
				t.Fatalf("failed/disabled operation activated model: %+v %v", status, err)
			}
		})
	}
}

func TestServiceEdgesAnalysisCapabilityAndMalformedBundle(t *testing.T) {
	a := New(newTestContainer(t), nil)
	got, err := a.GetRecommendedAnalysisBundle()
	want, wantErr := audio.RecommendedBundle()
	if (err != nil) != (wantErr != nil) || got.ID != want.ID {
		t.Fatal("bridge misreported native bundle capability", err, wantErr)
	}
	path := filepath.Join(t.TempDir(), "bundle.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.InspectAnalysisBundle(path); err == nil {
		t.Fatal("malformed bundle accepted")
	}
	if err := a.SetAnalysisEnabled(true); err == nil {
		t.Fatal("uninstalled analysis enabled")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.ClearAnalysis(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("clear cancellation lost: %v", err)
	}
}

func TestServiceEdgesRecommendedAnalysisUnavailableStorage(t *testing.T) {
	a := New(&app.Container{}, nil)
	if err := a.InstallRecommendedAnalysisBundle(context.Background()); err == nil {
		t.Fatal("recommended installation ignored unavailable storage")
	}
}

func TestServiceEdgesRulesModelStatus(t *testing.T) {
	a := New(newTestContainer(t), nil)
	status := a.GetModelStatus()
	if status.Backend != "rules" || !status.Ready || status.ModelPath != "" || status.ModelLabel != "" || status.ModelID != "" {
		t.Fatalf("rules status: %+v", status)
	}
	got := a.GetLlamaRuntime()
	want, builds := a.app.LlamaRuntime()
	if got.Available != want.Available || got.Path != want.Path || got.Kind != string(want.Kind) || len(got.Builds) != len(builds) {
		t.Fatalf("runtime wrapper lost capability: %+v %+v", got, want)
	}
}

type failingMetadataService struct {
	metadataFixture
	failure error
}

func (m *failingMetadataService) ClearCache(context.Context) error { return m.failure }
func (m *failingMetadataService) SetDiscogsToken(string) error     { return m.failure }

func TestServiceEdgesMetadataFailureDoesNotDiscardReusableParse(t *testing.T) {
	failure := errors.New("metadata persistence failed")
	m := &failingMetadataService{failure: failure}
	a := New(&app.Container{Knowledge: m}, nil)
	key := a.intentCache.scopedKey("existing")
	a.intentCache.put(key, parsedIntentEntry{})
	if err := a.ClearMusicMetadataCache(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("clear failure not propagated: %v", err)
	}
	if _, exists := a.intentCache.get(key); !exists {
		t.Fatal("failed clear discarded valid cached parse")
	}
	if err := a.SetDiscogsToken("not-a-real-token"); !errors.Is(err, failure) {
		t.Fatalf("credential persistence failure: %v", err)
	}
	a.app.Knowledge = nil
	if _, err := a.GetMetadataStatus(); err == nil {
		t.Fatal("missing metadata status reported available")
	}
	if err := a.SetDiscogsToken(""); err == nil {
		t.Fatal("missing metadata service accepted token")
	}
	if err := a.ClearMusicMetadataCache(context.Background()); err == nil {
		t.Fatal("missing metadata service cleared cache")
	}
}

func TestServiceEdgesPreferenceFailuresStayVisible(t *testing.T) {
	c := newTestContainer(t)
	a := New(c, nil)
	before := a.GetPreviewProviderName()
	path := filepath.Join(c.Config().DataDir, "prefs.json")
	if err := os.WriteFile(path, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.SetPreviewProvider("spotify"); err == nil {
		t.Fatal("corrupt preferences hidden")
	}
	if a.GetPreviewProviderName() != before {
		t.Fatal("failed preference write switched preview provider")
	}
	if err := a.CompleteOnboarding(); err == nil {
		t.Fatal("failed onboarding persistence hidden")
	}
	if err := a.ClearModel(); err == nil {
		t.Fatal("failed model preference persistence hidden")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "{corrupt" {
		t.Fatalf("corrupt evidence overwritten: %q %v", got, err)
	}
}

func TestServiceEdgesUpdateWithoutDesktopOrRelease(t *testing.T) {
	a := New(newTestContainer(t), nil)
	a.updates = updater.New("dev") // development builds never contact release servers
	offer, err := a.CheckForUpdate(context.Background())
	if err != nil || offer.Available {
		t.Fatalf("development build offered update: %+v %v", offer, err)
	}
	if err := a.InstallUpdate(context.Background()); err == nil {
		t.Fatal("headless update was allowed")
	}
	a.CancelUpdate()
}

func TestServiceEdgesCanceledAndClosedCatalogReads(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled", true: "closed"}[closed], func(t *testing.T) {
			c := newLoadedContainer(t)
			a := New(c, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if closed {
				if err := c.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			a.ctx = ctx
			a.runtime = func() app.RuntimeSnapshot {
				t.Fatal("canceled operation read runtime resources")
				return app.RuntimeSnapshot{}
			}
			if result, err := a.GetPreviewURL(ctx, "t0"); !errors.Is(err, context.Canceled) || result.Available {
				t.Fatalf("preview=%+v err=%v", result, err)
			}
			if result, err := a.PrepareExport([]string{"t0"}); !errors.Is(err, context.Canceled) || len(result) != 0 {
				t.Fatalf("export=%+v err=%v", result, err)
			}
		})
	}
}
