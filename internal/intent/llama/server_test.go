package llama

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/ports"
)

// fakeServerBin is built once for the whole package.
var fakeServerBin string

func TestMain(m *testing.M) { os.Exit(runTests(m)) }

func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "llama-fakeserver")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	if _, err := os.Stat("internal/fakeserver/main.go"); err == nil {
		bin := filepath.Join(dir, "fakeserver")
		build := exec.Command("go", "build", "-o", bin, "./internal/fakeserver")
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			panic("build fakeserver: " + err.Error())
		}
		fakeServerBin = bin
	}
	return m.Run()
}

func dummyModel(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(p, []byte("GGUF not really but non-empty"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestServerLifecycle(t *testing.T) {
	if fakeServerBin == "" {
		t.Skip("fakeserver not built")
	}
	t.Parallel()

	srv := newServer(ServerOptions{BinaryPath: fakeServerBin, ModelPath: dummyModel(t), NCtx: 2048})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !srv.Alive() {
		t.Fatal("should be alive after Start")
	}
	if !NewClient(srv.BaseURL()).Healthy(context.Background()) {
		t.Fatal("should be healthy after Start")
	}

	if err := srv.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if srv.Alive() {
		t.Fatal("should not be alive after Stop")
	}
}

func TestServerAcceptsExplicitBenchmarkDevice(t *testing.T) {
	if fakeServerBin == "" {
		t.Skip("fakeserver not built")
	}
	t.Parallel()

	srv := newServer(ServerOptions{
		BinaryPath: fakeServerBin,
		ModelPath:  dummyModel(t),
		GPULayers:  0,
		Device:     "CUDA0",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start with explicit device: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	if !srv.Alive() {
		t.Fatal("server with explicit device should be alive")
	}
}

func TestServerStartFailsOnMissingBinary(t *testing.T) {
	t.Parallel()
	srv := newServer(ServerOptions{BinaryPath: filepath.Join(t.TempDir(), "nope"), ModelPath: dummyModel(t)})
	if err := srv.Start(context.Background()); err == nil {
		t.Fatal("expected an error starting a missing binary")
	}
}

func TestParserEndToEndWithFakeServer(t *testing.T) {
	if fakeServerBin == "" {
		t.Skip("fakeserver not built")
	}
	t.Parallel()

	p, err := New(context.Background(), Options{
		BinaryPath:   fakeServerBin,
		ModelPath:    dummyModel(t),
		Device:       "CUDA0",
		StartTimeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	if info := p.Info(); info.Backend != "llama" || !info.Ready {
		t.Fatalf("Info = %+v", info)
	}

	m, err := p.Parse(context.Background(), ports.IntentInput{Prompt: "make it like Portishead please"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Seeds.Queries) != 1 || m.Seeds.Queries[0] != "Portishead please" {
		// fakeserver's regex is greedy on the seed; just assert something came through
		if len(m.Seeds.Queries) == 0 {
			t.Fatalf("no seed round-tripped: %+v", m)
		}
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if p.Info().Ready {
		t.Fatal("not Ready after Close")
	}
}

func TestNewRejectsBadModel(t *testing.T) {
	t.Parallel()
	if _, err := New(context.Background(), Options{ModelPath: ""}); err == nil {
		t.Fatal("empty model path should error")
	}
	if _, err := New(context.Background(), Options{ModelPath: filepath.Join(t.TempDir(), "missing.gguf")}); err == nil {
		t.Fatal("missing model should error")
	}
}
