package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/platten/playlistai/internal/discoveryasset"
	"github.com/platten/playlistai/internal/librarypack"
)

func appLibrarySpace() librarypack.VectorSpace {
	return librarypack.VectorSpace{
		Name: "library_mert", Dimension: 3, DType: "float32", ByteOrder: "little", Normalized: true,
		Model: "MERT-v1-95M", ModelRevision: "fixture", GraphSHA256: strings.Repeat("b", 64),
		Decoder: "fixture", Preprocessing: "mert-mono-24k-v1", Sampling: "balanced-v1",
		Pooling: "final-mean-l2-v1", Scope: "sampled-excerpts", Missingness: "absent-row",
	}
}

func writeAppLibraryPack(t *testing.T, path, generation, trackID string, relativePath string) librarypack.Manifest {
	t.Helper()
	track := librarypack.Track{ID: trackID, Artist: "Local Artist", Title: "Local Track", MERT: []float32{1, 0, 0}}
	if relativePath != "" {
		track.RootAlias, track.RelativePath = "music-main", relativePath
	}
	_, err := librarypack.Write(context.Background(), path, librarypack.Pack{
		CorpusGeneration: generation, MetadataGeneration: "metadata-" + generation,
		MERTGeneration: "mert-" + generation, MERT: appLibrarySpace(), Tracks: []librarypack.Track{track},
	}, librarypack.Limits{})
	if err != nil {
		t.Fatalf("write pack: %v", err)
	}
	manifest, err := discoveryasset.BuildIndexedFromPack(context.Background(), path, path, librarypack.Limits{})
	if err != nil {
		t.Fatalf("index pack: %v", err)
	}
	return manifest
}

func TestLocalLibraryRejectsUnindexedPackWithoutBuildingIndexes(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.paipack")
	if _, err := librarypack.Write(ctx, path, librarypack.Pack{CorpusGeneration: "legacy", MetadataGeneration: "legacy", Tracks: []librarypack.Track{{ID: "one", Artist: "Artist", Title: "One"}}}, librarypack.Limits{}); err != nil {
		t.Fatal(err)
	}
	c := &Container{cfg: testConfig(t)}
	defer c.Close()
	if _, err := c.ImportLocalLibrary(ctx, path); err == nil || !strings.Contains(err.Error(), "re-export") {
		t.Fatalf("unindexed pack was accepted: %v", err)
	}
	status, err := c.LocalLibraryStatus()
	if err != nil || status.Installed {
		t.Fatalf("failed import changed active library: %+v %v", status, err)
	}
}

func fileDigest(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(raw)
}

func TestLocalLibraryImportControlsAndRemovalPreserveSources(t *testing.T) {
	cfg := testConfig(t)
	packPath := filepath.Join(t.TempDir(), "source.paipack")
	manifest := writeAppLibraryPack(t, packPath, "first", "old-track", "Artist/Track.flac")
	musicRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(musicRoot, "Artist"), 0o755); err != nil {
		t.Fatal(err)
	}
	musicPath := filepath.Join(musicRoot, "Artist", "Track.flac")
	if err := os.WriteFile(musicPath, []byte("original music fixture"), 0o400); err != nil {
		t.Fatal(err)
	}
	packBefore, musicBefore := fileDigest(t, packPath), fileDigest(t, musicPath)

	c := &Container{cfg: cfg}
	status, err := c.ImportLocalLibrary(context.Background(), packPath)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || status.PackID != manifest.PackID || status.Coverage.Tracks != 1 || status.Coverage.MERT != 1 || len(status.Roots) != 1 {
		t.Fatalf("imported status = %+v", status)
	}
	status, err = c.SetLocalLibraryRoot("music-main", musicRoot)
	if err != nil || !status.Roots[0].Mapped || !status.Roots[0].Available {
		t.Fatalf("mapped status = %+v, %v", status, err)
	}
	status, err = c.SetLocalLibraryMode(LocalLibraryOnly)
	if err != nil || status.Mode != LocalLibraryOnly {
		t.Fatalf("mode status = %+v, %v", status, err)
	}
	pinned, err := c.PinLocalCatalog()
	if err != nil {
		t.Fatal(err)
	}
	track, ok, err := pinned.Lookup(context.Background(), pinned.NamespacedID("old-track"))
	if err != nil || !ok || track.LocalID != "old-track" {
		t.Fatalf("pinned lookup = %+v ok=%v err=%v", track, ok, err)
	}

	status, err = c.RemoveLocalLibrary(context.Background())
	if err != nil || status.Installed || status.Mode != LocalLibraryCombined || len(status.Roots) != 0 {
		t.Fatalf("removed status = %+v, %v", status, err)
	}
	// The request pinned before removal still owns the immutable generation.
	if _, ok, err := pinned.Lookup(context.Background(), pinned.NamespacedID("old-track")); err != nil || !ok {
		t.Fatalf("pinned reader after removal: ok=%v err=%v", ok, err)
	}
	if got := fileDigest(t, packPath); got != packBefore {
		t.Fatal("source pack changed during import/removal")
	}
	if got := fileDigest(t, musicPath); got != musicBefore {
		t.Fatal("original music changed during import/removal")
	}
	if err := pinned.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	// Removal and the safe combined-mode reset survive restart.
	reopened := &Container{cfg: cfg}
	t.Cleanup(func() { _ = reopened.Close() })
	status, err = reopened.LocalLibraryStatus()
	if err != nil || status.Installed || status.Mode != LocalLibraryCombined {
		t.Fatalf("reopened status = %+v, %v", status, err)
	}
}

