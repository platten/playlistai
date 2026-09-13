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
	d := RecommendedIntent()
	if d.Name != "intent-encoders-v1" || d.DownloadBytes != 324060693 || !validHash(d.SHA256) {
		t.Fatal(d)
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
