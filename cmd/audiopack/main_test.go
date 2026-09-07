package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/platten/playlistai/internal/audio"
)

func fixtureOptions(t *testing.T) options {
	t.Helper()
	dir := t.TempDir()
	o := options{export: dir, source: dir, worker: filepath.Join(dir, "audioworker"), runtime: filepath.Join(dir, "runtime.so"), licenses: filepath.Join(dir, "licenses.txt"), platform: runtime.GOOS + "/" + runtime.GOARCH, baseURL: "https://unpublished.invalid", output: filepath.Join(dir, "bundle"), memory: 2000000000}
	for _, name := range []string{"audioworker", "runtime.so", "licenses.txt", "audio.onnx", "text.onnx", "vocab.json", "merges.txt", "health.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("synthetic packaging fixture"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	parity := map[string]any{"model": "laion/larger_clap_music", "preprocessing": audio.PreprocessingVersion, "passed": true, "referenceRevision": "fixture", "fixtures": 10, "maximumAbsoluteError": 0, "minimumCosine": 1, "tokenizerCases": 7, "tokenizerExact": true, "preprocessingCases": 3, "preprocessingWithinTolerance": true}
	preprocessing := map[string]any{"version": audio.PreprocessingVersion, "samplingRate": 48000, "segmentSamples": 480000, "fftSize": 1024, "hopSize": 480, "melBins": 64, "minimumFrequency": 50, "maximumFrequency": 14000, "melScale": "slaney", "padding": "reflect", "floor": 1e-10}
	for name, value := range map[string]any{"parity.json": parity, "preprocessing.json": preprocessing} {
		raw, _ := json.Marshal(value)
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return o
}

func TestAssemblyIntegritySameDirectoryAndCalibrationGate(t *testing.T) {
	o := fixtureOptions(t)
	if err := assemble(o); err != nil {
		t.Fatal(err)
	}
	m, err := audio.ReadRuntimeBundle(o.output)
	if err != nil || len(m.Artifacts) != 9 {
		t.Fatalf("invalid packaged artifacts: %+v %v", m, err)
	}
	if m.Validate() == nil {
		t.Fatal("packaging fabricated a calibrated policy")
	}
	o.worker = filepath.Join(o.output, "audioworker")
	if err := assemble(o); err != nil {
		t.Fatal(err)
	}
	if _, err := audio.ReadRuntimeBundle(o.output); err != nil {
		t.Fatal("same-directory packaging truncated an artifact:", err)
	}
}

func TestAssemblyRejectsInvalidInputs(t *testing.T) {
	for _, mutate := range []func(*options){
		func(o *options) { o.baseURL = "http://example.invalid" },
		func(o *options) { o.memory = 0 },
		func(o *options) { o.licenses = o.worker },
		func(o *options) { o.worker = "" },
	} {
		o := fixtureOptions(t)
		mutate(&o)
		if assemble(o) == nil {
			t.Fatal("invalid package accepted")
		}
	}
}
