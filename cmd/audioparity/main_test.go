package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/audio"
)

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestParityReportSyntheticSignalsAndTokenizer(t *testing.T) {
	dir := t.TempDir()
	vocab := map[string]int64{"<s>": 0, "</s>": 1, "<pad>": 2, "<unk>": 3}
	// The no-merge fixture has every byte symbol, without external assets.
	for r := rune(33); r < 512; r++ {
		vocab[string(r)] = int64(r) + 4
	}
	raw, err := json.Marshal(vocab)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vocab.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "merges.txt"), []byte("# fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(dir, &out); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Preprocessing string
		Tokens        []struct {
			Text string
			IDs  []int64
		}
		Mels []struct {
			ToneHz   float64
			Features []float32
		}
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Preprocessing != audio.PreprocessingVersion || len(report.Tokens) != 7 || len(report.Mels) != 3 {
		t.Fatal("incomplete parity report")
	}
	for _, entry := range report.Tokens {
		if len(entry.IDs) != 77 {
			t.Fatal("incorrect token shape")
		}
	}
	for _, entry := range report.Mels {
		if len(entry.Features) != audio.MelFrames*audio.MelBins {
			t.Fatal("incorrect mel shape")
		}
	}
	if err := run(dir, brokenWriter{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("output error lost: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vocab.json"), []byte(`{"<s>":0,"</s>":1,"<pad>":2,"<unk>":3}`), 0600); err != nil {
		t.Fatal(err)
	}
	if run(dir, io.Discard) == nil {
		t.Fatal("incomplete tokenizer accepted")
	}
	if run(t.TempDir(), io.Discard) == nil {
		t.Fatal("missing tokenizer accepted")
	}
}
