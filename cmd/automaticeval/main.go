// automaticeval builds a label-only corpus, explicitly extracts real model
// features, runs the production engine, and evaluates independent measurements.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/platten/playlistai/internal/audioruntime"
	"github.com/platten/playlistai/internal/evaluation"
)

const maxInputBytes = 64 << 20

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--audio-worker" {
		if err := audioruntime.Run(os.Args[2]); err != nil { //nolint:staticcheck // native worker may return after clean EOF
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) > 0 && (args[0] == "requests" || args[0] == "engine") {
		return runEngine(args, stdout)
	}
	if len(args) > 0 && args[0] == "measure" {
		return runMeasure(args[1:], stdout)
	}
	if len(args) == 0 || (args[0] != "corpus" && args[0] != "evaluate") {
		return errors.New("usage: automaticeval corpus|evaluate [options]")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	output := flags.String("output", "-", "new output JSON file (0600, no overwrite), or - for stdout")
	if args[0] == "corpus" {
		raw := flags.String("annotations", "", "official raw annotation TSV")
		full := flags.String("audio-hashes", "", "official full-audio track checksum metadata")
		low := flags.String("low-audio-hashes", "", "official low-audio track checksum metadata")
		seed := flags.String("seed", "automatic-evaluation-v1", "immutable corpus selection seed")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *raw == "" || *full == "" || *low == "" {
			return errors.New("corpus requires -annotations, -audio-hashes, and -low-audio-hashes")
		}
		inputs := make([][]byte, 3)
		for i, path := range []string{*raw, *full, *low} {
			var err error
			inputs[i], err = readBounded(path)
			if err != nil {
				return err
			}
		}
		corpus, err := evaluation.BuildAutomaticCorpus(inputs[0], inputs[1], inputs[2], *seed)
		if err != nil {
			return err
		}
		return writeResult(*output, stdout, corpus)
	}
	manifest := flags.String("manifest", "", "frozen 600-recording corpus JSON")
	measurements := flags.String("measurements", "", "real model/engine observations, independent of labels")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *manifest == "" || *measurements == "" {
		return errors.New("evaluate requires -manifest and -measurements")
	}
	var corpus evaluation.AutomaticCorpus
	if err := readJSON(*manifest, &corpus); err != nil {
		return err
	}
	var observed evaluation.AutomaticMeasurements
	if err := readJSON(*measurements, &observed); err != nil {
		return err
	}
	report, err := evaluation.EvaluateAutomatic(corpus, observed)
	if err != nil {
		return err
	}
	if err := writeResult(*output, stdout, report); err != nil {
		return err
	}
	if report.State != "pass" {
		return fmt.Errorf("automatic evaluation gates: %s (report retained)", report.State)
	}
	return nil
}

func readBounded(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxInputBytes {
		return nil, errors.New("automaticeval input exceeds 64 MiB")
	}
	return raw, nil
}

func readJSON(path string, out any) error {
	raw, err := readBounded(path)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(out); err != nil {
		return err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return errors.New("automaticeval expects exactly one JSON object")
	}
	return nil
}

func writeResult(path string, stdout io.Writer, result any) error {
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if path == "-" {
		_, err = stdout.Write(raw)
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(raw)
	closeErr := f.Close()
	return errors.Join(writeErr, closeErr)
}
