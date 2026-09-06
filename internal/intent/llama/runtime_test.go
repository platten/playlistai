package llama

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/ports"
)

func touchExec(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestDetectRuntimeExplicit(t *testing.T) {
	dir := t.TempDir()

	server := filepath.Join(dir, exeName("llama-server"))
	touchExec(t, server)
	if rt := DetectRuntime(server); !rt.Available || rt.Kind != KindServer || rt.subcmd() != nil {
		t.Fatalf("llama-server: %+v subcmd=%v", rt, rt.subcmd())
	}

	unified := filepath.Join(dir, exeName("llama"))
	touchExec(t, unified)
	rt := DetectRuntime(unified)
	if !rt.Available || rt.Kind != KindLlama || rt.Source != "config" {
		t.Fatalf("llama: %+v", rt)
	}
	if got := rt.subcmd(); len(got) != 1 || got[0] != "serve" {
		t.Fatalf("unified subcmd = %v, want [serve]", got)
	}

	if rt := DetectRuntime(filepath.Join(dir, "does-not-exist")); rt.Available {
		t.Fatal("a bad explicit path must report unavailable")
	}
}

func TestDetectRuntimeFromInstallDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses HOME/.local/bin")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	touchExec(t, filepath.Join(home, ".local", "bin", "llama"))
	rt := DetectRuntime("")
	// installDirs() is checked before PATH, so this wins regardless of the
	// host having a llama on PATH.
	if !rt.Available || rt.Kind != KindLlama || rt.Source != "user-bin" {
		t.Fatalf("install-dir detect: %+v", rt)
	}
}

func TestStagedRuntimes(t *testing.T) {
	dir := t.TempDir()
	if got := StagedRuntimes(dir); got != nil {
		t.Fatalf("empty dir: %v", got)
	}
	touchExec(t, filepath.Join(dir, stagedName("primary")))
	touchExec(t, filepath.Join(dir, stagedName("cpu")))
	got := StagedRuntimes(dir)
	if len(got) != 2 || got[0].Label != "gpu" || got[1].Label != "cpu" {
		t.Fatalf("staged: %+v", got)
	}
	for _, r := range got {
		if r.Kind != KindLlama || len(r.subcmd()) != 1 || r.subcmd()[0] != "serve" {
			t.Fatalf("runtime kind/subcmd wrong: %+v", r)
		}
	}
}

func TestParseDeviceList(t *testing.T) {
	t.Parallel()
	got := ParseDeviceList(`load_backend: loaded CUDA backend
Available devices:
  CUDA0: NVIDIA GeForce RTX 5060 Laptop GPU (4096 MiB, 3832 MiB free)
  Vulkan1: AMD Radeon Graphics (8192 MiB, 7100 MiB free)
  CUDA1: NVIDIA GeForce RTX 5090 Laptop GPU (24576 MiB, 23800 MiB free)
  CUDA2: NVIDIA GeForce RTX 5090 (32768 MiB, 31900 MiB free)
  CUDA3: NVIDIA GeForce RTX 5070 Laptop GPU (8192 MiB, 7600 MiB free)
  CUDA4: NVIDIA GeForce RTX 5070 (12288 MiB, 11600 MiB free)
  CUDA5: NVIDIA GeForce RTX 3090 (24576 MiB, 23000 MiB free)
`)
	if len(got) != 7 {
		t.Fatalf("devices = %+v", got)
	}
	if got[0].ID != "CUDA0" || got[0].Name != "NVIDIA GeForce RTX 5060 Laptop GPU" || got[0].TotalBytes != 4096<<20 || got[0].FreeBytes != 3832<<20 {
		t.Fatalf("CUDA device = %+v", got[0])
	}
	if got[1].ID != "Vulkan1" || got[1].TotalBytes != 8192<<20 {
		t.Fatalf("Vulkan device = %+v", got[1])
	}
	if got[2].Name != "NVIDIA GeForce RTX 5090 Laptop GPU" || got[2].TotalBytes != 24576<<20 || got[2].FreeBytes != 23800<<20 {
		t.Fatalf("RTX 5090 Laptop device = %+v", got[2])
	}
	if got[3].Name != "NVIDIA GeForce RTX 5090" || got[3].TotalBytes != 32768<<20 || got[3].FreeBytes != 31900<<20 {
		t.Fatalf("RTX 5090 desktop device = %+v", got[3])
	}
	if got[4].Name != "NVIDIA GeForce RTX 5070 Laptop GPU" || got[4].TotalBytes != 8192<<20 {
		t.Fatalf("RTX 5070 Laptop device = %+v", got[4])
	}
	if got[5].Name != "NVIDIA GeForce RTX 5070" || got[5].TotalBytes != 12288<<20 {
		t.Fatalf("RTX 5070 desktop device = %+v", got[5])
	}
	if got[6].Name != "NVIDIA GeForce RTX 3090" || got[6].TotalBytes != 24576<<20 {
		t.Fatalf("RTX 3090 device = %+v", got[6])
	}
	if got := ParseDeviceList("Available devices:\n  (none)\n"); len(got) != 0 {
		t.Fatalf("no-device output = %+v", got)
	}
}

func TestNewFallsBackToNextRuntime(t *testing.T) {
	if fakeServerBin == "" {
		t.Skip("fakeserver not built")
	}
	t.Parallel()

	broken := filepath.Join(t.TempDir(), "not-a-binary")
	if err := os.WriteFile(broken, []byte("nope"), 0o755); err != nil {
		t.Fatal(err)
	}

	p, err := New(context.Background(), Options{
		Runtimes: []Runtime{
			{Path: broken, Kind: KindServer, Label: "gpu"},
			{Path: fakeServerBin, Kind: KindServer, Label: "cpu"},
		},
		ModelPath:    dummyModel(t),
		StartTimeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatalf("New should have fallen back to the fakeserver: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	if _, err := p.Parse(context.Background(), ports.IntentInput{Prompt: "like Air"}); err != nil {
		t.Fatalf("Parse via fallback runtime: %v", err)
	}
}
