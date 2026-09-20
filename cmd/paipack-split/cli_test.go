package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestExecuteSplitsPackAndWritesManifest(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "library.paipack")
	contents := []byte("0123456789abcdefghijklmnop")
	if err := os.WriteFile(input, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "parts")
	var stdout, stderr bytes.Buffer
	code, err := execute(context.Background(), []string{"--out", output, "--part-size", "10B", input}, &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("execute = code %d, err %v, stderr %q", code, err, stderr.String())
	}
	report := readTestManifest(t, output)
	if report.Format != "playlist-ai-paipack-parts" || report.Compression != "none" || report.PartSize != 10 {
		t.Fatalf("manifest header = %+v", report)
	}
	if len(report.Parts) != 3 || report.Parts[0].Size != 10 || report.Parts[1].Size != 10 || report.Parts[2].Size != 6 {
		t.Fatalf("parts = %+v", report.Parts)
	}
	if got := joinTestParts(t, output, report); !bytes.Equal(got, contents) {
		t.Fatalf("joined contents = %q, want %q", got, contents)
	}
	assertDigest(t, report.Source, contents)
	assertDigest(t, report.Payload, contents)
	if !strings.Contains(stdout.String(), "3 part(s)") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestExecuteCanWrapPartsInZstd(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "library.paipack")
	contents := bytes.Repeat([]byte("compressible paipack bytes\n"), 100)
	if err := os.WriteFile(input, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "parts")
	code, err := execute(context.Background(), []string{"--out", output, "--part-size", "31B", "--compression", "zstd", input}, io.Discard, io.Discard)
	if err != nil || code != 0 {
		t.Fatalf("execute = code %d, err %v", code, err)
	}
	report := readTestManifest(t, output)
	compressed := joinTestParts(t, output, report)
	assertDigest(t, report.Payload, compressed)
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	decoded, err := decoder.DecodeAll(compressed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, contents) {
		t.Fatal("decoded payload does not match input")
	}
}

func TestExecuteRejectsInvalidArgumentsAndExistingOutput(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "library.paipack")
	if err := os.WriteFile(input, []byte("pack"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "parts")
	if err := os.Mkdir(output, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{},
		{"--part-size", "0", input},
		{"--compression", "gzip", input},
		{"--out", output, input},
		{filepath.Join(root, "not-a-pack.zip")},
	} {
		code, err := execute(context.Background(), args, io.Discard, io.Discard)
		if err == nil || code == 0 {
			t.Fatalf("execute(%v) = code %d, err %v", args, code, err)
		}
	}
	empty := filepath.Join(root, "empty.paipack")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, err := execute(context.Background(), []string{empty}, io.Discard, io.Discard); err == nil || code == 0 {
		t.Fatalf("empty input = code %d, err %v", code, err)
	}
}

func TestSplitPackCancellationLeavesNoPublishedOutput(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "library.paipack")
	if err := os.WriteFile(input, bytes.Repeat([]byte("x"), 1024), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	output := filepath.Join(root, "parts")
	if _, err := splitPack(ctx, input, output, 16, "none"); err == nil {
		t.Fatal("splitPack succeeded after cancellation")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("published output exists after cancellation: %v", err)
	}
}

func readTestManifest(t *testing.T, directory string) manifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(directory, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	var report manifest
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func joinTestParts(t *testing.T, directory string, report manifest) []byte {
	t.Helper()
	var joined bytes.Buffer
	for index, entry := range report.Parts {
		if entry.Index != index {
			t.Fatalf("part index = %d, want %d", entry.Index, index)
		}
		contents, err := os.ReadFile(filepath.Join(directory, entry.Name))
		if err != nil {
			t.Fatal(err)
		}
		assertDigest(t, fileDigest{Name: entry.Name, Size: entry.Size, SHA256: entry.SHA256}, contents)
		joined.Write(contents)
	}
	return joined.Bytes()
}

func assertDigest(t *testing.T, digest fileDigest, contents []byte) {
	t.Helper()
	sum := sha256.Sum256(contents)
	if digest.Size != int64(len(contents)) || digest.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest = %+v for %d bytes", digest, len(contents))
	}
}
