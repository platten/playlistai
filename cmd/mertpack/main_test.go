package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/process"
)

func TestMERTPackRequiresExplicitManagedPath(t *testing.T) {
	for _, args := range [][]string{nil, {"remove"}, {"install-local", "--directory", t.TempDir()}, {"status", "--directory", "."}, {"status", "--directory", t.TempDir(), "--source", "anything"}} {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Fatal("invalid arguments accepted", args)
		}
	}
}
func TestMERTPackAbsentStatusAndSafeRemove(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mert-analysis")
	out := &bytes.Buffer{}
	if err := run([]string{"status", "--directory", dir}, out); err != nil || !strings.Contains(out.String(), `"installed":false`) {
		t.Fatal(out.String(), err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(marker, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"remove", "--directory", dir}, &bytes.Buffer{}); err == nil {
		t.Fatal("unmanaged directory removed")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("unrelated file removed")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "active.json"), []byte("old-model"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"remove", "--directory", dir}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("managed directory remained", err)
	}
}
func TestMERTPackCannotImportOwnSourceAsDestination(t *testing.T) {
	dir := t.TempDir()
	if err := run([]string{"install-local", "--directory", dir, "--source", filepath.Join(dir, "source")}, &bytes.Buffer{}); err == nil {
		t.Fatal("source inside managed output accepted")
	}
}

// Opt-in package probe sends the real framing protocol to the desktop binary,
// not to a separately built helper worker. No graphical session is launched.
func TestPackagedDesktopMERTHealth(t *testing.T) {
	exe := os.Getenv("PLAYLISTAI_MERT_MAIN_BINARY")
	bundle := os.Getenv("PLAYLISTAI_MERT_MAIN_BUNDLE")
	if exe == "" || bundle == "" {
		t.Skip("set native desktop executable and prepared pack to probe shipped worker")
	}
	manifest, err := audio.ReadMERTBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--mert-worker", bundle)
	process.Background(cmd)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	if err = audio.WriteFrame(input, audio.MERTWorkerRequest{Protocol: audio.MERTWorkerProtocol, Health: true}); err != nil {
		t.Fatal(err)
	}
	var response audio.MERTWorkerResponse
	if err = audio.ReadFrame(output, &response, 1<<20); err != nil {
		t.Fatal(err)
	}
	if response.Protocol != audio.MERTWorkerProtocol || response.Error != "" || response.Model != manifest.Model {
		t.Fatalf("desktop worker health mismatch: %+v", response)
	}
	if err = input.Close(); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveOwnedInterruptedInstallAndUpgrade(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(map[bool]string{false: "first-install", true: "upgrade"}[upgrade], func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "managed")
			if upgrade {
				old := filepath.Join(directory, "old-version")
				if err := os.MkdirAll(old, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(old, "mert-bundle.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(directory, "active.json"), []byte("old-version"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			manifest := audio.MERTBundleManifest{ID: "new-version"}
			if err := prepareAttempt(directory, manifest); err != nil {
				t.Fatal(err)
			}
			version := manifest.ID + "-" + audio.Fingerprint(manifest)[:16]
			partial := filepath.Join(directory, version, "mert-audio.onnx")
			if err := os.WriteFile(partial, []byte("interrupted-copy"), 0600); err != nil {
				t.Fatal(err)
			}
			// Simulate interruption before manager writes the manifest or activates.
			if _, err := os.Stat(filepath.Join(directory, version, "mert-bundle.json")); !os.IsNotExist(err) {
				t.Fatal("fixture unexpectedly has a manifest")
			}
			if err := run([]string{"remove", "--directory", directory}, &bytes.Buffer{}); err != nil {
				t.Fatal("owned partial install cannot be removed", err)
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatal("owned partial data remained", err)
			}
		})
	}
}
func TestAttemptCannotAdoptOrRemoveUnrelatedData(t *testing.T) {
	directory := t.TempDir()
	unrelated := filepath.Join(directory, "unrelated")
	if err := os.Mkdir(unrelated, 0700); err != nil {
		t.Fatal(err)
	}
	if err := prepareAttempt(directory, audio.MERTBundleManifest{ID: "new"}); err == nil {
		t.Fatal("unrelated directory adopted")
	}
	if err := run([]string{"remove", "--directory", directory}, &bytes.Buffer{}); err == nil {
		t.Fatal("unrelated directory removed")
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal(err)
	}
}
func TestSourceOutsideAcceptsDifferentWindowsVolumes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path semantics")
	}
	for _, paths := range [][2]string{{`C:\models\mert-analysis`, `D:\Downloads\mert`}, {`\\server\models\mert-analysis`, `\\server\sources\mert`}} {
		if err := sourceOutside(paths[0], paths[1]); err != nil {
			t.Fatal("distinct volumes rejected", err)
		}
	}
	if err := sourceOutside(`C:\models\mert-analysis`, `C:\models\mert-analysis\source`); err == nil {
		t.Fatal("nested source accepted")
	}
}
