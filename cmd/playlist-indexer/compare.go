package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"time"

	"github.com/platten/playlistai/internal/intent/llama"
)

func runCompare(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	flags := flag.NewFlagSet("playlist-indexer compare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "", "JSON containing prompt, criteria, coverage, model_fingerprint, clap_cosine and dsp")
	out := flags.String("out", "", "new diagnostic JSON report (existing files are preserved)")
	model := flags.String("model", "", "local GGUF model")
	binary := flags.String("runtime", "", "optional local llama executable")
	timeout := flags.Duration("timeout", 2*time.Minute, "total deadline including model startup")
	if err := flags.Parse(args); err != nil {
		return 1, err
	}
	if *input == "" || *out == "" || *model == "" || *timeout <= 0 || *timeout > 2*time.Minute || flags.NArg() != 0 {
		return 1, errors.New("compare requires --input, --out, --model and timeout in (0,2m]")
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	go func() {
		select {
		case <-gracefulStopFromContext(ctx):
			cancel()
		case <-ctx.Done():
		}
	}()
	f, err := os.Open(*input)
	if err != nil {
		return 1, err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, 65537))
	_ = f.Close()
	if readErr != nil {
		return 1, readErr
	}
	if len(data) > 65536 {
		return 1, errors.New("comparison input exceeds 64 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var in llama.AudioComparisonInput
	err = d.Decode(&in)
	if err == nil {
		if d.Decode(new(any)) != io.EOF {
			err = errors.New("input must contain exactly one JSON object")
		}
	}
	if err != nil {
		return 1, err
	}
	if err = in.Validate(); err != nil {
		return 1, err
	}
	if _, err = os.Lstat(*out); err == nil {
		return 1, errors.New("comparison output already exists")
	} else if !os.IsNotExist(err) {
		return 1, err
	}
	parser, err := llama.New(ctx, llama.Options{ModelPath: *model, BinaryPath: *binary, NCtx: 8192, NThreads: 2, StartTimeout: 30 * time.Second})
	if err != nil {
		return 1, err
	}
	defer parser.Close()
	result, err := parser.CompareAudio(ctx, in)
	if err != nil {
		return 1, err
	}
	if err = ctx.Err(); err != nil {
		return 1, err
	}
	data, err = json.MarshalIndent(result, "", "  ")
	if err != nil {
		return 1, err
	}
	data = append(data, '\n')
	file, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 1, err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(*out)
		return 1, err
	}
	return 0, json.NewEncoder(stdout).Encode(struct {
		Status   string `json:"status"`
		Repaired bool   `json:"repaired"`
	}{result.Status, result.Repaired})
}
