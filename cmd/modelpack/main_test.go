package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackModeCreatesUploadManifest(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "model.onnx"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "upload")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--pack-source", source, "--pack-output", output, "--name", "fixture", "--part-bytes", "1024"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Created") {
		t.Fatalf("missing result: %s", &stdout)
	}
	if _, err := os.Stat(filepath.Join(output, "manifest.json")); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsMixedPackAndUnpackModes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"--pack-source", "source", "--pack-output", "output", "--name", "fixture", "--manifest", "manifest"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("mixed mode accepted")
	}
}

func TestRecommendedModeRequiresCompleteExclusiveArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	for _, args := range [][]string{
		{"--recommended", "unknown", "--cache", "cache", "--out", "out"},
		{"--recommended", "mert", "--manifest", "also-set", "--cache", "cache", "--out", "out"},
		{"--recommended", "clap", "--cache", "cache"},
	} {
		if err := run(args, &stdout, &stderr); err == nil {
			t.Fatalf("invalid recommended arguments accepted: %v", args)
		}
	}
}
