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
	if !parity(a, b) {
		t.Fatal("identical embeddings rejected")
	}
	for _, value := range []float32{.01, float32(math.NaN()), float32(math.Inf(1))} {
		b[0] = value
		if parity(a, b) {
			t.Fatalf("invalid difference accepted: %v", value)
		}
	}
	if parity(a[:511], a) || parity(a, a[:511]) {
		t.Fatal("invalid shape accepted")
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
	if i.health(path) == nil {
		t.Fatal("missing health accepted")
	}
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if i.health(path) == nil {
		t.Fatal("malformed health accepted")
	}
}
