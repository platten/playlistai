package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/platten/playlistai/internal/audio"
)

func TestAssemblyCommandUsesFlags(t *testing.T) {
	o := fixtureOptions(t)
	old := os.Args
	t.Cleanup(func() { os.Args = old })
	os.Args = []string{"audiopack", "-export", o.export, "-source", o.source, "-worker", o.worker, "-runtime", o.runtime, "-licenses", o.licenses, "-platform", o.platform, "-artifact-base-url", o.baseURL, "-output", o.output, "-memory-bytes", strconv.FormatInt(o.memory, 10)}
	main()
	if _, err := audio.ReadRuntimeBundle(o.output); err != nil {
		t.Fatal(err)
	}
}

func TestArtifactCopyErrorsAndIdentity(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if copyArtifact(filepath.Join(dir, "missing"), source) == nil || copyArtifact(dir, source) == nil {
		t.Fatal("invalid source accepted")
	}
	if copyArtifact(source, filepath.Join(source, "child")) == nil {
		t.Fatal("invalid destination accepted")
	}
	if err := copyArtifact(source, source); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(source)
	if err != nil || string(raw) != "preserve" {
		t.Fatal("same-file copy altered source")
	}
	var value any
	if readJSON(filepath.Join(dir, "missing"), &value) == nil || readJSON(source, &value) == nil {
		t.Fatal("invalid JSON accepted")
	}
}

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

func TestBuiltinWorkerBundleWithoutCalibration(t *testing.T) {
	o := fixtureOptions(t)
	o.builtin, o.worker = true, ""
	o.audioOutput, o.textOutput = "audio_embeds", "text_embeds"
	o.textUnpadded = true
	if err := assemble(o); err != nil {
		t.Fatal(err)
	}
	m, err := audio.ReadRuntimeBundle(o.output)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != 2 || m.File(o.output, "worker") != "" || m.Policy.Valid() || !m.TextUnpadded || m.Model.Weights != m.EmbeddingFingerprint() {
		t.Fatalf("invalid custom runtime bundle: %+v", m)
	}
}
