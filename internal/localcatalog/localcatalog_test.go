package localcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/librarylearn"
	"github.com/platten/playlistai/internal/librarypack"
)

func testSpace() librarypack.VectorSpace {
	return librarypack.VectorSpace{
		Name: "library_mert", Dimension: 3, DType: "float32", ByteOrder: "little", Normalized: true,
		Model: "MERT-v1-95M", ModelRevision: "fixture", GraphSHA256: strings.Repeat("a", 64),
		Decoder: "fixture-decoder", Preprocessing: "mert-mono-24k-v1", Sampling: "balanced-v1",
		Pooling: "final-mean-l2-v1", Scope: "sampled-excerpts", Missingness: "absent-row",
	}
}

func testTracks() []librarypack.Track {
	return []librarypack.Track{
		{ID: "far", Artist: "Beyonce", Title: "Other", Album: "Set", MERT: []float32{0, 1, 0}},
		{ID: "meta", Artist: "東京事変", Title: "群青日和", Album: "教育", Missingness: []byte(`{"mert":"not_analyzed"}`)},
		{ID: "near", Artist: "Beyoncé", Title: "Halo", Album: "I Am", MERT: []float32{0.8, 0.6, 0}, RootAlias: "music-main", RelativePath: "Beyonce/Halo.flac"},
		{ID: "seed", Artist: "Seed Artist", Title: "Origin", Album: "Start", MERT: []float32{1, 0, 0}},
	}
}

func writeTestPack(t *testing.T, path, generation string, tracks []librarypack.Track) librarypack.Manifest {
	t.Helper()
	manifest, err := librarypack.Write(context.Background(), path, librarypack.Pack{
		CorpusGeneration: generation, MetadataGeneration: "metadata-" + generation,
		MERTGeneration: "mert-" + generation, MERT: testSpace(), Tracks: tracks,
	}, librarypack.Limits{})
	if err != nil {
		t.Fatalf("write pack: %v", err)
	}
	return manifest
}

func activatePack(t *testing.T, manager *librarypack.Manager, path string) {
	t.Helper()
	staged, err := manager.Stage(context.Background(), path)
	if err != nil {
		t.Fatalf("stage pack: %v", err)
	}
	if err := manager.Activate(context.Background(), staged); err != nil {
		t.Fatalf("activate pack: %v", err)
	}
}

func openTestCatalog(t *testing.T, tracks []librarypack.Track, mappings map[string]string) (*Catalog, *librarypack.Manager) {
	t.Helper()
	root := t.TempDir()
	pack := filepath.Join(root, "library.paipack")
	writeTestPack(t, pack, "corpus-one", tracks)
	manager, err := librarypack.OpenManager(context.Background(), filepath.Join(root, "managed"), librarypack.Limits{})
	if err != nil {
		t.Fatalf("open manager: %v", err)
	}
	activatePack(t, manager, pack)
	lease, err := manager.Pin()
	if err != nil {
		t.Fatalf("pin generation: %v", err)
	}
	catalog, err := Open(lease, Options{SourceID: "main", RootMappings: mappings, PageSize: 2})
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	return catalog, manager
}

