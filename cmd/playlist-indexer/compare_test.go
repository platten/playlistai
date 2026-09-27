package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCompareRejectsInvalidInputBeforeStartingRuntime(t *testing.T) {
	for _, tc := range []struct{ name, input string }{
		{"changed schema", `{"prompt":"soft piano","criteria":["soft piano"],"verified":true}`},
		{"trailing", `{"prompt":"soft piano","criteria":["soft piano"]}{}`},
		{"missing provenance", `{"prompt":"soft piano","criteria":["soft piano"],"clap_cosine":{"soft piano":0.4}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "input.json")
			out := filepath.Join(dir, "report.json")
			if err := os.WriteFile(input, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			code, err := execute(context.Background(), []string{"compare", "--input", input, "--out", out, "--model", "missing-model.gguf"}, io.Discard, io.Discard)
			if code != 1 || err == nil {
				t.Fatal("invalid input accepted")
			}
			if _, err = os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("invalid input wrote report")
			}
		})
	}
}

func TestComparePreservesExistingFile(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "evidence.json")
	out := filepath.Join(dir, "report.json")
	if err := os.WriteFile(input, []byte(`{"prompt":"soft piano","criteria":["soft piano"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := execute(context.Background(), []string{"compare", "--input", input, "--out", out, "--model", "missing.gguf"}, io.Discard, io.Discard)
	if err == nil || err.Error() != "comparison output already exists" {
		t.Fatalf("%v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil || string(b) != "original" {
		t.Fatal("existing file changed")
	}
}
