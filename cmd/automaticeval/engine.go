package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"strconv"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/evaluation"
)

func runEngine(args []string, stdout io.Writer) error {
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	output := f.String("output", "-", "new output JSON, or - for stdout")
	if args[0] == "requests" {
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return errors.New("requests accepts only -output")
		}
		return writeResult(*output, stdout, evaluation.AutomaticFeatureRequests())
	}
	manifest := f.String("manifest", "", "frozen corpus JSON")
	features := f.String("features", "", "real derived-feature JSON")
	source := f.String("producer-source", "", "engine producer source SHA256")
	policy := f.String("policy", "", "frozen policy SHA256")
	frozen := f.Bool("policy-frozen", false, "policy was frozen before heldout observation")
	seed := f.String("seed", "42", "lossless uint64 seed shared by all variants")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 || *manifest == "" || *features == "" || !validFeatureHash(*source) || !validFeatureHash(*policy) {
		return errors.New("engine requires manifest, features, producer-source and policy hashes")
	}
	n, err := strconv.ParseUint(*seed, 10, 64)
	if err != nil {
		return err
	}
	var corpus evaluation.AutomaticCorpus
	if err := readJSON(*manifest, &corpus); err != nil {
		return err
	}
	var observed evaluation.AutomaticFeatures
	if err := readJSON(*features, &observed); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	measurements, err := evaluation.RunAutomaticFeatures(ctx, corpus, observed, evaluation.AutomaticEngineOptions{ProducerSourceSHA256: *source, PolicySHA256: *policy, PolicyFrozen: *frozen, Seed: core.NewRNGSeed(n)})
	if err != nil {
		return err
	}
	return writeResult(*output, stdout, measurements)
}