func TestReimportIdenticalPackRebuildsMissingDerivedIndex(t *testing.T) {
	c := &Container{cfg: testConfig(t)}
	t.Cleanup(func() { _ = c.Close() })
	packPath := filepath.Join(t.TempDir(), "source.paipack")
	writeAppLibraryPack(t, packPath, "rebuild", "track", "")
	if _, err := c.ImportLocalLibrary(context.Background(), packPath); err != nil {
		t.Fatal(err)
	}
	state, err := c.localLibrary()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := state.manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(lease.Generation().Directory(), "local-index-v1")
	lease.Release()
	if err := os.RemoveAll(indexPath); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ImportLocalLibrary(context.Background(), packPath); err != nil {
		t.Fatalf("identical reimport did not reuse prebuilt index: %v", err)
	}
	catalog, err := c.PinLocalCatalog()
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	if _, ok, err := catalog.Lookup(context.Background(), catalog.NamespacedID("track")); err != nil || !ok {
		t.Fatalf("rebuilt catalog lookup: ok=%v err=%v", ok, err)
	}
}

func TestLocalLibraryReplacementAndCanceledOrCorruptUpdateKeepOldPin(t *testing.T) {
	cfg := testConfig(t)
	firstPath, secondPath := filepath.Join(t.TempDir(), "first.paipack"), filepath.Join(t.TempDir(), "second.paipack")
	first := writeAppLibraryPack(t, firstPath, "first", "old-track", "")
	second := writeAppLibraryPack(t, secondPath, "second", "new-track", "")
	c := &Container{cfg: cfg}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.ImportLocalLibrary(context.Background(), firstPath); err != nil {
		t.Fatal(err)
	}
	old, err := c.PinLocalCatalog()
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ImportLocalLibrary(canceled, secondPath); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled update error = %v", err)
	}
	status, err := c.LocalLibraryStatus()
	if err != nil || status.PackID != first.PackID {
		t.Fatalf("canceled update changed active pack: %+v %v", status, err)
	}
	corrupt := filepath.Join(t.TempDir(), "corrupt.paipack")
	if err := os.WriteFile(corrupt, []byte("not a pack"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ImportLocalLibrary(context.Background(), corrupt); err == nil {
		t.Fatal("corrupt update accepted")
	}
	status, err = c.LocalLibraryStatus()
	if err != nil || status.PackID != first.PackID {
		t.Fatalf("corrupt update changed active pack: %+v %v", status, err)
	}

	status, err = c.ImportLocalLibrary(context.Background(), secondPath)
	if err != nil || status.PackID != second.PackID {
		t.Fatalf("replacement status = %+v, %v", status, err)
	}
	if _, ok, err := old.Lookup(context.Background(), old.NamespacedID("old-track")); err != nil || !ok {
		t.Fatalf("old pin after replacement: ok=%v err=%v", ok, err)
	}
	current, err := c.PinLocalCatalog()
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	if _, ok, err := current.Lookup(context.Background(), current.NamespacedID("new-track")); err != nil || !ok {
		t.Fatalf("new pin lookup: ok=%v err=%v", ok, err)
	}
}

func TestLocalLibraryMutationsAreSerialized(t *testing.T) {
	cfg := testConfig(t)
	firstPath, secondPath := filepath.Join(t.TempDir(), "first.paipack"), filepath.Join(t.TempDir(), "second.paipack")
	first := writeAppLibraryPack(t, firstPath, "first", "first-track", "")
	second := writeAppLibraryPack(t, secondPath, "second", "second-track", "")
	c := &Container{cfg: cfg}
	t.Cleanup(func() { _ = c.Close() })

	var group sync.WaitGroup
	errorsOut := make(chan error, 2)
	for _, path := range []string{firstPath, secondPath} {
		path := path
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := c.ImportLocalLibrary(context.Background(), path)
			errorsOut <- err
		}()
	}
	group.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatalf("serialized import: %v", err)
		}
	}
	status, err := c.LocalLibraryStatus()
	if err != nil || status.PackID != first.PackID && status.PackID != second.PackID {
		t.Fatalf("final active pack = %+v, %v", status, err)
	}
}

