package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/libraryindex"
)

const gpuMatrixTrials = 3

type gpuMatrixResult struct {
	pipelineMatrixResult
	P95TrackLatency time.Duration `json:"p95TrackLatency"`
}

func gpuMatrixConfigurations(base libraryindex.ResourcePlan) []concurrencyBenchmarkConfig {
	configs := []concurrencyBenchmarkConfig{{name: "serial", mode: libraryindex.ConcurrencySerial, workers: 1, decode: 1, dsp: 1, io: 1, sessions: 1, threads: 1}}
	for _, decode := range []int{4, 8} {
		for _, dsp := range []int{2, 4} {
			for _, sessions := range []int{1, 2} {
				configs = append(configs, concurrencyBenchmarkConfig{name: fmt.Sprintf("decode-%d_dsp-%d_cuda-%d", decode, dsp, sessions), mode: libraryindex.ConcurrencyManual, workers: base.HeavyWorkers, decode: decode, dsp: dsp, io: base.IOWorkers, sessions: sessions, threads: 1, buffering: base.BufferingMode})
			}
		}
	}
	return configs
}

func runGPUMatrixBenchmark(ctx context.Context, common commonFlags, root string, paths []string, requested int, profile libraryindex.SamplingProfile, modelBundle, device string, base libraryindex.ResourcePlan, stdout, stderr io.Writer) (int, error) {
	configs := gpuMatrixConfigurations(base)
	results := make([]gpuMatrixResult, len(configs))
	for i, config := range configs {
		results[i].Configuration = config.name
		plan, err := libraryindex.ResolveResourcePlan(libraryindex.ResourceOverrides{
			Mode: config.mode, Workers: config.workers, DecodeWorkers: config.decode, DSPWorkers: config.dsp,
			IOWorkers: config.io, InferenceWorkers: config.sessions, InferenceThreads: 1,
			IOProfile: base.IOProfile, BufferingMode: config.buffering, MaxRAM: base.MaxRAM,
			MaxOpenFiles: base.MaxOpenFiles, QueueDepth: base.QueueDepth,
		})
		if err != nil {
			results[i].Skipped = err.Error()
			continue
		}
		results[i].Resources = plan
	}
	for trial := 0; trial < gpuMatrixTrials; trial++ {
		for _, i := range pipelineTrialOrder(len(configs), trial, gpuMatrixTrials) {
			if results[i].Skipped != "" {
				continue
			}
			if gracefulStopRequested(ctx) {
				return 130, libraryindex.ErrShutdownRequested
			}
			fmt.Fprintf(stderr, "GPU matrix trial %d/%d: %s\n", trial+1, gpuMatrixTrials, configs[i].name)
			result, err := benchmarkConfiguration(ctx, common, root, paths, profile, modelBundle, device, fmt.Sprintf("%s_trial-%d", configs[i].name, trial+1), results[i].Resources, benchmarkConfigurationOptions{PreReadSources: true, PipelineOnly: true}, stderr)
			if err != nil {
				return 1, fmt.Errorf("benchmark %s trial %d: %w", configs[i].name, trial+1, err)
			}
			results[i].Trials = append(results[i].Trials, result)
			fmt.Fprintf(stderr, "GPU matrix completed %s trial %d: %.3f tracks/s, %d failed\n", configs[i].name, trial+1, result.TracksPerSecond, result.Report.Failed)
		}
	}
	reference := ""
	if len(results[0].Trials) > 0 {
		reference = results[0].Trials[0].SemanticDigest
	}
	for i := range results {
		summarizeGPUResult(&results[i], len(paths), configs[i].sessions, reference)
		if len(results[0].Trials) == 0 {
			continue
		}
		for trial, result := range results[i].Trials {
			if !gpuCoverageEquivalent(result.Report, results[0].Trials[0].Report) {
				results[i].Eligible = false
				results[i].Ineligible = append(results[i].Ineligible, fmt.Sprintf("trial %d: sampled window coverage differs from serial baseline", trial+1))
			}
		}
	}
	selected := selectGPUMatrixResult(results)
	return 0, json.NewEncoder(stdout).Encode(map[string]any{
		"kind": "real-audio-gpu-matrix", "sampleSeed": common.seed, "sampleTracksRequested": requested,
		"sampleTracksFound": len(paths), "trialsPerConfiguration": gpuMatrixTrials,
		"host": runtime.GOOS + "/" + runtime.GOARCH, "integrity": "full", "results": results, "selected": selected,
		"selectionPolicy": "serial-equivalent complete CUDA trials within RAM target; retain baseline unless median throughput improves at least 10% in all three paired trials with no greater than 5% track p95 regression; two sessions must also meet those guards against their matched one-session configuration",
		"notes":           []string{"recommendation only; no defaults or user state are changed", "same deterministic source paths and full integrity in isolated scratch state per trial", "source pre-read precedes timed analysis; order rotates and reverses", "p95 track latency includes metadata and audio service time; ONNX execution is not GPU occupancy", "GPU device-memory and presentation telemetry require separate host capture"},
	})
}

