package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/intent/nlu"
)

func TestCLIRejectsIncompleteOrUnknownCommandsWithoutAcquisition(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"setup"}, {"setup", "--root", ".", "extra"}, {"verify"}, {"verify", "--kind", "other"}, {"verify", "--kind", "minilm", "--model-dir", "x", "--runtime", "y", "--reference-input", "z", "--output", "file.exe"}} {
		var out bytes.Buffer
		if err := run(context.Background(), args, &out, &out); err == nil {
			t.Fatalf("accepted arguments %v", args)
		}
	}
}

func TestReferenceValidationRequiresCompleteTokensAndByteOffsets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reference.json")
	reference := parityReport{Version: 1, Kind: nlu.DistilBERT, OffsetUnit: "utf8-bytes", MaxTokens: 512, Cases: []parityCase{{Text: "", Encoding: nlu.Encoding{IDs: []int64{101, 102}, AttentionMask: []int64{1, 1}, TypeIDs: []int64{0, 0}, Tokens: []nlu.Token{{ID: 101, Special: true}, {ID: 102, Special: true}}}}}}
	write := func() {
		raw, err := json.Marshal(reference)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if _, _, err := readReference(path, nlu.DistilBERT); err != nil {
		t.Fatal(err)
	}
	reference.OffsetUnit = "characters"
	write()
	if _, _, err := readReference(path, nlu.DistilBERT); err == nil {
		t.Fatal("character offsets treated as bytes")
	}
	if _, _, err := readReference(path, nlu.ModelKind("retired-model")); err == nil {
		t.Fatal("different checkpoint contract accepted")
	}
}

func TestVerifierCannotOverwriteReferenceOrModel(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "reference.json")
	for _, output := range []string{input, filepath.Join(dir, "config.json"), filepath.Join(dir, "nlu-head.json")} {
		var out bytes.Buffer
		err := run(context.Background(), []string{"verify", "--kind", "distilbert", "--model-dir", dir, "--runtime", filepath.Join(dir, "runtime.dll"), "--reference-input", input, "--output", output}, &out, &out)
		if err == nil {
			t.Fatal("overwrite of input accepted")
		}
	}
}
