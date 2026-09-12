package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
)

// sourceProbe records the existing pre-parser on public diagnostic prompts. It
// runs no LLM, reads no app settings/catalog, and never approves an annotation.
func sourceProbe(ctx context.Context, args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("source-probe", flag.ContinueOnError)
	flags.SetOutput(errOut)
	input := flags.String("input", "", "Public diagnostic annotation JSON")
	output := flags.String("output", "", "Fresh JSON output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *input == "" || *output == "" || flags.NArg() != 0 {
		return errors.New("source-probe requires --input and --output")
	}
	file, err := os.Open(*input)
	if err != nil {
		return err
	}
	defer file.Close()
	var corpus struct {
		Schema  string `json:"schema"`
		Records []struct {
			ID     string `json:"id"`
			Prompt string `json:"prompt"`
		} `json:"records"`
	}
	if err := json.NewDecoder(io.LimitReader(file, 16<<20)).Decode(&corpus); err != nil {
		return err
	}
	if corpus.Schema != "intent-nlu-review/v1" || len(corpus.Records) == 0 || len(corpus.Records) > 10000 {
		return errors.New("expected bounded intent-nlu-review/v1 corpus")
	}
	type result struct {
		ID          string                 `json:"id"`
		Prompt      string                 `json:"prompt"`
		Translation core.IntentTranslation `json:"translation"`
		ElapsedMS   float64                `json:"elapsedMs"`
	}
	results := make([]result, 0, len(corpus.Records))
	seen := map[string]bool{}
	for _, row := range corpus.Records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if row.ID == "" || seen[row.ID] || row.Prompt == "" || len(row.Prompt) > 16384 {
			return errors.New("invalid or duplicate diagnostic prompt")
		}
		seen[row.ID] = true
		start := time.Now()
		x := lexicon.Extract(row.Prompt)
		results = append(results, result{row.ID, row.Prompt, x, float64(time.Since(start).Nanoseconds()) / 1e6})
	}
	destination, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer destination.Close()
	if err := json.NewEncoder(destination).Encode(results); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Recorded %d source snapshots; no model inference or annotation approval\n", len(results))
	return err
}
