package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/discoveryasset"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/musicgraph"
)

func TestPreparedBudgetCountsRetainedAssetsAndRejectsBeforeActivation(t *testing.T) {
	c := &Container{cfg: config.Config{DataDir: t.TempDir()}}
	ctx := context.Background()
	first, firstHash, _ := graphFixture(t, 1)
	if _, err := c.ImportMusicGraph(ctx, first, firstHash); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(c.cfg.DataDir, "music-classifiers")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(root, "encoder.onnx"))
	if err != nil {
		t.Fatal(err)
	}
	// Sparse truncation reserves logical installed bytes without a model download.
	if err = f.Truncate(musicgraph.MaxAdditionalInstalledBytes - 1); err != nil {
		_ = f.Close()
		t.Skipf("sparse file unavailable: %v", err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	second, secondHash, _ := graphFixture(t, 2)
	if _, err = c.ImportMusicGraph(ctx, second, secondHash); err == nil {
		t.Fatal("installed-data ceiling ignored")
	}
	active, err := c.PreparedMusicGraph(ctx)
	if err != nil || active.SnapshotIdentity() != firstHash {
		t.Fatal("failed budget check changed active graph", err)
	}
	if _, err = os.Stat(filepath.Join(c.graphDir(), graphName(secondHash))); !os.IsNotExist(err) {
		t.Fatal("over-budget graph installed")
	}
	if _, err = c.ImportMusicGraph(ctx, first, firstHash); err != nil {
		t.Fatal("reactivating existing snapshot should add no bytes", err)
	}
}

func writeClassifiedBudgetPack(t *testing.T, generation string) string {
	t.Helper()
	id := core.MusicClassifierIdentity{Model: "fixture", Revision: "1", WeightsSHA256: strings.Repeat("a", 64), MetadataSHA256: strings.Repeat("b", 64)}
	evidence := core.MusicClassifierEvidence{Version: 1, Encoder: id, Preprocessing: "fixture/v1", Runtime: "fixture", AudioSHA256: strings.Repeat("c", 64), Source: "fixture", SourceID: "recording", License: "CC0-1.0", Coverage: core.LibraryCLAPCoverage{CoveredSeconds: 5, Segments: []core.LibraryAudioInterval{{EndSeconds: 5}}}, Heads: []core.MusicClassifierHead{{Kind: "instrumentation", Model: id, Classes: []string{"piano"}, Scores: []float32{.7}}}}
	path := filepath.Join(t.TempDir(), "classified.paipack")
	_, err := librarypack.Write(context.Background(), path, librarypack.Pack{CorpusGeneration: generation, MetadataGeneration: generation, Tracks: []librarypack.Track{{ID: "track", Artist: "Fixture", Title: "Song", ClassifierEvidence: []core.MusicClassifierEvidence{evidence}}}}, librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = discoveryasset.BuildIndexedFromPack(context.Background(), path, path, librarypack.Limits{}); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClassifiedImportsShareBudgetAndPublicationLock(t *testing.T) {
	ctx := context.Background()
	path := writeClassifiedBudgetPack(t, "classified")
	for _, kind := range []string{"local", "discovery"} {
		t.Run(kind, func(t *testing.T) {
			c := &Container{cfg: testConfig(t)}
			defer c.Close()
			importPack := func(path string) error {
				if kind == "local" {
					_, err := c.ImportLocalLibrary(ctx, path)
					return err
				}
				_, err := c.ImportDiscoveryAsset(ctx, path, nil)
				return err
			}
			baselinePath := filepath.Join(t.TempDir(), "baseline.paipack")
			baseline := writeAppLibraryPack(t, baselinePath, "budget-baseline", "baseline-track", "")
			if err := importPack(baselinePath); err != nil {
				t.Fatal(err)
			}
			if bytes, err := c.additionalPreparedBytes(ctx); err != nil || bytes != 0 {
				t.Fatal("unclassified baseline counted as additional", bytes, err)
			}
			release, err := c.graphInstallLock()
			if err != nil {
				t.Fatal(err)
			}
			if err = importPack(path); err == nil {
				t.Fatal("classified import bypassed graph publication lock")
			}
			if err = release(); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(c.cfg.DataDir, "music-classifiers")
			if err = os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			asset := filepath.Join(root, "encoder.onnx")
			f, err := os.Create(asset)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.Truncate(musicgraph.MaxAdditionalInstalledBytes - 1); err != nil {
				_ = f.Close()
				t.Skipf("sparse file unavailable: %v", err)
			}
			if err = f.Close(); err != nil {
				t.Fatal(err)
			}
			if err = importPack(path); err == nil {
				t.Fatal("classified import exceeded aggregate installed-data cap")
			}
			if kind == "local" {
				status, err := c.LocalLibraryStatus()
				if err != nil || !status.Installed || status.PackID != baseline.PackID {
					t.Fatal("failed import changed local activation", err)
				}
			} else {
				status, err := c.GetDiscoveryAssetStatus()
				if err != nil || !status.Installed || len(status.PackIDs) != 1 || status.PackIDs[0] != baseline.PackID {
					t.Fatal("failed import changed discovery activation", err)
				}
			}
			if bytes, err := c.additionalPreparedBytes(ctx); err != nil || bytes != musicgraph.MaxAdditionalInstalledBytes-1 {
				t.Fatal("failed staged classified data was retained", bytes, err)
			}
			if err = os.Remove(asset); err != nil {
				t.Fatal(err)
			}
			if err = importPack(path); err != nil {
				t.Fatal(err)
			}
			bytes, err := c.additionalPreparedBytes(ctx)
			if err != nil || bytes <= 0 {
				t.Fatal("actual installed classified pack not counted", bytes, err)
			}
			graph, hash, _ := graphFixture(t, 3)
			if _, err := c.ImportMusicGraph(ctx, graph, hash); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(graph)
			if err != nil {
				t.Fatal(err)
			}
			if total, err := c.additionalPreparedBytes(ctx); err != nil || total != bytes+info.Size() {
				t.Fatal("graph and classified pack do not share accounting", total, bytes, err)
			}
		})
	}
}

func TestPreparedBudgetCountsPinnedClassifiedLibraryGeneration(t *testing.T) {
	ctx := context.Background()
	c := &Container{cfg: testConfig(t)}
	defer c.Close()
	first := writeClassifiedBudgetPack(t, "first")
	second := writeClassifiedBudgetPack(t, "second")
	if _, err := c.ImportLocalLibrary(ctx, first); err != nil {
		t.Fatal(err)
	}
	before, err := c.additionalPreparedBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := c.PinLocalCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.ImportLocalLibrary(ctx, second); err != nil {
		pinned.Close()
		t.Fatal(err)
	}
	during, err := c.additionalPreparedBytes(ctx)
	if err != nil {
		pinned.Close()
		t.Fatal(err)
	}
	pinned.Close()
	after, err := c.additionalPreparedBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if during <= before || during <= after || before == 0 || after == 0 {
		t.Fatal("retained generation was not counted", before, during, after)
	}
}

func TestPreparedBudgetExcludesExistingBaseAssetsAndTemporaryDownloads(t *testing.T) {
	c := &Container{cfg: config.Config{DataDir: t.TempDir()}}
	for _, name := range []string{"catalog", "audio-models", "music-graph/.graph-download-test", "music-classifiers"} {
		root := filepath.Join(c.cfg.DataDir, name)
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "asset"), []byte("123"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	size, err := c.additionalPreparedBytes(context.Background())
	if err != nil || size != 3 {
		t.Fatalf("unexpected additional bytes %d, %v", size, err)
	}
}
