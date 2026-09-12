//go:build cgo

package audioruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMERTRejectsMalformedHealthBeforeInference(t *testing.T) {
	if RunMERT(t.TempDir()) == nil {
		t.Fatal("missing bundle accepted")
	}
	if _, err := mertEmbedding(nil, nil); err == nil {
		t.Fatal("empty inference accepted")
	}
	path := filepath.Join(t.TempDir(), "health.json")
	for _, raw := range []string{"{", `{"fixtures":[]}`, `{"fixtures":[{"samples":0},{"samples":0},{"samples":0}]}`} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if mertHealth(nil, path) == nil {
			t.Fatal("malformed health accepted")
		}
	}
}
