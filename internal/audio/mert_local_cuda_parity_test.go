package audio

import (
	"context"
	"math"
	"os"
	"testing"
)

func TestMERTLocalCUDAParityOptIn(t *testing.T) {
	bundle := os.Getenv("PLAYLISTAI_TEST_MERT_CUDA_BUNDLE")
	executable := os.Getenv("PLAYLISTAI_TEST_MERT_WORKER_EXECUTABLE")
	if bundle == "" || executable == "" {
		t.Skip("set PLAYLISTAI_TEST_MERT_CUDA_BUNDLE and PLAYLISTAI_TEST_MERT_WORKER_EXECUTABLE for real CUDA parity")
	}
	manifest, err := ReadMERTBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Backend() != "cuda" {
		t.Fatalf("bundle backend=%q", manifest.Backend())
	}
	worker := &MERTWorker{Executable: executable, BundleDir: bundle, Model: manifest.Model, Device: "cuda:0", InferenceThreads: 1}
	defer worker.Close()
	if err := worker.Health(context.Background()); err != nil {
		t.Fatal(err)
	}

	var oldSums, currentSums [MERTDimension]float64
	minimumCosine := 1.0
	maximumCoordinateDifference := 0.0
	for caseIndex, fixture := range []struct {
		rate, channels int
	}{
		{8000, 1}, {22050, 2}, {44100, 8}, {48000, 2}, {96000, 1}, {192000, 8},
	} {
		pcm := DecodedPCM{SampleRate: fixture.rate, Channels: fixture.channels, Samples: make([]float32, 5*fixture.rate*fixture.channels)}
		for frame := 0; frame < len(pcm.Samples)/fixture.channels; frame++ {
			for channel := 0; channel < fixture.channels; channel++ {
				value := 0.28*math.Sin(2*math.Pi*float64(311+caseIndex*73)*float64(frame)/float64(fixture.rate)) +
					0.07*math.Sin(2*math.Pi*float64(1703+channel*31)*float64(frame)/float64(fixture.rate)+float64(channel)*0.11)
				pcm.Samples[frame*fixture.channels+channel] = float32(value)
			}
		}
		legacy, err := mertResample(context.Background(), pcm, mertLocalMaximumRate)
		if err != nil {
			t.Fatal(err)
		}
		current, err := MERTResampleLocal(context.Background(), pcm)
		clear(pcm.Samples)
		if err != nil {
			clear(legacy)
			t.Fatal(err)
		}
		legacyVector, err := worker.EmbedAudio(context.Background(), legacy)
		if err != nil {
			clear(legacy)
			clear(current)
			t.Fatal(err)
		}
		currentVector, err := worker.EmbedAudio(context.Background(), current)
		clear(legacy)
		clear(current)
		if err != nil {
			clear(legacyVector)
			t.Fatal(err)
		}
		maximum, cosine, valid := MERTParityMetrics(legacyVector, currentVector)
		if !valid || maximum > 0.003 || cosine < 0.9999 {
			t.Fatalf("case %d (%d Hz/%d ch) maximum=%g cosine=%g valid=%t", caseIndex, fixture.rate, fixture.channels, maximum, cosine, valid)
		}
		maximumCoordinateDifference = max(maximumCoordinateDifference, maximum)
		minimumCosine = min(minimumCosine, cosine)
		for index := range legacyVector {
			oldSums[index] += float64(legacyVector[index])
			currentSums[index] += float64(currentVector[index])
		}
		clear(legacyVector)
		clear(currentVector)
	}
	legacyPooled := normalizeMERTParitySums(oldSums[:])
	currentPooled := normalizeMERTParitySums(currentSums[:])
	defer clear(legacyPooled)
	defer clear(currentPooled)
	maximum, cosine, valid := MERTParityMetrics(legacyPooled, currentPooled)
	if !valid || maximum > 0.003 || cosine < 0.9999 {
		t.Fatalf("pooled maximum=%g cosine=%g valid=%t", maximum, cosine, valid)
	}
	t.Logf("six-window minimum cosine=%g maximum coordinate difference=%g; pooled cosine=%g maximum coordinate difference=%g", minimumCosine, maximumCoordinateDifference, cosine, maximum)
}

func normalizeMERTParitySums(sums []float64) []float32 {
	var squared float64
	for _, value := range sums {
		squared += value * value
	}
	norm := math.Sqrt(squared)
	result := make([]float32, len(sums))
	for index, value := range sums {
		result[index] = float32(value / norm)
	}
	return result
}