func TestMetadataAndMERTTracksAreIndependentlyRetrievable(t *testing.T) {
	catalog, manager := openTestCatalog(t, testTracks(), nil)
	defer manager.Close()
	defer catalog.Close()

	metadata, err := catalog.Search(context.Background(), MetadataQuery{Text: "東京 群青", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 1 || metadata[0].Track.LocalID != "meta" || hasCapability(metadata[0].Track.Capabilities, "mert") {
		t.Fatalf("metadata-only result = %#v", metadata)
	}
	if !strings.HasPrefix(metadata[0].Track.ID, "local:main:") || metadata[0].Evidence.Channel != MetadataChannel {
		t.Fatalf("result leaked a non-local identity: %#v", metadata[0])
	}

	neighbors, err := catalog.Neighbors(context.Background(), NeighborQuery{SeedID: catalog.NamespacedID("seed"), Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(neighbors) != 2 || neighbors[0].Track.LocalID != "near" || neighbors[1].Track.LocalID != "far" {
		t.Fatalf("neighbors = %#v", neighbors)
	}
	for _, hit := range neighbors {
		if !strings.HasPrefix(hit.Track.ID, "local:main:") || hit.Evidence.Channel != MERTChannel || hit.Evidence.VectorSpace == nil {
			t.Fatalf("invalid local MERT evidence: %#v", hit)
		}
	}
	if neighbors[0].Track.Provenance.Source != "local_library" || neighbors[0].Track.Provenance.PackID == "" {
		t.Fatalf("missing provenance: %#v", neighbors[0].Track.Provenance)
	}
}

func TestUnicodeSearchFoldsAccents(t *testing.T) {
	catalog, manager := openTestCatalog(t, testTracks(), nil)
	defer manager.Close()
	defer catalog.Close()
	hits, err := catalog.Search(context.Background(), MetadataQuery{Text: "beyonce halo", Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Track.LocalID != "near" {
		t.Fatalf("accent-folded hits = %#v", hits)
	}
}

func TestSearchUsesExportedWeightedGenreModel(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "learned.paipack")
	sum := sha256.Sum256([]byte("artist\x00model artist"))
	model := librarylearn.MetadataModel{Version: librarylearn.MetadataVersion, Vocabulary: []string{"rock"}, IDF: []float64{1}, Rows: []librarylearn.SparseRow{{ArtistID: "artist:" + hex.EncodeToString(sum[:12]), Values: []librarylearn.SparseValue{{Column: 0, Value: 1}}}}}
	learning, err := json.Marshal(struct {
		Metadata librarylearn.MetadataModel `json:"metadata"`
	}{model})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := librarypack.Write(context.Background(), archive, librarypack.Pack{CorpusGeneration: "corpus-learned", MetadataGeneration: "metadata-learned", MERT: testSpace(), Learning: learning, Tracks: []librarypack.Track{{ID: "learned", Artist: "Model Artist", Title: "No Genre In Display"}}}, librarypack.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	manager, err := librarypack.OpenManager(context.Background(), filepath.Join(root, "managed"), librarypack.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	activatePack(t, manager, archive)
	lease, err := manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Open(lease, Options{SourceID: "learned"})
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	hits, err := catalog.Search(context.Background(), MetadataQuery{Text: "rock", Limit: 4})
	if err != nil || len(hits) != 1 || hits[0].Track.LocalID != "learned" {
		t.Fatalf("learned metadata hits=%+v err=%v", hits, err)
	}
}

func TestIncompatibleMERTContractIsRejected(t *testing.T) {
	catalog, manager := openTestCatalog(t, testTracks(), nil)
	defer manager.Close()
	defer catalog.Close()
	wrong := testSpace()
	wrong.Sampling = "preview-v1"
	_, err := catalog.Neighbors(context.Background(), NeighborQuery{
		Vector: []float32{1, 0, 0}, Space: &wrong, Limit: 2,
	})
	if !errors.Is(err, ErrIncompatibleSpace) {
		t.Fatalf("error = %v, want incompatible space", err)
	}
	if _, err := catalog.Neighbors(context.Background(), NeighborQuery{Vector: []float32{1, 0, 0}, Limit: 2}); !errors.Is(err, ErrIncompatibleSpace) {
		t.Fatalf("missing-contract error = %v", err)
	}
}

func TestExecutorMergeIgnoresChannelCompletionOrder(t *testing.T) {
	catalog, manager := openTestCatalog(t, testTracks(), nil)
	defer manager.Close()
	defer catalog.Close()

	metaTrack, ok, err := catalog.Lookup(context.Background(), catalog.NamespacedID("near"))
	if err != nil || !ok {
		t.Fatalf("lookup: ok=%v err=%v", ok, err)
	}
	farTrack, ok, err := catalog.Lookup(context.Background(), catalog.NamespacedID("far"))
	if err != nil || !ok {
		t.Fatalf("lookup: ok=%v err=%v", ok, err)
	}
	metaHits := []Hit{{Track: metaTrack, Evidence: Evidence{Channel: MetadataChannel, Rank: 1, Score: 2, Provenance: catalog.Provenance()}}}
	mertHits := []Hit{
		{Track: farTrack, Evidence: Evidence{Channel: MERTChannel, Rank: 1, Score: .9, Provenance: catalog.Provenance()}},
		{Track: metaTrack, Evidence: Evidence{Channel: MERTChannel, Rank: 2, Score: .8, Provenance: catalog.Provenance()}},
	}
	run := func(metadataFirst bool) QueryResult {
		executor, err := NewExecutor(catalog, 2)
		if err != nil {
			t.Fatal(err)
		}
		gate := make(chan struct{})
		var once sync.Once
		if metadataFirst {
			executor.metadata = func(context.Context, any) ([]Hit, error) { once.Do(func() { close(gate) }); return metaHits, nil }
			executor.mert = func(ctx context.Context, _ any) ([]Hit, error) {
				select {
				case <-gate:
					return mertHits, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		} else {
			executor.mert = func(context.Context, any) ([]Hit, error) { once.Do(func() { close(gate) }); return mertHits, nil }
			executor.metadata = func(ctx context.Context, _ any) ([]Hit, error) {
				select {
				case <-gate:
					return metaHits, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}
		result, err := executor.Query(context.Background(), Query{Metadata: &MetadataQuery{}, MERT: &NeighborQuery{}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first, second := run(true), run(false)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("completion order changed merge:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if len(first.Candidates) != 2 || first.Candidates[0].Track.LocalID != "near" || len(first.Candidates[0].Evidence) != 2 || first.Candidates[1].Track.LocalID != "far" {
		t.Fatalf("canonical candidates = %#v", first.Candidates)
	}
}

func TestExecutorCancellationStopsChannels(t *testing.T) {
	catalog, manager := openTestCatalog(t, testTracks(), nil)
	defer manager.Close()
	defer catalog.Close()
	executor, err := NewExecutor(catalog, 2)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 2)
	wait := func(ctx context.Context, _ any) ([]Hit, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	executor.metadata, executor.mert = wait, wait
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := executor.Query(ctx, Query{Metadata: &MetadataQuery{}, MERT: &NeighborQuery{}})
		done <- err
	}()
	<-started
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("query error = %v, want canceled", err)
	}
}

func TestExecutorBoundsConcurrentChannelWork(t *testing.T) {
	catalog, manager := openTestCatalog(t, testTracks(), nil)
	defer manager.Close()
	defer catalog.Close()
	executor, err := NewExecutor(catalog, 2)
	if err != nil {
		t.Fatal(err)
	}
	var active, highWater atomic.Int32
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	runner := func(ctx context.Context, _ any) ([]Hit, error) {
		current := active.Add(1)
		for {
			observed := highWater.Load()
			if current <= observed || highWater.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
			active.Add(-1)
			return []Hit{}, nil
		case <-ctx.Done():
			active.Add(-1)
			return nil, ctx.Err()
		}
	}
	executor.metadata, executor.mert = runner, runner
	var group sync.WaitGroup
	errorsOut := make(chan error, 4)
	for range 4 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := executor.Query(context.Background(), Query{Metadata: &MetadataQuery{}, MERT: &NeighborQuery{}})
			errorsOut <- err
		}()
	}
	<-started
	<-started
	if active.Load() != 2 {
		t.Fatalf("active channel work = %d, want overlap at capacity", active.Load())
	}
	close(release)
	group.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatal(err)
		}
	}
	if highWater.Load() != 2 {
		t.Fatalf("channel high-water = %d, want exactly 2", highWater.Load())
	}
}

func TestExecutorsFromSeparatePinsShareGenerationBudget(t *testing.T) {
	first, manager := openTestCatalog(t, testTracks(), nil)
	defer manager.Close()
	defer first.Close()
	lease, err := manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(lease, Options{SourceID: "second", PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	left, err := NewExecutor(first, 2)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewExecutor(second, 2)
	if err != nil {
		t.Fatal(err)
	}
	if left == right {
		t.Fatal("separate pins unexpectedly share namespace-bound executor state")
	}
	if left.requestSlots != right.requestSlots || left.channelSlots != right.channelSlots {
		t.Fatal("separate pins over one generation did not share admission slots")
	}

	var active, highWater atomic.Int32
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	runner := func(ctx context.Context, _ any) ([]Hit, error) {
		current := active.Add(1)
		for {
			observed := highWater.Load()
			if current <= observed || highWater.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
			active.Add(-1)
			return []Hit{}, nil
		case <-ctx.Done():
			active.Add(-1)
			return nil, ctx.Err()
		}
	}
	left.metadata, right.metadata = runner, runner
	var group sync.WaitGroup
	errorsOut := make(chan error, 8)
	for i := range 8 {
		executor := left
		if i%2 == 1 {
			executor = right
		}
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := executor.Query(context.Background(), Query{Metadata: &MetadataQuery{}})
			errorsOut <- err
		}()
	}
	<-started
	<-started
	if got := active.Load(); got != 2 {
		t.Fatalf("active work = %d, want shared capacity 2", got)
	}
	close(release)
	group.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := highWater.Load(); got != 2 {
		t.Fatalf("generation high-water = %d, want 2", got)
	}
}

func TestSharedExecutorCancellationWhileWaitingForAdmission(t *testing.T) {
	first, manager := openTestCatalog(t, testTracks(), nil)
	defer manager.Close()
	defer first.Close()
	lease, err := manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(lease, Options{SourceID: "second"})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	left, err := NewExecutor(first, 1)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewExecutor(second, 1)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	left.metadata = func(ctx context.Context, _ any) ([]Hit, error) {
		close(started)
		select {
		case <-release:
			return []Hit{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	firstDone := make(chan error, 1)
	go func() {
		_, err := left.Query(context.Background(), Query{Metadata: &MetadataQuery{}})
		firstDone <- err
	}()
	<-started
	waiting, cancel := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() {
		_, err := right.Query(waiting, Query{Metadata: &MetadataQuery{}})
		secondDone <- err
	}()
	cancel()
	if err := <-secondDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting query error = %v, want canceled", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	right.metadata = func(context.Context, any) ([]Hit, error) { return []Hit{}, nil }
	if _, err := right.Query(context.Background(), Query{Metadata: &MetadataQuery{}}); err != nil {
		t.Fatalf("canceled waiter leaked admission capacity: %v", err)
	}
}

func TestGenerationBudgetRejectsConflictsAndResetsAfterPinsClose(t *testing.T) {
	first, manager := openTestCatalog(t, testTracks(), nil)
	defer manager.Close()
	lease, err := manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(lease, Options{SourceID: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewExecutor(first, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := NewExecutor(second, 4); err == nil || !strings.Contains(err.Error(), "generation query budget is 2") {
		t.Fatalf("conflicting budget error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	lease, err = manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(lease, Options{SourceID: "reopened"})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := NewExecutor(reopened, 4); err != nil {
		t.Fatalf("released generation budget did not accept new capacity: %v", err)
	}
}

func TestCatalogCloseWaitsForAdmittedSharedQuery(t *testing.T) {
	catalog, manager := openTestCatalog(t, testTracks(), nil)
	defer manager.Close()
	executor, err := NewExecutor(catalog, 1)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	executor.metadata = func(ctx context.Context, _ any) ([]Hit, error) {
		close(started)
		select {
		case <-release:
			return []Hit{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	queryDone := make(chan error, 1)
	go func() {
		_, err := executor.Query(context.Background(), Query{Metadata: &MetadataQuery{}})
		queryDone <- err
	}()
	<-started
	closeDone := make(chan error, 1)
	go func() { closeDone <- catalog.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("close returned before admitted query drained: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-queryDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Query(context.Background(), Query{Metadata: &MetadataQuery{}}); !errors.Is(err, ErrClosed) {
		t.Fatalf("query after close error = %v", err)
	}
	if _, err := NewExecutor(catalog, 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("executor after close error = %v", err)
	}
}

func TestResolvePathAvailabilityOfflineAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Beyonce"), 0o755); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(root, "Beyonce", "Halo.flac")
	if err := os.WriteFile(good, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.flac")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(root, "Beyonce", "Escape.flac")
	if err := os.Symlink(outside, escape); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	tracks := testTracks()
	tracks = append(tracks,
		librarypack.Track{ID: "escape", Artist: "Escape", Title: "Outside", RootAlias: "music-main", RelativePath: "Beyonce/Escape.flac"},
		librarypack.Track{ID: "missing", Artist: "Missing", Title: "File", RootAlias: "music-main", RelativePath: "Beyonce/Missing.flac"},
	)
	catalog, manager := openTestCatalog(t, tracks, map[string]string{"music-main": root})
	defer manager.Close()
	defer catalog.Close()
	for id, state := range map[string]PathState{"near": PathAvailable, "escape": PathUnsafe, "missing": PathMissing} {
		resolved, err := catalog.ResolvePath(context.Background(), catalog.NamespacedID(id))
		if err != nil {
			t.Fatalf("resolve %s: %v", id, err)
		}
		if resolved.State != state {
			t.Fatalf("resolve %s state = %s, want %s (%#v)", id, resolved.State, state, resolved)
		}
	}

	lease, err := manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	offline, err := Open(lease, Options{SourceID: "offline", RootMappings: map[string]string{"music-main": filepath.Join(t.TempDir(), "absent")}})
	if err != nil {
		t.Fatal(err)
	}
	defer offline.Close()
	resolved, err := offline.ResolvePath(context.Background(), offline.NamespacedID("near"))
	if err != nil || resolved.State != PathOffline {
		t.Fatalf("offline resolution = %#v, %v", resolved, err)
	}
}

func TestPinnedCatalogSurvivesReplacementAndRemoval(t *testing.T) {
	root := t.TempDir()
	firstPath, secondPath := filepath.Join(root, "first.paipack"), filepath.Join(root, "second.paipack")
	writeTestPack(t, firstPath, "first", testTracks())
	writeTestPack(t, secondPath, "second", []librarypack.Track{{ID: "new", Artist: "New", Title: "Track", MERT: []float32{1, 0, 0}}})
	manager, err := librarypack.OpenManager(context.Background(), filepath.Join(root, "managed"), librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	activatePack(t, manager, firstPath)
	lease, err := manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Open(lease, Options{SourceID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()

	activatePack(t, manager, secondPath)
	if err := manager.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	track, ok, err := catalog.Lookup(context.Background(), catalog.NamespacedID("meta"))
	if err != nil || !ok || track.LocalID != "meta" {
		t.Fatalf("pinned lookup after removal = %#v, ok=%v, err=%v", track, ok, err)
	}
	hits, err := catalog.Neighbors(context.Background(), NeighborQuery{SeedID: catalog.NamespacedID("seed"), Limit: 1})
	if err != nil || len(hits) != 1 || hits[0].Track.LocalID != "near" {
		t.Fatalf("pinned neighbors after removal = %#v, err=%v", hits, err)
	}
	if _, err := manager.Pin(); !errors.Is(err, librarypack.ErrNoActiveGeneration) {
		t.Fatalf("new pin after removal = %v", err)
	}
}

func TestInvalidNamespaceAndRootMappingsRejected(t *testing.T) {
	catalog, manager := openTestCatalog(t, testTracks(), nil)
	defer manager.Close()
	defer catalog.Close()
	if _, _, err := catalog.Lookup(context.Background(), "seed"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("unqualified lookup error = %v", err)
	}

	lease, err := manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(lease, Options{SourceID: "bad namespace"}); err == nil {
		t.Fatal("invalid namespace accepted")
	}
	lease, err = manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(lease, Options{SourceID: "other", RootMappings: map[string]string{"music-main": "relative"}}); err == nil {
		t.Fatal("relative root accepted")
	}
}
