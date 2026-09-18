package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/platten/playlistai/internal/librarymerge"
)

func execute(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	flags := flag.NewFlagSet("paipack-combine", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var output, workDir, maxRAMRaw, workersRaw string
	var seed uint64
	var trainingSample, clusters int
	var jsonOutput bool
	flags.StringVar(&output, "out", "", "output .paipack path (required)")
	flags.StringVar(&workDir, "work-dir", "", "parent directory for temporary merge state")
	flags.StringVar(&maxRAMRaw, "max-ram", "2GiB", "maximum fitting RAM, such as 2GiB or 512MiB")
	flags.StringVar(&workersRaw, "workers", "auto", "worker count or auto")
	flags.Uint64Var(&seed, "seed", 42, "deterministic learning seed")
	flags.IntVar(&trainingSample, "training-sample", 50_000, "maximum MERT training rows before the RAM cap")
	flags.IntVar(&clusters, "clusters", 0, "cluster count; 0 uses the recorded heuristic")
	flags.BoolVar(&jsonOutput, "json", false, "write the merge report as JSON")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "Usage: paipack-combine --out merged.paipack [options] input-1.paipack input-2.paipack ...")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return 2, err
	}
	if strings.TrimSpace(output) == "" {
		flags.Usage()
		return 2, errors.New("paipack-combine: --out is required")
	}
	if flags.NArg() < 2 {
		flags.Usage()
		return 2, errors.New("paipack-combine: at least two input packs are required")
	}
	maxRAM, err := parseByteSize(maxRAMRaw)
	if err != nil {
		return 2, fmt.Errorf("paipack-combine: --max-ram: %w", err)
	}
	workers, err := parseWorkers(workersRaw)
	if err != nil {
		return 2, fmt.Errorf("paipack-combine: --workers: %w", err)
	}
	report, err := librarymerge.Combine(ctx, flags.Args(), output, librarymerge.Options{
		WorkDir: workDir, MaxRAM: maxRAM, Workers: workers, Seed: seed, TrainingSample: trainingSample, Clusters: clusters,
	})
	if err != nil {
		return 1, err
	}
	if jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return 1, err
		}
	} else {
		_, err = fmt.Fprintf(stdout, "Combined %d unique packs: %d input tracks -> %d output tracks (%d duplicates removed)\nOutput: %s\nPack ID: %s\n",
			report.UniqueInputPacks, report.InputTracks, report.OutputTracks, report.RemovedDuplicates, output, report.Manifest.PackID)
		if err != nil {
			return 1, err
		}
	}
	return 0, nil
}

func parseWorkers(value string) (int, error) {
	if strings.EqualFold(strings.TrimSpace(value), "auto") {
		return 0, nil
	}
	workers, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || workers <= 0 {
		return 0, errors.New("expected auto or a positive integer")
	}
	return workers, nil
}

func parseByteSize(value string) (int64, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, errors.New("size is empty")
	}
	upper := strings.ToUpper(trimmed)
	multipliers := []struct {
		suffix string
		value  int64
	}{
		{"GIB", 1 << 30}, {"MIB", 1 << 20}, {"KIB", 1 << 10},
		{"GB", 1_000_000_000}, {"MB", 1_000_000}, {"KB", 1_000}, {"B", 1},
	}
	multiplier := int64(1)
	number := upper
	for _, candidate := range multipliers {
		if strings.HasSuffix(upper, candidate.suffix) {
			multiplier = candidate.value
			number = strings.TrimSpace(trimmed[:len(trimmed)-len(candidate.suffix)])
			break
		}
	}
	amount, err := strconv.ParseInt(number, 10, 64)
	if err != nil || amount <= 0 || amount > (1<<63-1)/multiplier {
		return 0, fmt.Errorf("invalid positive byte size %q", value)
	}
	return amount * multiplier, nil
}
