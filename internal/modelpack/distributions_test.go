package modelpack

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecommendedDistributionsSelectOnlyShippedPlatforms(t *testing.T) {
	t.Parallel()
	for _, platform := range []string{"windows/amd64", "windows/arm64", "linux/amd64", "linux/arm64", "darwin/arm64"} {
		parts := strings.Split(platform, "/")
		d, err := RecommendedMERT(parts[0], parts[1])
		if err != nil || !strings.HasSuffix(d.URL, "/mert-"+parts[0]+"-"+parts[1]+"/manifest.json") || !validHash(d.SHA256) || d.DownloadBytes <= 0 {
			t.Fatalf("wrong platform selection: %+v %v", d, err)
		}
	}
	if _, err := RecommendedMERT("darwin", "amd64"); err == nil {
		t.Fatal("unshipped architecture accepted")
	}
	if _, err := RecommendedMERT("linux", "386"); err == nil {
		t.Fatal("unsupported architecture accepted")
	}
	for platform, expected := range map[string]struct {
		hash  string
		bytes int64
	}{
		"linux/amd64":   {"c464f5ec98561e7fdaaedbf86f9f93abf86904832c3b29f7bd90e69c2f2ed233", 1828938273},
		"windows/amd64": {"6483ce9b761492a963b02fd7edd6ecdea7efa286d4d015c4b5acfefd0687541d", 1642630436},
	} {
		parts := strings.Split(platform, "/")
		mert, err := RecommendedMERTGPU(parts[0], parts[1])
		if err != nil || !strings.HasSuffix(mert.URL, "/mert-"+parts[0]+"-"+parts[1]+"-gpu/manifest.json") || mert.SHA256 != expected.hash || mert.DownloadBytes != expected.bytes {
			t.Fatalf("wrong GPU MERT distribution for %s: %+v %v", platform, mert, err)
		}
	}
	if _, err := RecommendedMERTGPU("darwin", "arm64"); err == nil {
		t.Fatal("unpublished CUDA MERT platform accepted")
	}
	if _, err := recommended("intent-encoders-v1"); err == nil {
		t.Fatal("retired combined intent pack is still recommended")
	}
	clapPlatforms := map[string]struct {
		hash  string
		bytes int64
	}{
		"darwin/arm64":  {"02693451b932d53007f5c2d17e713942a938e7640662f68f16879bb5fed9d18b", 726861199},
		"linux/arm64":   {"f6082ff51b6df68dff99492c49583785c4903c390acdc0f46b23c7f66824bdb0", 724894253},
		"linux/amd64":   {"701f021df640044ec3d25fe7aef20a6633898a518b0096f87023f1aba5c5bae5", 725762212},
		"windows/arm64": {"06fcc36bc096c097b9cef7abd3f07783da9e770470c1357d207de2d8da8b38ad", 722800048},
		"windows/amd64": {"8a9368d1c5fcb301104ad89a91a23d5b4593a0a44968021e99be7ef6dd84a15d", 722852708},
	}
	for platform, expected := range clapPlatforms {
		parts := strings.Split(platform, "/")
		clap, err := RecommendedCLAP(parts[0], parts[1])
		if err != nil || !strings.HasSuffix(clap.URL, "/clap-"+parts[0]+"-"+parts[1]+"/manifest.json") || clap.SHA256 != expected.hash || clap.DownloadBytes != expected.bytes {
			t.Fatalf("wrong CLAP distribution for %s: %+v %v", platform, clap, err)
		}
	}
	if _, err := RecommendedCLAP("darwin", "amd64"); err == nil {
		t.Fatal("unpublished CLAP platform accepted")
	}
	for platform, expected := range map[string]struct {
		hash  string
		bytes int64
	}{
		"linux/amd64":   {"aabdbe4afe0e4971093cc09285ec06b1d17c9f3bb013f930079231eb8c6cbd65", 2302443250},
		"windows/amd64": {"d4a2c5cc903bfd321f45f5e62d4e6989668e19e225b7415908526462ac022542", 2116252087},
	} {
		parts := strings.Split(platform, "/")
		clap, err := RecommendedCLAPGPU(parts[0], parts[1])
		if err != nil || !strings.HasSuffix(clap.URL, "/clap-"+parts[0]+"-"+parts[1]+"-gpu/manifest.json") || clap.SHA256 != expected.hash || clap.DownloadBytes != expected.bytes {
			t.Fatalf("wrong GPU CLAP distribution for %s: %+v %v", platform, clap, err)
		}
	}
	if _, err := RecommendedCLAPGPU("darwin", "arm64"); err == nil {
		t.Fatal("unpublished CUDA CLAP platform accepted")
	}
}

func TestPinnedManifestRejectsChangesBeforeFetchingParts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	location := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(location, []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	expected := fmt.Sprintf("%x", sha256.Sum256([]byte("different approved bytes")))
	err := FetchPinned(context.Background(), location, expected, filepath.Join(dir, "cache"), filepath.Join(dir, "out"), nil)
	if err == nil || !strings.Contains(err.Error(), "pinned model manifest checksum mismatch") {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cache")); !os.IsNotExist(err) {
		t.Fatal("cache touched before trust check", err)
	}
}
