package audio

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
)

// The test executable doubles as a deliberately failing native worker. This
// checks process cleanup and restart without downloading an inference runtime.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "--bundle" && strings.HasPrefix(os.Args[2], "test:mert-") {
		runMERTTestWorker(os.Args[2])
		os.Exit(0)
	}
	if len(os.Args) == 3 && os.Args[1] == "--bundle" && strings.HasPrefix(os.Args[2], "test:") {
		var request WorkerRequest
		if ReadFrame(os.Stdin, &request, 16<<20) != nil {
			os.Exit(2)
		}
		switch os.Args[2] {
		case "test:hang":
			time.Sleep(time.Minute)
		case "test:healthy":
			_ = WriteFrame(os.Stdout, WorkerResponse{Protocol: WorkerProtocol, Vector: []float32{1, 0}})
		}
		clear(request.Audio)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestWorkerCancellationAndCrashPermitRestart(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	w := &Worker{Executable: executable, BundleDir: "test:hang"}
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := w.EmbedAudio(ctx, make([]float32, SegmentSamples)); err == nil {
		t.Fatal("hung worker succeeded")
	}
	if w.cmd != nil {
		t.Fatal("canceled worker was not reaped")
	}
	w.BundleDir = "test:crash"
	if err := w.Health(context.Background()); err == nil {
		t.Fatal("crashed worker succeeded")
	}
	if w.cmd != nil {
		t.Fatal("crashed worker was not reaped")
	}
	w.BundleDir = "test:healthy"
	if err := w.Health(context.Background()); err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	w.Unload()
	if w.cmd != nil {
		t.Fatal("disabled worker still resident")
	}
	if err := w.Health(context.Background()); err != nil {
		t.Fatalf("enable after unload failed: %v", err)
	}
	_ = w.Close()
	if err := w.Health(context.Background()); err == nil || w.cmd != nil {
		t.Fatal("retired worker restarted")
	}
}

func TestBundlePreprocessingCannotSilentlyChangeCompiledContract(t *testing.T) {
	config := preprocessingConfig{Version: PreprocessingVersion, SamplingRate: SampleRate, SegmentSamples: SegmentSamples, FFTSize: 1024, HopSize: 480, MelBins: MelBins, MinimumFrequency: 50, MaximumFrequency: 14000, MelScale: "slaney", Padding: "reflect", Floor: 1e-10}
	path := filepath.Join(t.TempDir(), "preprocessing.json")
	write := func() {
		raw, _ := json.Marshal(config)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if err := CheckPreprocessing(path); err != nil {
		t.Fatal(err)
	}
	config.SamplingRate = 44100
	write()
	if CheckPreprocessing(path) == nil {
		t.Fatal("incompatible preprocessing accepted")
	}
}

func TestSnapshotIdentityIgnoresExecutionMetrics(t *testing.T) {
	s := &Session{started: time.Now()}
	s.snapshot.Assessments = []core.AudioAssessment{{TrackID: "b", AnalysisID: "second"}, {TrackID: "a", AnalysisID: "first"}}
	first := s.Snapshot().ID
	s.snapshot.CacheHits = 10
	s.snapshot.NewAnalyses = 20
	s.snapshot.BytesFetched = 999
	s.snapshot.Assessments[0], s.snapshot.Assessments[1] = s.snapshot.Assessments[1], s.snapshot.Assessments[0]
	if s.Snapshot().ID != first {
		t.Fatal("execution metrics changed evidence identity")
	}
	s.snapshot.Assessments[0].AnalysisID = "different"
	if s.Snapshot().ID == first {
		t.Fatal("changed evidence retained identity")
	}
}
