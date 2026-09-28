package audio

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func runDiscogsTestWorker(mode string) {
	for {
		var request DiscogsWorkerRequest
		if ReadFrame(os.Stdin, &request, 32<<20) != nil {
			return
		}
		if mode == "test:discogs-hang" {
			time.Sleep(time.Minute)
			return
		}
		response := DiscogsWorkerResponse{Protocol: DiscogsWorkerProtocol}
		if !request.Health {
			response.Scores = [][]float32{make([]float32, 400), make([]float32, 40), make([]float32, 2), make([]float32, 2)}
		}
		if mode == "test:discogs-invalid" {
			response.Scores = [][]float32{{1}}
		}
		clear(request.Audio)
		if WriteFrame(os.Stdout, response) != nil {
			return
		}
	}
}

func TestDiscogsWorkerCancellationAndRestart(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	w := &DiscogsWorker{Executable: executable, ModelDir: "test:discogs-hang"}
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := w.Health(ctx); !errors.Is(err, context.DeadlineExceeded) || w.cmd != nil {
		t.Fatalf("canceled worker remained active: %v", err)
	}
	w.ModelDir = "test:discogs-invalid"
	if _, err := w.Classify(context.Background(), make([]float32, 3*discogsRate)); err == nil || w.cmd != nil {
		t.Fatalf("invalid native output accepted: %v", err)
	}
	w.ModelDir = "test:discogs-healthy"
	if err := w.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if scores, err := w.Classify(context.Background(), make([]float32, 3*discogsRate)); err != nil || len(scores) != 4 {
		t.Fatalf("worker failed to restart: %v", err)
	}
	_ = w.Close()
	if err := w.Health(context.Background()); err == nil || w.cmd != nil {
		t.Fatal("closed worker restarted")
	}
}
