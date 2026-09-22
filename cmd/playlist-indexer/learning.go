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
	out := flags.String("out", "", "portable .paipack destination (with prebuilt indexes)")
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
	Configuration      string                      `json:"configuration"`
	Skipped            string                      `json:"skipped,omitempty"`
	Resources          libraryindex.ResourcePlan   `json:"resources"`
	ColdSetup          time.Duration               `json:"coldSetup"`
	SourcePreRead      time.Duration               `json:"sourcePreRead"`
	SourcePreReadBytes int64                       `json:"sourcePreReadBytes"`
	WarmAnalysis       time.Duration               `json:"warmAnalysis"`
	Report             libraryindex.AnalysisReport `json:"report"`
	SemanticDigest     string                      `json:"semanticDigest,omitempty"`
	SerialEquivalent   *bool                       `json:"serialEquivalent,omitempty"`
	PeakOwnedRSSBytes  int64                       `json:"peakOwnedRssBytes"`
	TracksPerSecond    float64                     `json:"tracksPerSecond"`
	FitDuration        time.Duration               `json:"fitDuration"`
	ExportDuration     time.Duration               `json:"exportDuration"`
	PackBytes          int64                       `json:"packBytes"`
	PackBytesPerTrack  float64                     `json:"packBytesPerTrack"`
	QuerySamples       int                         `json:"querySamples"`
	QueryP50           time.Duration               `json:"queryP50"`
	QueryP95           time.Duration               `json:"queryP95"`
	ResumeOpen         time.Duration               `json:"resumeOpen"`
	RAMTargetMet       bool                        `json:"ramTargetMet"`
	Host               benchmarkHostDiagnostics    `json:"hostDiagnostics"`
	ARCBefore          benchmarkARCSnapshot        `json:"arcBefore"`
	ARCAfter           benchmarkARCSnapshot        `json:"arcAfter"`
	ARCDelta           benchmarkARCSnapshot        `json:"arcDelta"`
}

type benchmarkHostDiagnostics struct {
	Filesystem    string   `json:"filesystem"`
	Affinity      string   `json:"affinity,omitempty"`
	GPUDevice     string   `json:"gpuDevice"`
	GPUIdentities []string `json:"gpuIdentities,omitempty"`
	PowerProfile  string   `json:"powerProfile,omitempty"`
}

type benchmarkARCSnapshot struct {
	SizeBytes int64 `json:"sizeBytes"`
	Hits      int64 `json:"hits"`
	Misses    int64 `json:"misses"`
}

type concurrencyBenchmarkConfig struct {
	name                                        string
	mode                                        libraryindex.ConcurrencyMode
	workers, decode, dsp, io, sessions, threads int
	buffering                                   libraryindex.BufferingMode
}

type pipelineMatrixResult struct {
	Configuration    string                    `json:"configuration"`
	Skipped          string                    `json:"skipped,omitempty"`
	Resources        libraryindex.ResourcePlan `json:"resources"`
	Trials           []benchmarkResult         `json:"trials,omitempty"`
	MedianWall       time.Duration             `json:"medianWall"`
	P95Wall          time.Duration             `json:"p95Wall"`
	MedianThroughput float64                   `json:"medianTracksPerSecond"`
	Equivalent       bool                      `json:"equivalent"`
	Eligible         bool                      `json:"eligible"`
	Ineligible       []string                  `json:"ineligible,omitempty"`
}

type benchmarkConfigurationOptions struct {
	PreReadSources bool
	PipelineOnly   bool
}

