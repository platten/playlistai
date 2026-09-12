// mertparity validates a prepared MERT pack through the shipped native worker.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/audioruntime"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) == 3 && os.Args[1] == "--bundle" {
		return audioruntime.RunMERT(os.Args[2])
	}
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: mertparity <prepared-bundle-directory>")
	}
	dir := os.Args[1]
	manifest, err := audio.ReadMERTBundle(dir)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	w := &audio.MERTWorker{Executable: exe, BundleDir: dir, Model: manifest.Model}
	defer w.Close()
	cold := time.Now()
	if err = w.Health(context.Background()); err != nil {
		return err
	}
	coldElapsed := time.Since(cold)
	warm := time.Now()
	if err = w.Health(context.Background()); err != nil {
		return err
	}
	warmElapsed := time.Since(warm)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	pcm := make([]float32, audio.MERTSegmentSamples)
	_, err = w.EmbedAudio(ctx, pcm)
	clear(pcm)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("native cancellation did not return deadline: %v", err)
	}
	reload := time.Now()
	if err = w.Health(context.Background()); err != nil {
		return fmt.Errorf("native restart after cancellation: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Model              any   `json:"model"`
		ColdHealthMS       int64 `json:"coldHealthMs"`
		WarmHealthMS       int64 `json:"warmHealthMs"`
		ReloadHealthMS     int64 `json:"reloadHealthMs"`
		CancellationPassed bool  `json:"cancellationPassed"`
	}{manifest.Model, coldElapsed.Milliseconds(), warmElapsed.Milliseconds(), time.Since(reload).Milliseconds(), true})
}
