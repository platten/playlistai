package localaudio

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func integrityTestRuntime(t *testing.T, body string) (*Runtime, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX test child")
	}
	dir := t.TempDir()
	executable := filepath.Join(dir, "ffmpeg-fixture")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "source.flac")
	if err := os.WriteFile(path, []byte("stable fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &Runtime{
		ffmpeg:   executable,
		manifest: Manifest{ID: "integrity-test-runtime"},
		limits:   Limits{IntegrityTimeout: time.Second, IntegrityStall: 500 * time.Millisecond, MaxStderrBytes: 4096},
	}, path
}

func TestValidateIntegrityStopsSilentDecoder(t *testing.T) {
	r, path := integrityTestRuntime(t, "sleep 1\nexit 0")
	r.limits.IntegrityStall = 40 * time.Millisecond
	err := r.ValidateIntegrity(context.Background(), integrityTestProbe(t, r, path))
	if !errors.Is(err, ErrProcessStalled) || errors.Is(err, ErrCorrupt) {
		t.Fatalf("validation error = %v", err)
	}
}

func integrityTestProbe(t *testing.T, r *Runtime, path string) ProbeResult {
	t.Helper()
	revision, err := sourceRevision(path)
	if err != nil {
		t.Fatal(err)
	}
	return ProbeResult{Path: path, Revision: revision, ProbeRuntimeID: r.ID(), SelectedStream: AudioStream{Index: 0, Codec: "flac"}}
}

func TestValidateIntegrityClassifiesDecoderFailureAsCorrupt(t *testing.T) {
	r, path := integrityTestRuntime(t, "echo damaged >&2\nexit 9")
	err := r.ValidateIntegrity(context.Background(), integrityTestProbe(t, r, path))
	if !errors.Is(err, ErrCorrupt) || errors.Is(err, ErrSourceChanged) {
		t.Fatalf("validation error = %v", err)
	}
}

func TestValidateIntegrityChecksSizeAgainAfterDecode(t *testing.T) {
	r, path := integrityTestRuntime(t, "sleep 0.2\nexit 0")
	probe := integrityTestProbe(t, r, path)
	changed := make(chan error, 1)
	go func() {
		time.Sleep(40 * time.Millisecond)
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err == nil {
			_, err = file.Write([]byte(" changed"))
			err = errors.Join(err, file.Close())
		}
		changed <- err
	}()
	err := r.ValidateIntegrity(context.Background(), probe)
	if changeErr := <-changed; changeErr != nil {
		t.Fatal(changeErr)
	}
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("validation error = %v", err)
	}
}

func TestValidateIntegrityUsesCodecNotExtension(t *testing.T) {
	if RequiresIntegrityValidation(ProbeResult{SelectedStream: AudioStream{Codec: "aac"}}) {
		t.Fatal("AAC unexpectedly selected for FLAC/MP3 full validation")
	}
	if !RequiresIntegrityValidation(ProbeResult{SelectedStream: AudioStream{Codec: "mp3"}}) {
		t.Fatal("MP3 did not select full validation")
	}
}
