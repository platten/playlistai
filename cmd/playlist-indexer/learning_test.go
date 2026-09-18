package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/libraryindex"
)

func TestSelectPipelineMatrixResultUsesPerformanceGuardrailsThenResources(t *testing.T) {
	trial := func(duration time.Duration) []benchmarkResult {
		return []benchmarkResult{{WarmAnalysis: duration, SemanticDigest: "same"}}
	}
	results := []pipelineMatrixResult{
		{Configuration: "fast", Resources: libraryindex.ResourcePlan{HeavyWorkers: 11, DecodeWorkers: 10, DSPWorkers: 3, IOWorkers: 2, BufferingMode: libraryindex.BufferingTrack}, Trials: trial(100 * time.Second), MedianWall: 100 * time.Second, P95Wall: 110 * time.Second, Equivalent: true, Eligible: true},
		{Configuration: "efficient", Resources: libraryindex.ResourcePlan{HeavyWorkers: 8, DecodeWorkers: 4, DSPWorkers: 1, IOWorkers: 1, BufferingMode: libraryindex.BufferingWindow}, Trials: trial(102 * time.Second), MedianWall: 102 * time.Second, P95Wall: 115 * time.Second, Equivalent: true, Eligible: true},
		{Configuration: "bad-tail", Resources: libraryindex.ResourcePlan{HeavyWorkers: 7, DecodeWorkers: 3, DSPWorkers: 1, IOWorkers: 1, BufferingMode: libraryindex.BufferingWindow}, Trials: trial(101 * time.Second), MedianWall: 101 * time.Second, P95Wall: 116 * time.Second, Equivalent: true, Eligible: true},
	}
	selected := selectPipelineMatrixResult(results)
	if selected == nil || selected.Configuration != "efficient" {
		t.Fatalf("selected=%+v", selected)
	}
}

func TestPipelineMatrixEligibilityRequiresCompleteCUDAAnalysis(t *testing.T) {
	complete := benchmarkResult{
		Resources:      libraryindex.ResourcePlan{InferenceWorkers: 1, InferenceThreads: 1},
		Host:           benchmarkHostDiagnostics{GPUDevice: "cuda:0"},
		SemanticDigest: "digest",
		Report: libraryindex.AnalysisReport{
			MetadataCompleted: 2,
			AudioCompleted:    2,
			WindowsDecoded:    12,
		},
	}
	if reasons := pipelineTrialIneligibility(complete, 2, 12); len(reasons) != 0 {
		t.Fatalf("complete CUDA trial rejected: %v", reasons)
	}
	complete.Report.Failed = 1
	complete.Report.WindowsDecoded = 11
	complete.Host.GPUDevice = "cpu"
	if reasons := pipelineTrialIneligibility(complete, 2, 12); len(reasons) < 3 {
		t.Fatalf("broken trial was not fully rejected: %v", reasons)
	}
}

func TestPipelineTrialOrderRotatesAndReversesWithoutDuplicates(t *testing.T) {
	first := pipelineTrialOrder(12, 0, 3)
	second := pipelineTrialOrder(12, 1, 3)
	third := pipelineTrialOrder(12, 2, 3)
	if first[0] == second[0] || second[0] == third[0] || first[0] == third[0] {
		t.Fatalf("trial starts did not rotate: %v %v %v", first[0], second[0], third[0])
	}
	for trial, order := range [][]int{first, second, third} {
		seen := make(map[int]bool, len(order))
		for _, index := range order {
			seen[index] = true
		}
		if len(seen) != len(order) {
			t.Fatalf("trial %d order is not a permutation: %v", trial, order)
		}
	}
}

func TestPreReadBenchmarkSourcesCountsBytesAndCancels(t *testing.T) {
	directory := t.TempDir()
	paths := []string{filepath.Join(directory, "first.flac"), filepath.Join(directory, "second.mp3")}
	for index, contents := range []string{"abc", "12345"} {
		if err := os.WriteFile(paths[index], []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if bytes, err := preReadBenchmarkSources(context.Background(), paths); err != nil || bytes != 8 {
		t.Fatalf("pre-read bytes=%d err=%v", bytes, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := preReadBenchmarkSources(ctx, paths); err == nil {
		t.Fatal("canceled pre-read succeeded")
	}
}

func TestPipelineMatrixRequiresKnownNameAndThreeTrials(t *testing.T) {
	for _, args := range [][]string{
		{"bench", "concurrency", "--matrix", "unknown", "--root", t.TempDir(), "--sample-tracks", "1"},
		{"bench", "concurrency", "--matrix", "pipeline", "--trials", "2", "--root", t.TempDir(), "--sample-tracks", "1"},
	} {
		if code, err := execute(context.Background(), args, io.Discard, io.Discard); code == 0 || err == nil {
			t.Fatalf("invalid matrix arguments accepted: code=%d err=%v", code, err)
		}
	}
}
