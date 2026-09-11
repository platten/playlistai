package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("PLAYLIST_INTENTEVAL_PROCESS_FIXTURE"); mode != "" {
		if mode == "error" {
			os.Exit(2)
		}
		if mode == "device" {
			fmt.Println("CUDA0: Synthetic GPU (4096 MiB, 3900 MiB free)")
		}
		if mode == "version" {
			fmt.Println("fixture-runtime v1\nextra detail")
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestBenchmarkDeviceProbeContracts(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, fixture, selected, mode string
		wantErr                       bool
	}{{"single device", "device", "", "gpu", false}, {"selected device", "device", "CUDA0", "gpu", false}, {"missing selection", "device", "CUDA1", "", true}, {"no devices", "empty", "", "auto", false}, {"no selected device", "empty", "CUDA0", "", true}, {"probe error", "error", "", "auto", false}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PLAYLIST_INTENTEVAL_PROCESS_FIXTURE", tc.fixture)
			env, err := benchmarkEnvironment(context.Background(), executable, tc.selected, 1024, 2, 1)
			if (err != nil) != tc.wantErr {
				t.Fatalf("unexpected probe result: %+v %v", env, err)
			}
			if err == nil && env.ExecutionMode != tc.mode {
				t.Fatalf("wrong execution mode: %+v", env)
			}
			if tc.fixture == "device" && err == nil && (env.SelectedDevice != "CUDA0" || len(env.Devices) != 1) {
				t.Fatal("device identity lost")
			}
			if tc.fixture == "error" && env.ProbeError == "" {
				t.Fatal("probe failure concealed")
			}
		})
	}
	t.Setenv("PLAYLIST_INTENTEVAL_PROCESS_FIXTURE", "version")
	if got := runtimeVersion(executable); got != "fixture-runtime v1" {
		t.Fatalf("version: %q", got)
	}
}

func args(t *testing.T, values ...string) {
	t.Helper()
	old := os.Args
	os.Args = append([]string{"intenteval"}, values...)
	t.Cleanup(func() { os.Args = old })
}

func TestRulesEvaluationCLI(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "nested", "report.json")
	md := filepath.Join(dir, "nested", "report.md")
	args(t, "-dataset", "../../internal/evaluation/testdata/synthetic.json", "-backend", "rules", "-case", "negation", "-output", out, "-markdown", md)
	main()
	for _, path := range []string{out, md} {
		raw, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(raw), "negation") {
			t.Fatalf("missing evaluation output %s: %v", path, err)
		}
	}
}

func TestEvaluationCLIRejectsInvalidInput(t *testing.T) {
	dataset := "../../internal/evaluation/testdata/synthetic.json"
	for name, values := range map[string][]string{"missing dataset": {}, "unknown flag": {"-bogus"}, "missing file": {"-dataset", "absent"}, "unknown case": {"-dataset", dataset, "-case", "absent"}, "unknown backend": {"-dataset", dataset, "-backend", "other"}, "missing model": {"-dataset", dataset}, "incompatible device": {"-dataset", dataset, "-model", "missing.gguf", "-runtime", "missing", "-device", "GPU"}, "invalid gguf": {"-dataset", dataset, "-model", "missing.gguf", "-runtime", "missing"}} {
		t.Run(name, func(t *testing.T) {
			args(t, values...)
			if run() == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
}

func TestModelIdentityHelpers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model")
	raw := []byte("fixture")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := fileSHA256(path)
	if err != nil || got != fmt.Sprintf("%x", sha256.Sum256(raw)) {
		t.Fatalf("hash: %s %v", got, err)
	}
	if _, err := fileSHA256(dir); err == nil {
		t.Fatal("hashed directory")
	}
	if _, err := fileSHA256(path + "missing"); err == nil {
		t.Fatal("hashed absent file")
	}
	if got := runtimeVersion(path + "missing"); !strings.Contains(got, "version unavailable") {
		t.Fatal(got)
	}
	for _, gpu := range []int{-1, 0} {
		env, err := benchmarkEnvironment(context.Background(), path+"missing", "", 1024, 2, gpu)
		if err != nil || env.ContextSize != 1024 || env.Threads != 2 {
			t.Fatalf("environment: %+v %v", env, err)
		}
		if gpu == 0 && env.ProbeError == "" {
			t.Fatal("missing runtime concealed")
		}
	}
	if err := ensureParent("bare.json"); err != nil {
		t.Fatal(err)
	}
	if err := ensureParent(filepath.Join(path, "blocked", "report")); err == nil {
		t.Fatal("file accepted as parent")
	}
}
