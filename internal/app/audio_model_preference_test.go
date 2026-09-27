package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/audioruntime"
)

func TestMain(m *testing.M) {
	if os.Getenv("PLAYLISTAI_TEST_CUDA_INSTALL") == "1" && len(os.Args) == 3 {
		var err error
		switch os.Args[1] {
		case "--audio-worker":
			err = audioruntime.Run(os.Args[2])
		case "--mert-worker":
			err = audioruntime.RunMERT(os.Args[2])
		default:
			os.Exit(m.Run())
		}
		if err != nil {
			_, _ = os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestInvalidCUDAIndexerCacheFallsBackToCPU(t *testing.T) {
	t.Setenv("PLAYLIST_INDEXER_OFFLINE_CACHE_DIR", t.TempDir())
	if _, _, ok := preferredCUDACLAP(); ok {
		t.Fatal("missing CUDA CLAP bundle was selected")
	}
	if _, _, ok := preferredCUDAMERT(); ok {
		t.Fatal("missing CUDA MERT bundle was selected")
	}
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	status, err := c.GetAnalysisStatus(context.Background())
	if err != nil || status.RecommendedManifest != "" && filepath.IsAbs(status.RecommendedManifest) {
		t.Fatalf("CLAP fallback: %+v, %v", status, err)
	}
	enhanced, err := c.GetEnhancedAnalysisStatus(context.Background())
	if err != nil || enhanced.RecommendedManifestURL != "" && filepath.IsAbs(enhanced.RecommendedManifestURL) {
		t.Fatalf("MERT fallback: %+v, %v", enhanced, err)
	}
}

func TestHostedCUDACLAPPreferredWithCPUFallback(t *testing.T) {
	if !audio.NativeInferenceAvailable() {
		t.Skip("native CLAP inference unavailable")
	}
	t.Setenv("PLAYLIST_INDEXER_OFFLINE_CACHE_DIR", t.TempDir())
	c := &Container{}
	cpu, err := c.recommendedCLAPFor(false)
	if err != nil || cpu.offer.Backend != "cpu" || !strings.HasSuffix(cpu.source, "/clap-"+runtime.GOOS+"-"+runtime.GOARCH+"/manifest.json") {
		t.Fatalf("CPU fallback recommendation: %+v %v", cpu, err)
	}
	gpu, err := c.recommendedCLAPFor(true)
	if runtime.GOARCH != "amd64" || runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		if err != nil || gpu.offer.Backend != "cpu" {
			t.Fatalf("unsupported CUDA platform selected GPU: %+v %v", gpu, err)
		}
		return
	}
	wantID := "custom-clap-cpu-v2-cuda-v1"
	if runtime.GOOS == "windows" {
		wantID = "custom-clap-cuda-v2"
	}
	if err != nil || gpu.offer.Backend != "cuda" || !strings.HasSuffix(gpu.source, "/clap-"+runtime.GOOS+"-amd64-gpu/manifest.json") || gpu.distribution == nil || gpu.offer.DownloadBytes != gpu.distribution.DownloadBytes || gpu.expected.ID != wantID || gpu.expected.Model.Weights != cpu.expected.Model.Weights || gpu.expected.Model.Runtime != "onnxruntime/1.26.0/cuda" {
		t.Fatalf("hosted CUDA recommendation: %+v %v", gpu, err)
	}
}

func TestHostedCUDAMERTPreferredWithCPUFallback(t *testing.T) {
	if !audio.NativeInferenceAvailable() {
		t.Skip("native MERT inference unavailable")
	}
	t.Setenv("PLAYLIST_INDEXER_OFFLINE_CACHE_DIR", t.TempDir())
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cpu, err := c.recommendedMERTFor(false)
	if err != nil || !strings.HasSuffix(cpu.URL, "/mert-"+runtime.GOOS+"-"+runtime.GOARCH+"/manifest.json") {
		t.Fatalf("CPU fallback recommendation: %+v %v", cpu, err)
	}
	gpu, err := c.recommendedMERTFor(true)
	if runtime.GOARCH != "amd64" || runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		if err != nil || gpu != cpu {
			t.Fatalf("unsupported CUDA platform selected GPU: %+v %v", gpu, err)
		}
		return
	}
	if err != nil || !strings.HasSuffix(gpu.URL, "/mert-"+runtime.GOOS+"-amd64-gpu/manifest.json") || gpu.SHA256 == "" || gpu.DownloadBytes <= cpu.DownloadBytes {
		t.Fatalf("hosted CUDA recommendation: %+v %v", gpu, err)
	}
	if !audio.MERTCUDAHostAvailable() {
		return
	}
	installedCPU := audio.MERTBundleManifest{}
	installedCPU.Model.Runtime = "onnxruntime/1.26.0/cpu"
	c.enhanced.manifest = &installedCPU
	status, err := c.GetEnhancedAnalysisStatus(context.Background())
	if err != nil || status.RecommendedManifestURL != gpu.URL || !status.RecommendedUpgrade {
		t.Fatalf("hosted CUDA upgrade not offered: %+v %v", status, err)
	}
	installedCUDA := installedCPU
	installedCUDA.Model.Runtime = "onnxruntime/1.26.0/cuda"
	c.enhanced.manifest = &installedCUDA
	status, err = c.GetEnhancedAnalysisStatus(context.Background())
	if err != nil || status.RecommendedUpgrade {
		t.Fatalf("installed CUDA MERT offered duplicate upgrade: %+v %v", status, err)
	}
}

func TestPreferredCUDAIndexerCacheOptIn(t *testing.T) {
	root := os.Getenv("PLAYLISTAI_TEST_CUDA_CACHE")
	if root == "" {
		t.Skip("set PLAYLISTAI_TEST_CUDA_CACHE to a verified offline indexer cache")
	}
	t.Setenv("PLAYLIST_INDEXER_OFFLINE_CACHE_DIR", root)
	if !audio.NativeInferenceAvailable() || !audio.MERTCUDAHostAvailable() {
		t.Skip("native CUDA worker unavailable on this host")
	}
	clapDir, clap, clapOK := preferredCUDACLAP()
	mertDir, mert, mertOK := preferredCUDAMERT()
	if !clapOK || !mertOK || clap.Backend() != "cuda" || mert.Backend() != "cuda" {
		t.Fatalf("verified CUDA bundles not selected: CLAP=%q %t, MERT=%q %t", clapDir, clapOK, mertDir, mertOK)
	}
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	status, err := c.GetAnalysisStatus(context.Background())
	if err != nil || status.RecommendedManifest != clapDir || !status.RecommendedAvailable {
		t.Fatalf("CLAP status: %+v, %v", status, err)
	}
	cpuCLAP := clap
	cpuCLAP.Model.Runtime = "onnxruntime/1.26.0/cpu"
	c.analysis.manifest = &cpuCLAP
	status, err = c.GetAnalysisStatus(context.Background())
	if err != nil || status.RecommendedInstalled {
		t.Fatalf("CPU CLAP was mistaken for the recommended CUDA model: %+v, %v", status, err)
	}
	c.analysis.manifest = nil
	enhanced, err := c.GetEnhancedAnalysisStatus(context.Background())
	if err != nil || enhanced.RecommendedManifestURL != mertDir {
		t.Fatalf("MERT status: %+v, %v", enhanced, err)
	}
	cpu := mert
	cpu.Model.Runtime = "onnxruntime/1.26.0/cpu"
	c.enhanced.manifest = &cpu
	enhanced, err = c.GetEnhancedAnalysisStatus(context.Background())
	if err != nil || !enhanced.RecommendedUpgrade {
		t.Fatalf("CPU MERT upgrade was not offered: %+v, %v", enhanced, err)
	}
	c.enhanced.manifest = nil
	if os.Getenv("PLAYLISTAI_TEST_CUDA_INSTALL") != "1" {
		return
	}
	if err := c.InstallRecommendedAnalysisBundle(context.Background(), nil); err != nil {
		t.Fatalf("install CUDA CLAP: %v", err)
	}
	status, err = c.GetAnalysisStatus(context.Background())
	if err != nil || !status.Available || !status.RecommendedInstalled || c.analysis.worker.EffectiveDevice() != "cuda:0" {
		t.Fatalf("installed CLAP: %+v, %v", status, err)
	}
	if err := c.InstallRecommendedMERT(context.Background(), nil); err != nil {
		t.Fatalf("install CUDA MERT: %v", err)
	}
	enhanced, err = c.GetEnhancedAnalysisStatus(context.Background())
	if err != nil || !enhanced.MERTAvailable || enhanced.RecommendedUpgrade || c.enhanced.worker.EffectiveDevice() != "cuda:0" {
		t.Fatalf("installed MERT: %+v, %v", enhanced, err)
	}
}