func runBenchmark(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	if len(args) > 0 && args[0] == "scale" {
		return runScaleBenchmark(ctx, args[1:], stdout, stderr)
	}
	if len(args) == 0 || args[0] != "concurrency" {
		return 1, errors.New("usage: playlist-indexer bench concurrency [--matrix pipeline|gpu] --root PATH --sample-tracks N | playlist-indexer bench scale [--rows 2000000]")
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
	matrix := flags.String("matrix", "", "optional benchmark matrix: pipeline or gpu")
	trials := flags.Int("trials", 3, "trials per matrix configuration (gpu requires exactly 3)")
	if err := flags.Parse(args[1:]); err != nil {
		return 1, err
	}
	if *rootPath == "" || *sampleTracks <= 0 {
		return 1, errors.New("bench concurrency requires --root and positive --sample-tracks")
	}
	if *matrix != "" && *matrix != "pipeline" && *matrix != "gpu" {
		return 1, errors.New("--matrix accepts only pipeline or gpu")
	}
	if *matrix == "pipeline" && *trials < 3 {
		return 1, errors.New("--matrix pipeline requires at least three trials")
	}
	if *matrix == "gpu" && *trials != 3 {
		return 1, errors.New("--matrix gpu requires exactly three trials")
	}
	if *matrix == "gpu" {
		normalized, preference, err := audio.ParseMERTDevicePreference(*device)
		if err != nil {
			return 1, err
		}
		if preference == "auto" {
			normalized, preference = "cuda:0", "cuda"
		}
		if preference != "cuda" {
			return 1, errors.New("--matrix gpu requires a CUDA device")
		}
		*device = normalized
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
	if *matrix == "gpu" {
		return runGPUMatrixBenchmark(ctx, common, root, paths, *sampleTracks, profileValue, *modelBundle, *device, basePlan, stdout, stderr)
	}
	if *matrix == "pipeline" {
		return runPipelineMatrixBenchmark(ctx, common, root, paths, *sampleTracks, profileValue, *modelBundle, *device, *trials, basePlan, stdout, stderr)
	}
	configs := []concurrencyBenchmarkConfig{
		{name: "serial", mode: libraryindex.ConcurrencySerial, workers: 1, sessions: 1, threads: 1},
		{name: "1-session-1-thread", mode: libraryindex.ConcurrencyManual, workers: basePlan.HeavyWorkers, sessions: 1, threads: 1},
		{name: "1-session-2-thread", mode: libraryindex.ConcurrencyManual, workers: basePlan.HeavyWorkers, sessions: 1, threads: 2},
		{name: "2-session-1-thread", mode: libraryindex.ConcurrencyManual, workers: basePlan.HeavyWorkers, sessions: 2, threads: 1},
		{name: "auto", mode: libraryindex.ConcurrencyAuto},
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
		result, runErr := benchmarkConfiguration(ctx, common, root, paths, profileValue, *modelBundle, *device, config.name, plan, benchmarkConfigurationOptions{}, stderr)
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

func runPipelineMatrixBenchmark(ctx context.Context, common commonFlags, root string, paths []string, sampleTracksRequested int, profile libraryindex.SamplingProfile, modelBundle, device string, trials int, basePlan libraryindex.ResourcePlan, stdout, stderr io.Writer) (int, error) {
	configs := make([]concurrencyBenchmarkConfig, 0, 3*4*3*2*2)
	for _, workers := range []int{8, 10, 11} {
		for _, decode := range []int{4, 6, 8, 10} {
			for _, dsp := range []int{1, 2, 3} {
				for _, ioWorkers := range []int{1, 2} {
					for _, buffering := range []libraryindex.BufferingMode{libraryindex.BufferingWindow, libraryindex.BufferingTrack} {
						configs = append(configs, concurrencyBenchmarkConfig{
							name: fmt.Sprintf("workers-%d_decode-%d_dsp-%d_io-%d_buffer-%s", workers, decode, dsp, ioWorkers, buffering),
							mode: libraryindex.ConcurrencyManual, workers: workers, decode: decode, dsp: dsp,
							io: ioWorkers, sessions: 1, threads: 1, buffering: buffering,
						})
					}
				}
			}
		}
	}
	results := make([]pipelineMatrixResult, len(configs))
	for i, config := range configs {
		results[i].Configuration = config.name
		if config.workers > basePlan.EffectiveCPUSlots {
			results[i].Skipped = fmt.Sprintf("requires %d effective CPU slots; available %d", config.workers, basePlan.EffectiveCPUSlots)
			continue
		}
		overrides := libraryindex.ResourceOverrides{
			Mode: config.mode, Workers: config.workers, DecodeWorkers: config.decode, DSPWorkers: config.dsp,
			IOWorkers: config.io, InferenceWorkers: 1, InferenceThreads: 1, IOProfile: basePlan.IOProfile,
			BufferingMode: config.buffering, MaxRAM: basePlan.MaxRAM, MaxOpenFiles: basePlan.MaxOpenFiles, QueueDepth: basePlan.QueueDepth,
		}
		plan, err := libraryindex.ResolveResourcePlan(overrides)
		if err != nil {
			results[i].Skipped = err.Error()
			continue
		}
		results[i].Resources = plan
		results[i].Trials = make([]benchmarkResult, 0, trials)
	}
	for trial := 0; trial < trials; trial++ {
		for _, i := range pipelineTrialOrder(len(configs), trial, trials) {
			config := configs[i]
			if results[i].Skipped != "" {
				continue
			}
			if gracefulStopRequested(ctx) {
				return 130, libraryindex.ErrShutdownRequested
			}
			trialName := fmt.Sprintf("%s_trial-%d", config.name, trial+1)
			result, err := benchmarkConfiguration(ctx, common, root, paths, profile, modelBundle, device, trialName, results[i].Resources, benchmarkConfigurationOptions{PreReadSources: true, PipelineOnly: true}, stderr)
			if err != nil {
				return 1, fmt.Errorf("benchmark %s: %w", trialName, err)
			}
			results[i].Resources = result.Resources
			results[i].Trials = append(results[i].Trials, result)
		}
	}
	expectedWindows, err := libraryindex.SamplingWindows(300, profile)
	if err != nil {
		return 1, err
	}
	expectedTracks := sampleTracksRequested
	expectedWindowCount := expectedTracks * len(expectedWindows)
	referenceDigest := ""
	for i := range results {
		for _, trial := range results[i].Trials {
			if len(pipelineTrialIneligibility(trial, expectedTracks, expectedWindowCount)) == 0 {
				referenceDigest = trial.SemanticDigest
				break
			}
		}
		if referenceDigest != "" {
			break
		}
	}
	for i := range results {
		walls := make([]time.Duration, 0, len(results[i].Trials))
		throughputs := make([]float64, 0, len(results[i].Trials))
		results[i].Eligible = len(results[i].Trials) == trials
		results[i].Equivalent = results[i].Eligible && referenceDigest != ""
		for trialIndex, trial := range results[i].Trials {
			walls = append(walls, trial.WarmAnalysis)
			throughputs = append(throughputs, trial.TracksPerSecond)
			for _, reason := range pipelineTrialIneligibility(trial, expectedTracks, expectedWindowCount) {
				results[i].Ineligible = append(results[i].Ineligible, fmt.Sprintf("trial %d: %s", trialIndex+1, reason))
			}
			results[i].Eligible = results[i].Eligible && len(pipelineTrialIneligibility(trial, expectedTracks, expectedWindowCount)) == 0
			results[i].Equivalent = results[i].Equivalent && trial.SemanticDigest == referenceDigest
		}
		results[i].Equivalent = results[i].Equivalent && results[i].Eligible
		results[i].MedianWall = durationPercentile(walls, .50)
		results[i].P95Wall = durationPercentile(walls, .95)
		results[i].MedianThroughput = floatPercentile(throughputs, .50)
	}
	selected := selectPipelineMatrixResult(results)
	return 0, json.NewEncoder(stdout).Encode(map[string]any{
		"kind": "real-audio-pipeline-matrix", "sampleSeed": common.seed, "sampleTracksRequested": sampleTracksRequested,
		"sampleTracksFound": len(paths), "trialsPerConfiguration": trials, "host": runtime.GOOS + "/" + runtime.GOARCH,
		"selectionPolicy": "lowest sum of heavy/decode/DSP/source-I/O workers and window buffering before track buffering, among complete one-session CUDA runs with the expected window count and equivalent output within 3% of the fastest median wall time and 5% of its p95",
		"selected":        selected, "results": results,
	})
}

func pipelineTrialOrder(length, trial, trials int) []int {
	if length <= 0 || trials <= 0 {
		return nil
	}
	order := make([]int, length)
	offset := trial * length / trials
	for position := range order {
		if trial%2 == 0 {
			order[position] = (offset + position) % length
		} else {
			order[position] = (offset - position + length) % length
		}
	}
	return order
}

func pipelineTrialIneligibility(result benchmarkResult, expectedTracks, expectedWindows int) []string {
	var reasons []string
	if result.Skipped != "" {
		reasons = append(reasons, result.Skipped)
	}
	if result.Report.MetadataCompleted != int64(expectedTracks) || result.Report.AudioCompleted != int64(expectedTracks) {
		reasons = append(reasons, fmt.Sprintf("completed metadata/audio %d/%d, expected %d/%d", result.Report.MetadataCompleted, result.Report.AudioCompleted, expectedTracks, expectedTracks))
	}
	if result.Report.Failed != 0 || result.Report.SkippedChanged != 0 {
		reasons = append(reasons, fmt.Sprintf("failed/skipped %d/%d", result.Report.Failed, result.Report.SkippedChanged))
	}
	if result.Report.WindowsDecoded != int64(expectedWindows) {
		reasons = append(reasons, fmt.Sprintf("decoded windows %d, expected %d", result.Report.WindowsDecoded, expectedWindows))
	}
	if result.Resources.InferenceWorkers != 1 || result.Resources.InferenceThreads != 1 {
		reasons = append(reasons, fmt.Sprintf("MERT sessions/threads %d/%d, expected 1/1", result.Resources.InferenceWorkers, result.Resources.InferenceThreads))
	}
	if _, cuda := audio.MERTCUDADeviceIndex(result.Host.GPUDevice); !cuda {
		reasons = append(reasons, "MERT device is not CUDA")
	}
	if result.SemanticDigest == "" {
		reasons = append(reasons, "semantic digest is empty")
	}
	return reasons
}

func selectPipelineMatrixResult(results []pipelineMatrixResult) *pipelineMatrixResult {
	fastest := -1
	for i := range results {
		if results[i].Skipped != "" || len(results[i].Trials) == 0 || !results[i].Eligible || !results[i].Equivalent {
			continue
		}
		if fastest < 0 || results[i].MedianWall < results[fastest].MedianWall {
			fastest = i
		}
	}
	if fastest < 0 {
		return nil
	}
	selected := fastest
	for i := range results {
		if results[i].Skipped != "" || len(results[i].Trials) == 0 || !results[i].Eligible || !results[i].Equivalent ||
			float64(results[i].MedianWall) > float64(results[fastest].MedianWall)*1.03 ||
			float64(results[i].P95Wall) > float64(results[fastest].P95Wall)*1.05 {
			continue
		}
		if pipelineResourceLess(results[i].Resources, results[selected].Resources) {
			selected = i
		}
	}
	result := results[selected]
	return &result
}

func pipelineResourceLess(left, right libraryindex.ResourcePlan) bool {
	leftScore := left.HeavyWorkers + left.DecodeWorkers + left.DSPWorkers + left.IOWorkers
	rightScore := right.HeavyWorkers + right.DecodeWorkers + right.DSPWorkers + right.IOWorkers
	if leftScore != rightScore {
		return leftScore < rightScore
	}
	if left.BufferingMode != right.BufferingMode {
		return left.BufferingMode == libraryindex.BufferingWindow
	}
	return left.Summary() < right.Summary()
}

func benchmarkConfiguration(ctx context.Context, common commonFlags, rootPath string, paths []string, profile libraryindex.SamplingProfile, modelBundle, requestedDevice, name string, plan libraryindex.ResourcePlan, options benchmarkConfigurationOptions, stderr io.Writer) (benchmarkResult, error) {
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
	analyzer := &libraryindex.Analyzer{Trace: libraryindex.NewStageTrace(common.stageTraceEvents), State: state, Runtime: codec, MERT: pool, Plan: plan, Admission: libraryindex.NewAdmission(plan, 0), Profile: profile, StopAdmission: gracefulStopFromContext(ctx)}
	defer analyzer.Admission.Close()
	result.Host = readBenchmarkHostDiagnostics(rootPath, pool.Device())
	if options.PreReadSources {
		preReadStarted := time.Now()
		result.SourcePreReadBytes, err = preReadBenchmarkSources(ctx, paths)
		result.SourcePreRead = time.Since(preReadStarted)
		if err != nil {
			return result, err
		}
	}
	result.ARCBefore = readBenchmarkARCSnapshot()
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
	monitorStopped := false
	stopMonitor := func() {
		if monitorStopped {
			return
		}
		monitorStopped = true
		close(monitorDone)
		result.PeakOwnedRSSBytes = <-peakRSS
		result.RAMTargetMet = result.PeakOwnedRSSBytes <= plan.MaxRAM
	}
	defer stopMonitor()
	result.Report, err = analyzer.Run(ctx, libraryindex.AnalysisOptions{Metadata: true, Audio: true, Profile: profile, Integrity: libraryindex.IntegrityFull}, done)
	result.ARCAfter = readBenchmarkARCSnapshot()
	result.ARCDelta = benchmarkARCSnapshot{
		SizeBytes: result.ARCAfter.SizeBytes - result.ARCBefore.SizeBytes,
		Hits:      result.ARCAfter.Hits - result.ARCBefore.Hits,
		Misses:    result.ARCAfter.Misses - result.ARCBefore.Misses,
	}
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
	if options.PipelineOnly {
		stopMonitor()
		return result, err
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
	stopMonitor()
	return result, err
}

func preReadBenchmarkSources(ctx context.Context, paths []string) (int64, error) {
	buffer := make([]byte, 1<<20)
	defer clear(buffer)
	var total int64
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		file, err := os.Open(path)
		if err != nil {
			return total, err
		}
		for {
			if err := ctx.Err(); err != nil {
				_ = file.Close()
				return total, err
			}
			read, readErr := file.Read(buffer)
			total += int64(read)
			if readErr != nil {
				closeErr := file.Close()
				if errors.Is(readErr, io.EOF) {
					if closeErr != nil {
						return total, closeErr
					}
					break
				}
				return total, errors.Join(readErr, closeErr)
			}
		}
	}
	return total, nil
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

func floatPercentile(values []float64, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]float64(nil), values...)
	slices.Sort(ordered)
	index := int(math.Ceil(percentile*float64(len(ordered)))) - 1
	index = max(0, min(index, len(ordered)-1))
	return ordered[index]
}
