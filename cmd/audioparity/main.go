// audioparity emits only synthetic signal features and tokenizer fixtures for
// developer reference comparison. It does not fetch or persist preview audio.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"github.com/platten/playlistai/internal/audio"
)

func main() {
	dir := flag.String("tokenizer", "", "reference tokenizer directory")
	flag.Parse()
	if err := run(*dir, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(dir string, output io.Writer) error {
	tokenizer, err := audio.LoadTokenizer(filepath.Join(dir, "vocab.json"), filepath.Join(dir, "merges.txt"))
	if err != nil {
		return err
	}
	type tokenCase struct {
		Text string  `json:"text"`
		IDs  []int64 `json:"ids"`
	}
	type melCase struct {
		ToneHz   float64   `json:"toneHz"`
		Features []float32 `json:"features"`
	}
	report := struct {
		Preprocessing string      `json:"preprocessing"`
		Tokens        []tokenCase `json:"tokens"`
		Mels          []melCase   `json:"mels"`
	}{Preprocessing: audio.PreprocessingVersion}
	for _, text := range []string{"ambient electronica", "relaxing but not sleepy", "instrumental, no vocals", "宇多田ヒカルの音楽", "Bj\u00f6rk's shimmering textures", "مرحبا بالعالم", " don't  stop\n the music!"} {
		ids, _, err := tokenizer.Encode(text)
		if err != nil {
			return err
		}
		report.Tokens = append(report.Tokens, tokenCase{Text: text, IDs: ids})
	}
	for _, hz := range []float64{220, 440, 880} {
		pcm := make([]float32, audio.SegmentSamples)
		for i := range pcm {
			pcm[i] = float32(0.1 * math.Sin(2*math.Pi*hz*float64(i)/audio.SampleRate))
		}
		mel, err := audio.LogMel(pcm)
		clear(pcm)
		if err != nil {
			return err
		}
		report.Mels = append(report.Mels, melCase{ToneHz: hz, Features: mel})
	}
	return json.NewEncoder(output).Encode(report)
}
