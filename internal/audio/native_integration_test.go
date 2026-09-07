package audio

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Optional real-model test: large artifacts are never downloaded by go test.
// Run with PLAYLISTAI_TEST_AUDIO_BUNDLE pointing to an exported native bundle.
func TestNativeInferenceWithoutPython(t *testing.T) {
	dir := os.Getenv("PLAYLISTAI_TEST_AUDIO_BUNDLE")
	if dir == "" {
		t.Skip("set PLAYLISTAI_TEST_AUDIO_BUNDLE for real native inference")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadRuntimeBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	// No executables are discoverable, and a Python interpreter cannot use its
	// usual environment. Native runtime paths come exclusively from the bundle.
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	t.Setenv("PYTHONHOME", empty)
	t.Setenv("PYTHONPATH", empty)
	t.Setenv("LD_LIBRARY_PATH", empty)
	w := &Worker{Executable: manifest.File(dir, "worker"), BundleDir: dir, Model: manifest.Model}
	defer w.Close()
	if err := w.Health(context.Background()); err != nil {
		t.Fatal("native audio/text health requires an external runtime:", err)
	}
	if runtime.GOOS == "linux" {
		maps, err := os.ReadFile(fmt.Sprintf("/proc/%d/maps", w.cmd.Process.Pid))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(string(maps)), "libpython") {
			t.Fatal("worker loaded a Python runtime")
		}
	}
}
