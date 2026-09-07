package audio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func fixtureBundle(base string) BundleManifest {
	data := []byte("synthetic-bundle-control-fixture")
	hash := sha256.Sum256(data)
	m := BundleManifest{Version: 1, ID: "fixture", Platform: runtime.GOOS + "/" + runtime.GOARCH, Model: core.AudioModelIdentity{Model: "laion/larger_clap_music", Revision: "fixture", Preprocessing: PreprocessingVersion, Runtime: "onnxruntime/1.26.0/cpu", Dimension: 512}, MemoryBytes: 1, License: "fixture", SourceURL: "https://example.invalid", Policy: Policy{Version: "fixture", DevelopmentSet: "synthetic-controls", MinimumPositive: 0.6, MaximumNegative: 0.2}, Parity: ParityReport{ReferenceRevision: "fixture", Fixtures: 3, MinimumCosine: 1, TokenizerCases: 6, TokenizerExact: true, PreprocessingCases: 3, PreprocessingWithinTolerance: true}}
	for _, role := range []string{"worker", "runtime", "audio_model", "text_model", "vocabulary", "merges", "preprocessing", "license", "health"} {
		m.Artifacts = append(m.Artifacts, BundleArtifact{Role: role, Name: role + ".fixture", URL: base + "/" + role, Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:])})
	}
	return m
}

func TestBundleGatesRejectUnvalidatedOrIncompatibleArtifacts(t *testing.T) {
	m := fixtureBundle("https://example.invalid")
	m.Parity.ReferenceRevision = "different-model"
	if m.Validate() == nil {
		t.Fatal("another model's parity report was accepted")
	}
	for _, change := range []func(*BundleManifest){func(m *BundleManifest) { m.Parity.TokenizerExact = false }, func(m *BundleManifest) { m.Policy.DevelopmentSet = "" }, func(m *BundleManifest) { m.Artifacts[0].Name = "../worker" }, func(m *BundleManifest) { m.Artifacts[0].SHA256 = "" }, func(m *BundleManifest) { m.Platform = "unsupported" }, func(m *BundleManifest) { m.Model.Runtime = "onnxruntime/gpu" }} {
		m := fixtureBundle("https://example.invalid")
		change(&m)
		if m.Validate() == nil {
			t.Fatal("bundle gate bypassed")
		}
	}
}

func TestInterruptedBundleResumesAndHealthFailureKeepsActive(t *testing.T) {
	data := []byte("synthetic-bundle-control-fixture")
	requests := 0
	resumed := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			_, _ = w.Write(data[:8])
			return
		}
		if r.Header.Get("Range") == "bytes=8-" {
			resumed = true
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 8-%d/%d", len(data)-1, len(data)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(data[8:])
			return
		}
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	previousClient := http.DefaultClient
	http.DefaultClient = srv.Client()
	defer func() { http.DefaultClient = previousClient }()
	m := fixtureBundle(srv.URL)
	dir := t.TempDir()
	b := &BundleManager{Directory: dir, healthCheck: func(context.Context, string, BundleManifest) error { return errors.New("fixture unhealthy") }}
	if err := os.WriteFile(filepath.Join(dir, "active.json"), []byte("previous-version"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Install(context.Background(), m, nil); err == nil {
		t.Fatal("interrupted download activated")
	}
	if _, err := b.Install(context.Background(), m, nil); err == nil {
		t.Fatal("unhealthy bundle activated")
	}
	if !resumed {
		t.Fatal("interrupted artifact was not resumed")
	}
	active, _ := os.ReadFile(filepath.Join(dir, "active.json"))
	if string(active) != "previous-version" {
		t.Fatal("failed installation replaced active model")
	}
	b.healthCheck = func(context.Context, string, BundleManifest) error { return nil }
	before := requests
	if _, err := b.Install(context.Background(), m, nil); err != nil {
		t.Fatal(err)
	}
	if requests != before {
		t.Fatal("verified files downloaded again")
	}
	active, _ = os.ReadFile(filepath.Join(dir, "active.json"))
	if !strings.HasPrefix(string(active), "fixture-") {
		t.Fatal("healthy bundle not activated")
	}
}
