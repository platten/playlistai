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

func TestReferenceValidationRequiresActualEmbeddingsAndByteOffsets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reference.json")
	reference := parityReport{Version: 1, Kind: nlu.MiniLM, OffsetUnit: "utf8-bytes", MaxTokens: 256, Cases: []parityCase{{Text: "", Encoding: nlu.Encoding{IDs: []int64{101, 102}, AttentionMask: []int64{1, 1}, TypeIDs: []int64{0, 0}, Tokens: []nlu.Token{{ID: 101, Special: true}, {ID: 102, Special: true}}}}}}
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
	if _, _, err := readReference(path, nlu.MiniLM); err == nil {
		t.Fatal("embedding parity accepted without original embeddings")
	}
	reference.Cases[0].Embedding = make([]float32, 384)
	reference.Cases[0].Embedding[0] = 1
	write()
	if _, _, err := readReference(path, nlu.MiniLM); err != nil {
		t.Fatal(err)
	}
	reference.OffsetUnit = "characters"
	write()
	if _, _, err := readReference(path, nlu.MiniLM); err == nil {
		t.Fatal("character offsets treated as bytes")
	}
	if _, _, err := readReference(path, nlu.DistilBERT); err == nil {
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

func TestEmbeddingComparisonRejectsUnnormalizedAndChangedOutputs(t *testing.T) {
	vector := make([]float32, 384)
	vector[0] = 1
	if _, _, ok := compareVectors(vector, vector); !ok {
		t.Fatal("identical reference rejected")
	}
	changed := append([]float32(nil), vector...)
	changed[0] = 2
	if _, _, ok := compareVectors(changed, vector); ok {
		t.Fatal("unnormalized vector accepted")
	}
	changed[0], changed[1] = 0, 1
	if _, _, ok := compareVectors(changed, vector); ok {
		t.Fatal("unrelated vector accepted")
	}
}
