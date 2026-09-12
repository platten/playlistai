package audio

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"testing"
	"time"
)

func runMERTTestWorker(mode string) {
	for {
		var request MERTWorkerRequest
		if ReadFrame(os.Stdin, &request, 1<<20) != nil {
			return
		}
		if mode == "test:mert-hang" {
			time.Sleep(time.Minute)
			return
		}
		if mode == "test:mert-crash" {
			return
		}
		response := MERTWorkerResponse{Protocol: MERTWorkerProtocol, Model: mertTestModel(), Vector: make([]float32, MERTDimension)}
		response.Vector[0] = 1
		if mode == "test:mert-invalid" {
			response.Vector[0] = float32(math.NaN())
		}
		if mode == "test:mert-wrong-model" {
			response.Model.Pooling = "incompatible"
		}
		clear(request.Audio)
		if WriteFrame(os.Stdout, response) != nil {
			return
		}
	}
}
func TestMERTWorkerKillsReapsAndRestarts(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	w := &MERTWorker{Executable: exe, BundleDir: "test:mert-hang", Model: mertTestModel()}
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err = w.EmbedAudio(ctx, make([]float32, 400)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if w.cmd != nil {
		t.Fatal("canceled native child not reaped")
	}
	for _, mode := range []string{"test:mert-crash", "test:mert-invalid", "test:mert-wrong-model"} {
		w.BundleDir = mode
		if _, err = w.EmbedAudio(context.Background(), make([]float32, 400)); err == nil || w.cmd != nil {
			t.Fatal("bad worker remained active", mode, err)
		}
	}
	w.BundleDir = "test:mert-healthy"
	if err = w.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.Unload()
	if w.cmd != nil {
		t.Fatal("unload failed")
	}
	if _, err = w.EmbedAudio(context.Background(), make([]float32, 400)); err != nil {
		t.Fatal("restart failed", err)
	}
	_ = w.Close()
	if err = w.Health(context.Background()); err == nil || w.cmd != nil {
		t.Fatal("closed worker restarted")
	}
}
func TestMERTPreparedPreprocessingReference(t *testing.T) {
	path := os.Getenv("PLAYLISTAI_MERT_PREPROCESSING_REFERENCE")
	if path == "" {
		t.Skip("opt-in reference file from pinned offline exporter")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Fixtures []struct {
			Name       string    `json:"name"`
			SampleRate int       `json:"sampleRate"`
			Channels   int       `json:"channels"`
			PCM        []float32 `json:"pcm"`
			Resampled  []float32 `json:"resampled"`
			Normalized []float32 `json:"normalized"`
		} `json:"fixtures"`
	}
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Fixtures) < 5 {
		t.Fatal("insufficient preprocessing cases")
	}
	for _, f := range document.Fixtures {
		t.Run(f.Name, func(t *testing.T) {
			resampled, err := MERTResample(context.Background(), DecodedPCM{Samples: f.PCM, SampleRate: f.SampleRate, Channels: f.Channels})
			if err != nil {
				t.Fatal(err)
			}
			defer clear(resampled)
			if len(resampled) != len(f.Resampled) {
				t.Fatal("resampling length")
			}
			for j, x := range resampled {
				if math.Abs(float64(x-f.Resampled[j])) > 1e-6 {
					t.Fatalf("resampling mismatch sample%d", j)
				}
			}
			normalized, mask, err := MERTInput(resampled)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(normalized)
			defer clear(mask)
			if len(f.Normalized) != len(resampled) {
				t.Fatal("reference normalization length")
			}
			for j, x := range f.Normalized {
				if math.Abs(float64(x-normalized[j])) > 1e-6+1e-6*math.Abs(float64(x)) {
					t.Fatalf("normalization mismatch sample%d got%g want%g", j, normalized[j], x)
				}
			}
		})
	}
}