func summarizeGPUResult(result *gpuMatrixResult, tracks, sessions int, reference string) {
	result.Eligible = result.Skipped == "" && len(result.Trials) == gpuMatrixTrials
	result.Equivalent = reference != ""
	var walls, latencies []time.Duration
	var rates []float64
	for i := range result.Trials {
		trial := &result.Trials[i]
		equivalent := reference != "" && trial.SemanticDigest == reference
		trial.SerialEquivalent = &equivalent
		result.Equivalent = result.Equivalent && equivalent
		reasons := gpuTrialIneligibility(*trial, tracks, sessions)
		if !equivalent {
			reasons = append(reasons, "semantic digest differs from serial baseline")
		}
		for _, reason := range reasons {
			result.Ineligible = append(result.Ineligible, fmt.Sprintf("trial %d: %s", i+1, reason))
		}
		result.Eligible = result.Eligible && len(reasons) == 0
		walls = append(walls, trial.WarmAnalysis)
		rates = append(rates, trial.TracksPerSecond)
		for _, track := range trial.Report.TrackTimings {
			latencies = append(latencies, track.Total)
		}
	}
	result.MedianWall = durationPercentile(walls, .5)
	result.P95Wall = durationPercentile(walls, .95)
	result.P95TrackLatency = durationPercentile(latencies, .95)
	result.MedianThroughput = floatPercentile(rates, .5)
}

func gpuTrialIneligibility(result benchmarkResult, tracks, sessions int) []string {
	var reasons []string
	if result.Skipped != "" {
		reasons = append(reasons, result.Skipped)
	}
	if result.Report.MetadataCompleted != int64(tracks) || result.Report.AudioCompleted != int64(tracks) {
		reasons = append(reasons, "incomplete metadata/audio coverage")
	}
	if result.Report.Failed != 0 || result.Report.SkippedChanged != 0 {
		reasons = append(reasons, "failed or changed source tracks")
	}
	if result.Resources.InferenceWorkers != sessions || result.Resources.InferenceThreads != 1 {
		reasons = append(reasons, "resolved sessions/threads differ from matrix point")
	}
	if _, cuda := audio.MERTCUDADeviceIndex(result.Host.GPUDevice); !cuda {
		reasons = append(reasons, "MERT device is not CUDA")
	}
	if !result.RAMTargetMet {
		reasons = append(reasons, "measured owned memory exceeds admission target")
	}
	if result.SemanticDigest == "" {
		reasons = append(reasons, "semantic digest is empty")
	}
	if len(result.Report.TrackTimings) != tracks {
		reasons = append(reasons, "incomplete track latency evidence")
	}
	return reasons
}

func gpuImprovement(candidate, baseline gpuMatrixResult) bool {
	if !candidate.Eligible || !candidate.Equivalent || !baseline.Eligible || !baseline.Equivalent || baseline.MedianThroughput <= 0 || baseline.P95TrackLatency <= 0 ||
		candidate.MedianThroughput < baseline.MedianThroughput*1.10 || float64(candidate.P95TrackLatency) > float64(baseline.P95TrackLatency)*1.05 || len(candidate.Trials) != gpuMatrixTrials || len(baseline.Trials) != gpuMatrixTrials {
		return false
	}
	for i, trial := range candidate.Trials {
		if trial.TracksPerSecond < baseline.Trials[i].TracksPerSecond*1.10 {
			return false
		}
	}
	return true
}

func selectGPUMatrixResult(results []gpuMatrixResult) *gpuMatrixResult {
	if len(results) < 2 || !results[0].Eligible || !results[0].Equivalent {
		return nil
	}
	// The smallest one-session matrix point is the tuning baseline. Serial is
	// the correctness reference, not a justification for extra CUDA residency.
	baseline := 1
	if !results[baseline].Eligible || !results[baseline].Equivalent {
		return nil
	}
	selected := baseline
	for i := baseline + 1; i < len(results); i++ {
		if !gpuImprovement(results[i], results[baseline]) {
			continue
		}
		if results[i].Resources.InferenceWorkers == 2 {
			matched := -1
			for j := 1; j < len(results); j++ {
				if results[j].Resources.InferenceWorkers == 1 && results[j].Resources.DecodeWorkers == results[i].Resources.DecodeWorkers && results[j].Resources.DSPWorkers == results[i].Resources.DSPWorkers {
					matched = j
					break
				}
			}
			if matched < 0 || !gpuImprovement(results[i], results[matched]) {
				continue
			}
		}
		if results[i].MedianThroughput > results[selected].MedianThroughput {
			selected = i
		}
	}
	result := results[selected]
	return &result
}

func gpuCoverageEquivalent(candidate, reference libraryindex.AnalysisReport) bool {
	if candidate.WindowsDecoded != reference.WindowsDecoded || len(candidate.TrackTimings) != len(reference.TrackTimings) {
		return false
	}
	windows := make(map[string]int, len(reference.TrackTimings))
	for _, track := range reference.TrackTimings {
		windows[track.FileID] = track.Windows
	}
	for _, track := range candidate.TrackTimings {
		count, ok := windows[track.FileID]
		if !ok || count != track.Windows {
			return false
		}
		delete(windows, track.FileID)
	}
	return len(windows) == 0
}
