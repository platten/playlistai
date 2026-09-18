package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/libraryindex"
	"github.com/platten/playlistai/internal/librarysearch"
)

func runLearningCommand(ctx context.Context, args []string, stdout, stderr io.Writer) (code int, runErr error) {
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
	if gracefulStopRequested(ctx) {
		return 130, libraryindex.ErrShutdownRequested
	}
	state, err := libraryindex.OpenState(ctx, common.state, command, max(2, plan.HeavyWorkers))
	if err != nil {
		return 1, err
	}
	defer func() {
		if closeErr := state.Close(); closeErr != nil {
			code, runErr = 1, fmt.Errorf("close durable state: %w", closeErr)
		}
	}()
	switch command {
	case "fit":
		progress := startPipelineProgress(ctx, state, nil, stderr, common.noProgress, "Fitting library", progressActivity)
		succeeded := false
		defer func() { progress.Stop(succeeded) }()
		result, err := state.Fit(ctx, libraryindex.FitOptions{Seed: uint64(common.seed), TrainingSample: *trainingSample, Clusters: *clusters, Refit: *refit, Plan: plan})
		if err != nil {
			return 1, err
		}
		if gracefulStopRequested(ctx) {
			return 130, libraryindex.ErrShutdownRequested
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
		if gracefulStopRequested(ctx) {
			return 130, libraryindex.ErrShutdownRequested
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
		if gracefulStopRequested(ctx) {
			return 130, libraryindex.ErrShutdownRequested
		}
		succeeded = true
		progress.Stop(true)
		return 0, json.NewEncoder(stdout).Encode(manifest)
	default:
		return 1, fmt.Errorf("unknown learning command %q", command)
	}
}

type benchmarkResult struct {
	Configuration     string                      `json:"configuration"`
	Skipped           string                      `json:"skipped,omitempty"`
	Resources         libraryindex.ResourcePlan   `json:"resources"`
	ColdSetup         time.Duration               `json:"coldSetup"`
	WarmAnalysis      time.Duration               `json:"warmAnalysis"`
	Report            libraryindex.AnalysisReport `json:"report"`
	SemanticDigest    string                      `json:"semanticDigest,omitempty"`
	SerialEquivalent  *bool                       `json:"serialEquivalent,omitempty"`
	PeakOwnedRSSBytes int64                       `json:"peakOwnedRssBytes"`
	TracksPerSecond   float64                     `json:"tracksPerSecond"`
	FitDuration       time.Duration               `json:"fitDuration"`
	ExportDuration    time.Duration               `json:"exportDuration"`
	PackBytes         int64                       `json:"packBytes"`
	PackBytesPerTrack float64                     `json:"packBytesPerTrack"`
	QuerySamples      int                         `json:"querySamples"`
	QueryP50          time.Duration               `json:"queryP50"`
	QueryP95          time.Duration               `json:"queryP95"`
	ResumeOpen        time.Duration               `json:"resumeOpen"`
	RAMTargetMet      bool                        `json:"ramTargetMet"`
}

func runBenchmark(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	if len(args) > 0 && args[0] == "scale" {
		return runScaleBenchmark(ctx, args[1:], stdout, stderr)
	}
	if len(args) == 0 || args[0] != "concurrency" {
		return 1, errors.New("usage: playlist-indexer bench concurrency --root PATH --sample-tracks N | playlist-indexer bench scale [--rows 2000000]")
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
	device := flags.String("device", "auto", "auto, cpu, cuda, or cuda:INDEX")
	modelBundle := flags.String("model-bundle", "", "verified local prepared MERT bundle")
	if err := flags.Parse(args[1:]); err != nil {
		return 1, err
	}
	if *rootPath == "" || *sampleTracks <= 0 {
		return 1, errors.New("bench concurrency requires --root and positive --sample-tracks")
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
		name                       string
		mode                       libraryindex.ConcurrencyMode
		workers, sessions, threads int
	}{
		{"serial", libraryindex.ConcurrencySerial, 1, 1, 1},
		{"1-session-1-thread", libraryindex.ConcurrencyManual, basePlan.HeavyWorkers, 1, 1},
		{"1-session-2-thread", libraryindex.ConcurrencyManual, basePlan.HeavyWorkers, 1, 2},
		{"2-session-1-thread", libraryindex.ConcurrencyManual, basePlan.HeavyWorkers, 2, 1},
		{"auto", libraryindex.ConcurrencyAuto, 0, 0, 0},
	}
	results := make([]benchmarkResult, 0, len(configs))
	for _, config := range configs {
		if gracefulStopRequested(ctx) {
			return 130, libraryindex.ErrShutdownRequested
		}
		if config.workers > basePlan.EffectiveCPUSlots {
			results = append(results, benchmarkResult{Configuration: config.name, Skipped: fmt.Sprintf("requires %d effective CPU slots; available %d", config.workers, basePlan.EffectiveCPUSlots)})
			continue
		}
		overrides := libraryindex.ResourceOverrides{Mode: config.mode, IOProfile: basePlan.IOProfile, MaxRAM: basePlan.MaxRAM, MaxOpenFiles: basePlan.MaxOpenFiles, QueueDepth: basePlan.QueueDepth}
		if config.workers == 0 {
			overrides.WorkersAuto = true
		} else {
			overrides.Workers = config.workers
			overrides.InferenceWorkers = config.sessions
			overrides.InferenceThreads = config.threads
		}
		plan, planErr := libraryindex.ResolveResourcePlan(overrides)
		if planErr != nil {
			results = append(results, benchmarkResult{Configuration: config.name, Skipped: planErr.Error()})
			continue
		}
		result, runErr := benchmarkConfiguration(ctx, common, root, paths, profileValue, *modelBundle, *device, config.name, plan, stderr)
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

func benchmarkConfiguration(ctx context.Context, common commonFlags, rootPath string, paths []string, profile libraryindex.SamplingProfile, modelBundle, requestedDevice, name string, plan libraryindex.ResourcePlan, stderr io.Writer) (benchmarkResult, error) {
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
	bundleDir, manifest, err := ensureModel(ctx, benchFlags, modelBundle, requestedDevice, stderr)
	if err != nil {
		return result, err
	}
	device, err := audio.ResolveMERTDevice(requestedDevice, manifest.Backend())
	if err != nil {
		return result, err
	}
	if name == "auto" {
		plan = tunePlanForDevice(plan, common, device)
	}
	result.Resources = plan
	executable, err := os.Executable()
	if err != nil {
		return result, err
	}
	pool, resolvedPlan, err := warmMERTPool(ctx, executable, bundleDir, manifest, device, plan, stderr, nil)
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
	analyzer := &libraryindex.Analyzer{State: state, Runtime: codec, MERT: pool, Plan: plan, Admission: libraryindex.NewAdmission(plan, plan.MaxRAM/4), Profile: profile, StopAdmission: gracefulStopFromContext(ctx)}
	defer analyzer.Admission.Close()
	analysisStart := time.Now()
	monitorDone := make(chan struct{})
	peakRSS := make(chan int64, 1)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		var peak int64
		for {
			owned := processRSSBytes() + pool.ResidentBytes()
			if owned > peak {
				peak = owned
			}
			select {
			case <-monitorDone:
				peakRSS <- peak
				return
			case <-ticker.C:
			}
		}
	}()
	result.Report, err = analyzer.Run(ctx, libraryindex.AnalysisOptions{Metadata: true, Audio: true, Profile: profile}, done)
	result.WarmAnalysis = time.Since(analysisStart)
	if seconds := result.WarmAnalysis.Seconds(); seconds > 0 {
		result.TracksPerSecond = float64(result.Report.AudioCompleted) / seconds
	}
	if err == nil {
		result.SemanticDigest, err = state.SemanticDigest(ctx)
	}
	if err == nil && gracefulStopRequested(ctx) {
		err = libraryindex.ErrShutdownRequested
	}
	if err == nil {
		fitStart := time.Now()
		_, err = state.Fit(ctx, libraryindex.FitOptions{Seed: uint64(common.seed), Plan: plan})
		result.FitDuration = time.Since(fitStart)
	}
	if err == nil && gracefulStopRequested(ctx) {
		err = libraryindex.ErrShutdownRequested
	}
	packPath := filepath.Join(scratch, "benchmark.paipack")
	if err == nil {
		exportStart := time.Now()
		_, err = state.ExportPack(ctx, packPath)
		result.ExportDuration = time.Since(exportStart)
	}
	if err == nil && gracefulStopRequested(ctx) {
		err = libraryindex.ErrShutdownRequested
	}
	if err == nil {
		if info, statErr := os.Stat(packPath); statErr != nil {
			err = statErr
		} else {
			result.PackBytes = info.Size()
			if result.Report.MetadataCompleted > 0 {
				result.PackBytesPerTrack = float64(info.Size()) / float64(result.Report.MetadataCompleted)
			}
		}
	}
	if err == nil && result.Report.AudioCompleted > 1 {
		var queryID string
		queryErr := state.Reader().QueryRowContext(ctx, `SELECT file_id FROM mert_results ORDER BY file_id LIMIT 1`).Scan(&queryID)
		if queryErr == nil {
			vector, vectorErr := state.TrackVector(ctx, queryID)
			manager, managerErr := librarysearch.OpenManager(ctx, filepath.Join(scratch, "generations", "indexes"))
			var handle *librarysearch.Handle
			if managerErr == nil {
				handle, managerErr = manager.Pin()
			}
			queryErr = errors.Join(vectorErr, managerErr)
			latencies := make([]time.Duration, 0, 25)
			if queryErr == nil {
				for range 25 {
					if gracefulStopRequested(ctx) {
						queryErr = libraryindex.ErrShutdownRequested
						break
					}
					started := time.Now()
					_, queryErr = handle.Search(ctx, librarysearch.Query{Vector: vector, Limit: min(50, int(result.Report.AudioCompleted)-1), Workers: plan.IndexWorkers, Exclude: map[string]struct{}{queryID: {}}})
					latencies = append(latencies, time.Since(started))
					if queryErr != nil {
						break
					}
				}
			}
			if handle != nil {
				handle.Release()
			}
			if manager != nil {
				queryErr = errors.Join(queryErr, manager.Close())
			}
			clear(vector)
			if queryErr == nil {
				result.QuerySamples = len(latencies)
				result.QueryP50, result.QueryP95 = durationPercentile(latencies, .50), durationPercentile(latencies, .95)
			} else {
				err = queryErr
			}
		} else if !errors.Is(queryErr, sql.ErrNoRows) {
			err = queryErr
		}
	}
	if err == nil {
		if closeErr := state.Close(); closeErr != nil {
			err = closeErr
		} else {
			resumeStart := time.Now()
			resumed, openErr := libraryindex.OpenState(ctx, scratch, "bench-resume-"+name, max(2, plan.HeavyWorkers))
			result.ResumeOpen = time.Since(resumeStart)
			if openErr != nil {
				err = openErr
			} else {
				err = resumed.Close()
			}
		}
	}
	close(monitorDone)
	result.PeakOwnedRSSBytes = <-peakRSS
	result.RAMTargetMet = result.PeakOwnedRSSBytes <= plan.MaxRAM
	return result, err
}

func durationPercentile(values []time.Duration, percentile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]time.Duration(nil), values...)
	slices.Sort(ordered)
	index := int(math.Ceil(percentile*float64(len(ordered)))) - 1
	index = max(0, min(index, len(ordered)-1))
	return ordered[index]
}
