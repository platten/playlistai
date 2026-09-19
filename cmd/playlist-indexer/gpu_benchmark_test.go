package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/libraryindex"
)

func TestGPUMatrixContainsBoundedCartesianProductAndSerial(t *testing.T) {
	configs := gpuMatrixConfigurations(libraryindex.ResourcePlan{HeavyWorkers: 8, IOWorkers: 2})
	if len(configs) != 9 || configs[0].mode != libraryindex.ConcurrencySerial {
		t.Fatalf("configs=%+v", configs)
	}
	seen := make(map[[3]int]bool)
	for _, config := range configs[1:] {
		seen[[3]int{config.decode, config.dsp, config.sessions}] = true
	}
	for _, decode := range []int{4, 8} {
		for _, dsp := range []int{2, 4} {
			for _, sessions := range []int{1, 2} {
				if !seen[[3]int{decode, dsp, sessions}] {
					t.Fatalf("missing %d/%d/%d", decode, dsp, sessions)
				}
			}
		}
	}
}

func TestGPUMatrixRejectsUnboundedTrialsAndCPU(t *testing.T) {
	for _, extra := range [][]string{{"--trials", "2"}, {"--trials", "4"}, {"--device", "cpu"}} {
		args := append([]string{"bench", "concurrency", "--matrix", "gpu", "--root", t.TempDir(), "--sample-tracks", "1"}, extra...)
		if code, err := execute(context.Background(), args, io.Discard, io.Discard); code == 0 || err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
}

func gpuTestResult(name string, decode, dsp, sessions int, rate float64) gpuMatrixResult {
	result := gpuMatrixResult{pipelineMatrixResult: pipelineMatrixResult{Configuration: name, Resources: libraryindex.ResourcePlan{DecodeWorkers: decode, DSPWorkers: dsp, InferenceWorkers: sessions}, Eligible: true, Equivalent: true, MedianThroughput: rate}, P95TrackLatency: time.Second}
	for i := 0; i < gpuMatrixTrials; i++ {
		result.Trials = append(result.Trials, benchmarkResult{TracksPerSecond: rate})
	}
	return result
}

func TestGPUSelectionRequiresRepeatableGainAndMatchedSingleSession(t *testing.T) {
	serial := gpuTestResult("serial", 1, 1, 1, 1)
	baseline := gpuTestResult("baseline", 4, 2, 1, 10)
	dual := gpuTestResult("dual", 4, 2, 2, 10.9)
	results := []gpuMatrixResult{serial, baseline, dual}
	if got := selectGPUMatrixResult(results); got.Configuration != "baseline" {
		t.Fatalf("adopted sub-threshold dual session: %+v", got)
	}
	results[2] = gpuTestResult("dual", 4, 2, 2, 11.1)
	if got := selectGPUMatrixResult(results); got.Configuration != "dual" {
		t.Fatalf("rejected repeated gain: %+v", got)
	}
	results[2].Trials[1].TracksPerSecond = 10.5
	if got := selectGPUMatrixResult(results); got.Configuration != "baseline" {
		t.Fatalf("adopted inconsistent gain: %+v", got)
	}
	results[2] = gpuTestResult("dual", 4, 2, 2, 11.1)
	results[2].P95TrackLatency = 1060 * time.Millisecond
	if got := selectGPUMatrixResult(results); got.Configuration != "baseline" {
		t.Fatalf("adopted tail regression: %+v", got)
	}
	results[2] = gpuTestResult("dual", 8, 4, 2, 15)
	if got := selectGPUMatrixResult(results); got.Configuration != "baseline" {
		t.Fatalf("adopted dual without matched evidence: %+v", got)
	}
}

func TestGPUTrialEligibilityRejectsMissingCoverageAndMemory(t *testing.T) {
	result := benchmarkResult{Resources: libraryindex.ResourcePlan{InferenceWorkers: 2, InferenceThreads: 1}, Host: benchmarkHostDiagnostics{GPUDevice: "cuda:0"}, SemanticDigest: "same", RAMTargetMet: true,
		Report: libraryindex.AnalysisReport{MetadataCompleted: 1, AudioCompleted: 1, TrackTimings: []libraryindex.TrackAnalysisTiming{{FileID: "a", Total: time.Second}}}}
	if reasons := gpuTrialIneligibility(result, 1, 2); len(reasons) != 0 {
		t.Fatal(reasons)
	}
	result.RAMTargetMet = false
	result.Report.AudioCompleted = 0
	result.Resources.InferenceWorkers = 1
	if reasons := gpuTrialIneligibility(result, 1, 2); len(reasons) != 3 {
		t.Fatalf("missing guard: %v", reasons)
	}
}

func TestGPUCoverageUsesActualSerialWindowsAndTrackIdentity(t *testing.T) {
	baseline := libraryindex.AnalysisReport{WindowsDecoded: 3, TrackTimings: []libraryindex.TrackAnalysisTiming{{FileID: "short", Windows: 1}, {FileID: "long", Windows: 2}}}
	candidate := libraryindex.AnalysisReport{WindowsDecoded: 3, TrackTimings: []libraryindex.TrackAnalysisTiming{{FileID: "long", Windows: 2}, {FileID: "short", Windows: 1}}}
	if !gpuCoverageEquivalent(candidate, baseline) {
		t.Fatal("completion order changed coverage")
	}
	candidate.TrackTimings[0].Windows = 1
	candidate.TrackTimings[1].Windows = 2
	if gpuCoverageEquivalent(candidate, baseline) {
		t.Fatal("aggregate count hid changed per-track coverage")
	}
}

func TestStageTraceFlagRejectsOutOfBounds(t *testing.T) {
	for _, value := range []string{"-1", "100001"} {
		args := []string{"status", "--stage-trace-events", value, "--state", t.TempDir()}
		if code, err := execute(context.Background(), args, io.Discard, io.Discard); code == 0 || err == nil {
			t.Fatalf("invalid trace bound accepted: %s", value)
		}
	}
}

func TestGPUMatrixAcceptsCUDAAliasesBeforeSourceSelection(t *testing.T) {
	for _, device := range []string{"auto", "cuda", "cuda:0", "CUDA:1"} {
		_, err := execute(context.Background(), []string{"bench", "concurrency", "--matrix", "gpu", "--root", t.TempDir(), "--sample-tracks", "1", "--device", device}, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "no supported audio files") {
			t.Fatalf("device %q: %v", device, err)
		}
	}
}
