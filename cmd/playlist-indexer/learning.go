package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/platten/playlistai/internal/libraryindex"
)

func runLearningCommand(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	command := args[0]
	if command == "bench" {
		return runBenchmark(ctx, args[1:], stdout, stderr)
	}
	flags := flag.NewFlagSet("playlist-indexer "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	var common commonFlags
	if err := common.preloadConfig(args[1:]); err != nil {
		return 1, err
	}
	addCommon(flags, &common)
	trainingSample := flags.Int("training-sample", 0, "maximum deterministic training sample")
	clusters := flags.Int("clusters", 0, "spherical cluster count")
	refit := flags.Bool("refit", false, "force fitting instead of compatible reuse")
	trackID := flags.String("track-id", "", "stable local track ID")
	limit := flags.Int("limit", 20, "maximum neighbors")
	out := flags.String("out", "", "portable .paipack destination")
	if err := flags.Parse(args[1:]); err != nil {
		return 1, err
	}
	plan, err := common.metadataPlan()
	if err != nil {
		return 1, err
	}
	state, err := libraryindex.OpenState(ctx, common.state, command, max(2, plan.HeavyWorkers))
	if err != nil {
		return 1, err
	}
	defer state.Close()
	switch command {
	case "fit":
		progress := startPipelineProgress(ctx, state, nil, stderr, common.noProgress, "Fitting library", progressActivity)
		succeeded := false
		defer func() { progress.Stop(succeeded) }()
		result, err := state.Fit(ctx, libraryindex.FitOptions{Seed: uint64(common.seed), TrainingSample: *trainingSample, Clusters: *clusters, Refit: *refit, Plan: plan})
		if err != nil {
			return 1, err
		}
		succeeded = true
		progress.Stop(true)
		return 0, json.NewEncoder(stdout).Encode(result)
	case "neighbors":
		if *trackID == "" || *limit <= 0 {
			return 1, errors.New("neighbors requires --track-id and a positive --limit")
		}
		hits, err := state.SearchNeighbors(ctx, *trackID, *limit, plan.IndexWorkers)
		if err != nil {
			return 1, err
		}
		return 0, json.NewEncoder(stdout).Encode(map[string]any{"trackId": *trackID, "neighbors": hits})
	case "export":
		if *out == "" {
			return 1, errors.New("export requires --out")
		}
		progress := startPipelineProgress(ctx, state, nil, stderr, common.noProgress, "Exporting library pack", progressActivity)
		succeeded := false
		defer func() { progress.Stop(succeeded) }()
		manifest, err := state.ExportPack(ctx, *out)
		if err != nil {
			return 1, err
		}
		succeeded = true
		progress.Stop(true)
		return 0, json.NewEncoder(stdout).Encode(manifest)
	default:
		return 1, fmt.Errorf("unknown learning command %q", command)
	}
}

type benchmarkResult struct {
	Configuration    string                      `json:"configuration"`
	Skipped          string                      `json:"skipped,omitempty"`
	Resources        libraryindex.ResourcePlan   `json:"resources"`
	ColdSetup        time.Duration               `json:"coldSetup"`
	WarmAnalysis     time.Duration               `json:"warmAnalysis"`
	Report           libraryindex.AnalysisReport `json:"report"`
	SemanticDigest   string                      `json:"semanticDigest,omitempty"`
	SerialEquivalent *bool                       `json:"serialEquivalent,omitempty"`
}

func runBenchmark(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	if len(args) == 0 || args[0] != "concurrency" {
		return 1, errors.New("usage: playlist-indexer bench concurrency --root PATH --sample-tracks N")
	}
	flags := flag.NewFlagSet("playlist-indexer bench concurrency", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var common commonFlags
	if err := common.preloadConfig(args[1:]); err != nil {
		return 1, err
	}
	addCommon(flags, &common)
	rootPath := flags.String("root", "", "authorized source tree")
	sampleTracks := flags.Int("sample-tracks", 0, "positive deterministic real-audio sample size")
	profile := flags.String("profile", "balanced", "fast, balanced, or deep")
	device := flags.String("device", "cpu", "cpu or auto")
	modelBundle := flags.String("model-bundle", "", "verified local prepared MERT bundle")
	if err := flags.Parse(args[1:]); err != nil {
		return 1, err
	}
	if *rootPath == "" || *sampleTracks <= 0 {
		return 1, errors.New("bench concurrency requires --root and positive --sample-tracks")
	}
	if *device != "cpu" && *device != "auto" {
		return 1, errors.New("only the validated CPU backend is available")
	}
	profileValue := libraryindex.SamplingProfile(*profile)
	if _, err := libraryindex.SamplingWindows(60, profileValue); err != nil {
		return 1, err
	}
	root, err := filepath.Abs(*rootPath)
	if err != nil {
		return 1, err
	}
	paths, err := libraryindex.SampleAudioFiles(ctx, root, *sampleTracks, uint64(common.seed))
	if err != nil {
		return 1, err
	}
	if len(paths) == 0 {
		return 1, errors.New("benchmark found no supported audio files")
	}
	basePlan, err := common.plan()
	if err != nil {
		return 1, err
	}
	configs := []struct {
		name    string
		mode    libraryindex.ConcurrencyMode
		workers int
	}{
		{"serial", libraryindex.ConcurrencySerial, 1}, {"2-worker", libraryindex.ConcurrencyManual, 2},
		{"4-worker", libraryindex.ConcurrencyManual, 4}, {"auto", libraryindex.ConcurrencyAuto, 0},
	}
	results := make([]benchmarkResult, 0, len(configs))
	for _, config := range configs {
		if config.workers > basePlan.EffectiveCPUSlots {
			results = append(results, benchmarkResult{Configuration: config.name, Skipped: fmt.Sprintf("requires %d effective CPU slots; available %d", config.workers, basePlan.EffectiveCPUSlots)})
			continue
		}
		overrides := libraryindex.ResourceOverrides{Mode: config.mode, IOProfile: basePlan.IOProfile, MaxRAM: basePlan.MaxRAM, MaxOpenFiles: basePlan.MaxOpenFiles, QueueDepth: basePlan.QueueDepth}
		if config.workers == 0 {
			overrides.WorkersAuto = true
		} else {
			overrides.Workers = config.workers
			overrides.InferenceThreads = 1
		}
		plan, planErr := libraryindex.ResolveResourcePlan(overrides)
		if planErr != nil {
			results = append(results, benchmarkResult{Configuration: config.name, Skipped: planErr.Error()})
			continue
		}
		result, runErr := benchmarkConfiguration(ctx, common, root, paths, profileValue, *modelBundle, config.name, plan, stderr)
		if runErr != nil {
			return 1, fmt.Errorf("benchmark %s: %w", config.name, runErr)
		}
		results = append(results, result)
	}
	serialDigest := ""
	for i := range results {
		if results[i].Configuration == "serial" && results[i].Skipped == "" {
			serialDigest = results[i].SemanticDigest
			break
		}
	}
	if serialDigest != "" {
		for i := range results {
			if results[i].Skipped == "" {
				equivalent := results[i].SemanticDigest == serialDigest
				results[i].SerialEquivalent = &equivalent
			}
		}
	}
	return 0, json.NewEncoder(stdout).Encode(map[string]any{
		"kind": "real-audio-concurrency", "sampleSeed": common.seed, "sampleTracksRequested": *sampleTracks,
		"sampleTracksFound": len(paths), "host": runtime.GOOS + "/" + runtime.GOARCH, "results": results,
		"notes": []string{"isolated scratch state", "cold setup includes codec/model install and worker warmup", "warm analysis uses real decoder, DSP, and MERT"},
	})
}

func benchmarkConfiguration(ctx context.Context, common commonFlags, rootPath string, paths []string, profile libraryindex.SamplingProfile, modelBundle, name string, plan libraryindex.ResourcePlan, stderr io.Writer) (benchmarkResult, error) {
	result := benchmarkResult{Configuration: name, Resources: plan}
	scratch, err := os.MkdirTemp("", "playlist-indexer-bench-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(scratch)
	benchFlags := common
	benchFlags.state = scratch
	setupStart := time.Now()
	codec, err := resolveCodec(ctx, benchFlags)
	if err != nil {
		return result, err
	}
	bundleDir, manifest, err := ensureModel(ctx, benchFlags, modelBundle, stderr)
	if err != nil {
		return result, err
	}
	executable, err := os.Executable()
	if err != nil {
		return result, err
	}
	pool, resolvedPlan, err := warmMERTPool(ctx, executable, bundleDir, manifest, plan, stderr)
	if err != nil {
		return result, err
	}
	plan, result.Resources = resolvedPlan, resolvedPlan
	defer pool.Close()
	state, err := libraryindex.OpenState(ctx, scratch, "bench-"+name, max(2, plan.HeavyWorkers))
	if err != nil {
		return result, err
	}
	defer state.Close()
	root, err := state.EnsureRoot(ctx, rootPath, "benchmark-root")
	if err != nil {
		return result, err
	}
	epoch, err := state.BeginEpoch(ctx, []libraryindex.Root{root})
	if err != nil {
		return result, err
	}
	jobs := map[string]string{"metadata": libraryindex.MetadataSemanticKey(codec.ID()), "audio": libraryindex.AudioSemanticKey(codec.ID(), pool.Identity(), profile)}
	for _, path := range paths {
		if err := state.ObserveSamplePath(ctx, epoch, root, path, jobs); err != nil {
			return result, err
		}
	}
	result.ColdSetup = time.Since(setupStart)
	done := make(chan struct{})
	close(done)
	analyzer := &libraryindex.Analyzer{State: state, Runtime: codec, MERT: pool, Plan: plan, Admission: libraryindex.NewAdmission(plan, plan.MaxRAM/4), Profile: profile}
	defer analyzer.Admission.Close()
	analysisStart := time.Now()
	result.Report, err = analyzer.Run(ctx, libraryindex.AnalysisOptions{Metadata: true, Audio: true, Profile: profile}, done)
	result.WarmAnalysis = time.Since(analysisStart)
	if err == nil {
		result.SemanticDigest, err = state.SemanticDigest(ctx)
	}
	return result, err
}
