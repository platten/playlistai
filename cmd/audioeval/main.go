// audioeval audits recording/artist splits and reports frozen-policy ablations
// from externally reviewed listening data. It never fabricates labels.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/platten/playlistai/internal/evaluation"
)

func main() {
	flag := flag.NewFlagSet("audioeval", flag.ExitOnError)
	input := flag.String("input", "", "reviewed audio evaluation JSON")
	output := flag.String("output", "", "report JSON path")
	_ = flag.Parse(os.Args[1:])
	if err := run(*input, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(input, output string) error {
	raw, err := os.ReadFile(input)
	if err != nil {
		return err
	}
	var data evaluation.AudioReviewDataset
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}
	report, err := evaluation.EvaluateAudioReviews(data)
	if err != nil {
		return err
	}
	raw, err = json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(output, append(raw, '\n'), 0o600)
}
