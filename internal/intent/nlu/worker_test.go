package nlu

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/audio"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 5 && os.Args[1] == "--nlu-worker" {
		if os.Getenv("PLAYLISTAI_NLU_FAKE_WORKER") == "1" {
			fakeWorker()
			os.Exit(0)
		}
		if err := RunWorker(WorkerConfig{Kind: ModelKind(os.Args[2]), ModelDir: os.Args[3], RuntimeLibrary: os.Args[4]}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeWorker() {
	for {
		var request WorkerRequest
		if audio.ReadFrame(os.Stdin, &request, 1<<20) != nil {
			return
		}
		if request.Text == "wait" {
			time.Sleep(10 * time.Second)
		}
		response := WorkerResponse{Protocol: WorkerProtocol, Kind: ModelKind(os.Args[2]), ModelSHA256: "test-digest"}
		response.Result = Result{Abstained: true, Reason: "no_confident_proposals"}
		if os.Getenv("PLAYLISTAI_NLU_FAKE_UNTRAINED") == "1" {
			response.Result.Reason = "trained_head_unavailable"
		}
		if request.Text == "wrong-model" {
			response.ModelSHA256 = "another-digest"
		}
		if request.Text == "wrong-proposal" {
			response.Result = Result{Proposals: []Proposal{{Text: "invented", Start: 0, End: 999, Label: "artist:similarity", Score: .9}}}
		}
		if audio.WriteFrame(os.Stdout, response) != nil {
			return
		}
	}
}

func TestWorkerCancellationReapsAndCanRestart(t *testing.T) {
	t.Setenv("PLAYLISTAI_NLU_FAKE_WORKER", "1")
	w := &Worker{Config: WorkerConfig{Kind: DistilBERT, ModelDir: t.TempDir(), RuntimeLibrary: "unused"}, ExpectedModelSHA256: "test-digest"}
	defer w.Close()
	if err := w.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	pid := w.cmd.Process.Pid
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := w.Propose(ctx, "wait"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation: %v", err)
	}
	if w.cmd != nil {
		t.Fatal("canceled process not reaped")
	}
	if err := w.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.cmd.Process.Pid == pid {
		t.Fatal("stale native process reused")
	}
	w.Unload()
	if err := w.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Propose(context.Background(), "quiet"); err == nil {
		t.Fatal("closed worker restarted")
	}
}

func TestWorkerRejectsChangedModelAndInvalidProposal(t *testing.T) {
	t.Setenv("PLAYLISTAI_NLU_FAKE_WORKER", "1")
	w := &Worker{Config: WorkerConfig{Kind: DistilBERT, ModelDir: t.TempDir(), RuntimeLibrary: "unused"}, ExpectedModelSHA256: "test-digest"}
	defer w.Close()
	for _, text := range []string{"wrong-model", "wrong-proposal"} {
		if _, err := w.Propose(context.Background(), text); err == nil || w.cmd != nil {
			t.Fatalf("accepted %s or left process alive", text)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.Propose(ctx, "quiet"); !errors.Is(err, context.Canceled) || w.cmd != nil {
		t.Fatal("pre-canceled request spawned worker")
	}
}

func TestWorkerHealthCannotPromoteUntrainedExtractor(t *testing.T) {
	t.Setenv("PLAYLISTAI_NLU_FAKE_UNTRAINED", "1")
	t.Setenv("PLAYLISTAI_NLU_FAKE_WORKER", "1")
	w := &Worker{Config: WorkerConfig{Kind: DistilBERT, ModelDir: t.TempDir(), RuntimeLibrary: "unused"}}
	defer w.Close()
	if err := w.Health(context.Background()); err == nil {
		t.Fatal("inactive extractor counted as native health success")
	}
	result, err := w.Propose(context.Background(), "Aerosmith")
	if err != nil || !result.Abstained || result.Reason != "trained_head_unavailable" {
		t.Fatalf("missing-head proposal should still explicitly abstain: %+v %v", result, err)
	}
}

func TestServeWorkerUntrainedDistilBERTAbstainsWithoutInference(t *testing.T) {
	var input, output bytes.Buffer
	if err := audio.WriteFrame(&input, WorkerRequest{Protocol: WorkerProtocol, Text: "only Aerosmith"}); err != nil {
		t.Fatal(err)
	}
	if err := serveWorker(&input, &output, DistilBERT, modelSettings{digest: "test", abstention: "trained_head_unavailable"}, nil); err != nil {
		t.Fatal(err)
	}
	var response WorkerResponse
	if err := audio.ReadFrame(&output, &response, 1<<20); err != nil {
		t.Fatal(err)
	}
	if !response.Result.Abstained || len(response.Result.Proposals) != 0 || response.Error != "" {
		t.Fatalf("untrained result: %+v", response)
	}
	if _, err := io.ReadAll(&output); err != nil {
		t.Fatal(err)
	}
}

// Real reviewed extractors are opt-in; ordinary tests never acquire model files.
func TestNativeReviewedExtractorWithoutPython(t *testing.T) {
	dir, library := os.Getenv("PLAYLISTAI_TEST_NLU_EXTRACTOR_DIR"), os.Getenv("PLAYLISTAI_TEST_NLU_RUNTIME")
	if dir == "" || library == "" {
		t.Skip("set PLAYLISTAI_TEST_NLU_EXTRACTOR_DIR and PLAYLISTAI_TEST_NLU_RUNTIME for native inference")
	}
	t.Setenv("PLAYLISTAI_NLU_FAKE_WORKER", "")
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	t.Setenv("PYTHONHOME", empty)
	t.Setenv("PYTHONPATH", empty)
	w := &Worker{Config: WorkerConfig{Kind: DistilBERT, ModelDir: dir, RuntimeLibrary: library}}
	defer w.Close()
	if err := w.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Propose(context.Background(), "like Aerosmith"); err != nil {
		t.Fatal(err)
	}
}