func TestLocalLibraryStagingDoesNotBlockPinnedReaders(t *testing.T) {
	c := &Container{cfg: testConfig(t)}
	t.Cleanup(func() { _ = c.Close() })
	oldPath, newPath := filepath.Join(t.TempDir(), "old.paipack"), filepath.Join(t.TempDir(), "new.paipack")
	oldManifest := writeAppLibraryPack(t, oldPath, "old", "old-track", "")
	newManifest := writeAppLibraryPack(t, newPath, "new", "new-track", "")
	if _, err := c.ImportLocalLibrary(context.Background(), oldPath); err != nil {
		t.Fatal(err)
	}
	state, err := c.localLibrary()
	if err != nil {
		t.Fatal(err)
	}
	staged, release := make(chan struct{}), make(chan struct{})
	state.onStaged = func() {
		close(staged)
		<-release
	}
	importDone := make(chan error, 1)
	go func() {
		_, err := c.ImportLocalLibrary(context.Background(), newPath)
		importDone <- err
	}()
	<-staged
	pinned, err := c.PinLocalCatalog()
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	if pinned.Provenance().PackID != oldManifest.PackID {
		_ = pinned.Close()
		close(release)
		t.Fatalf("reader observed staged generation: got=%s old=%s new=%s", pinned.Provenance().PackID, oldManifest.PackID, newManifest.PackID)
	}
	_ = pinned.Close()
	close(release)
	if err := <-importDone; err != nil {
		t.Fatal(err)
	}
}

func TestLocalLibraryRejectsInvalidModeAndMapping(t *testing.T) {
	c := &Container{cfg: testConfig(t)}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.SetLocalLibraryMode("invalid"); err == nil {
		t.Fatal("invalid mode accepted")
	}
	if _, err := c.SetLocalLibraryMode(LocalLibraryOnly); err == nil {
		t.Fatal("library-only mode accepted without a pack")
	}
	pack := filepath.Join(t.TempDir(), "library.paipack")
	writeAppLibraryPack(t, pack, "first", "track", "Artist/Track.flac")
	if _, err := c.ImportLocalLibrary(context.Background(), pack); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetLocalLibraryRoot("unknown", t.TempDir()); err == nil {
		t.Fatal("unknown alias accepted")
	}
	if _, err := c.SetLocalLibraryRoot("music-main", "relative/path"); err == nil {
		t.Fatal("relative root accepted")
	}
}

func TestLocalLibraryModeAndMappingsSurviveRestart(t *testing.T) {
	cfg := testConfig(t)
	pack := filepath.Join(t.TempDir(), "library.paipack")
	writeAppLibraryPack(t, pack, "first", "track", "Artist/Track.flac")
	offlineRoot := filepath.Join(t.TempDir(), "offline")
	c := &Container{cfg: cfg}
	if _, err := c.ImportLocalLibrary(context.Background(), pack); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetLocalLibraryRoot("music-main", offlineRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetLocalLibraryMode(LocalLibraryOnly); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := &Container{cfg: cfg}
	t.Cleanup(func() { _ = reopened.Close() })
	status, err := reopened.LocalLibraryStatus()
	if err != nil || !status.Installed || status.Mode != LocalLibraryOnly || len(status.Roots) != 1 {
		t.Fatalf("reopened settings = %+v, %v", status, err)
	}
	if status.Roots[0].Path != offlineRoot || !status.Roots[0].Mapped || status.Roots[0].Available {
		t.Fatalf("reopened root = %+v", status.Roots[0])
	}
}
