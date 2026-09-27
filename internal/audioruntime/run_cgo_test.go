//go:build cgo

package audioruntime

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestParityRejectsInvalidAndChangedEmbeddings(t *testing.T) {
	a := make([]float32, 512)
	b := make([]float32, 512)
	if !parity(a, b, "cpu") {
		t.Fatal("identical embeddings rejected")
	}
	for _, value := range []float32{.01, float32(math.NaN()), float32(math.Inf(1))} {
		b[0] = value
		if parity(a, b, "cpu") {
			t.Fatalf("invalid difference accepted: %v", value)
		}
	}
	if parity(a[:511], a, "cpu") || parity(a, a[:511], "cpu") {
		t.Fatal("invalid shape accepted")
	}
}

func TestCUDAParityAllowsMeasuredDriftButRejectsChangedDirection(t *testing.T) {
	reference := make([]float32, 512)
	observed := make([]float32, 512)
	reference[0], observed[0] = 1, 1
	observed[1] = 0.00035
	if parity(reference, observed, "cpu") || !parity(reference, observed, "cuda") {
		t.Fatal("CPU and CUDA numerical gates are not distinct")
	}
	observed[1] = 0.0006
	if parity(reference, observed, "cuda") {
		t.Fatal("CUDA accepted an oversized coordinate error")
	}
	for i := 1; i < len(observed); i++ {
		observed[i] = 0.0004
	}
	if parity(reference, observed, "cuda") {
		t.Fatal("CUDA accepted a changed embedding direction")
	}
}

func TestRuntimeRejectsMissingBundleAndMalformedHealthBeforeInference(t *testing.T) {
	if Run(t.TempDir()) == nil {
		t.Fatal("missing bundle accepted")
	}
	i := &inference{}
	if _, err := i.audioEmbedding(nil); err == nil {
		t.Fatal("invalid audio shape accepted")
	}
	path := filepath.Join(t.TempDir(), "health.json")
	if i.health(path, "cpu") == nil {
		t.Fatal("missing health accepted")
	}
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if i.health(path, "cpu") == nil {
		t.Fatal("malformed health accepted")
	}
}
