//go:build !cgo

package audioruntime

import (
	"strings"
	"testing"
)

func TestNonNativeBuildReportsRequiredRuntime(t *testing.T) {
	if err := Run(t.TempDir()); err == nil || !strings.Contains(err.Error(), "native ONNX Runtime") {
		t.Fatalf("missing native runtime concealed: %v", err)
	}
}
