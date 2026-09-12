//go:build !cgo

package audioruntime

import "testing"

func TestMERTUnavailableWithoutCGO(t *testing.T) {
	if RunMERT("ignored") == nil {
		t.Fatal("MERT native inference available without CGO")
	}
}
