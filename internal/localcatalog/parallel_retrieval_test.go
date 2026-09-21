package localcatalog

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/searchwork"
)

type blockingBase struct {
	started chan struct{}
	release chan struct{}
	err     error
}

func (b blockingBase) Retrieve(ctx context.Context, _ ports.RetrievalRequest) ([]core.Candidate, error) {
	close(b.started)
	select {
	case <-b.release:
		return nil, b.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestCombinedRetrievalOverlapsBaseAndIndependentPackQueries(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("requires two job slots")
	}
	local, _ := openTestCatalog(t, testTracks(), nil)
	defer local.Close()
	executor, err := NewExecutor(local, 2)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	last := make(chan struct{})
	first := make(chan struct{})
	sentinel := errors.New("base failed")
	executor.metadata = func(ctx context.Context, value any) ([]Hit, error) {
		<-started
		query := value.(MetadataQuery)
		if query.Text == "first" {
			close(first)
			select {
			case <-last:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		} else {
			<-first
			close(last)
		}
		return nil, nil
	}
	r, err := NewCombinedRetriever(blockingBase{started: started, release: last, err: sentinel}, local, ModeCombined, 2)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = r.Retrieve(ctx, ports.RetrievalRequest{Intent: core.MusicIntent{References: []core.IntentReference{
		{Kind: core.ReferenceTrack, Query: "first", Influence: core.InfluencePositive},
		{Kind: core.ReferenceTrack, Query: "second", Influence: core.InfluencePositive},
	}}})
	if !errors.Is(err, sentinel) {
		t.Fatalf("base error must survive successful pack queries: %v", err)
	}
}

func TestCombinedRetrievalCancelsAndJoinsBaseOnClosedPack(t *testing.T) {
	local, _ := openTestCatalog(t, testTracks(), nil)
	_ = local.Close()
	started := make(chan struct{})
	release := make(chan struct{})
	r, err := NewCombinedRetriever(blockingBase{started: started, release: release}, local, ModeCombined, 2)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Retrieve(context.Background(), ports.RetrievalRequest{})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("err=%v", err)
	}
	select {
	case <-started:
	default:
		t.Fatal("base work was not joined")
	}
}

func BenchmarkInstalledPackQueries(b *testing.B) {
	ctx := context.Background()
	root := b.TempDir()
	path := filepath.Join(root, "fixture.paipack")
	tracks := make([]librarypack.Track, 4096)
	for i := range tracks {
		tracks[i] = librarypack.Track{ID: strconv.Itoa(i), Artist: "Fixture Artist", Title: "Song " + strconv.Itoa(i), MERT: []float32{1, 0, 0}}
	}
	_, err := librarypack.Write(ctx, path, librarypack.Pack{CorpusGeneration: "fixture", MetadataGeneration: "fixture", MERTGeneration: "fixture", MERT: testSpace(), Tracks: tracks}, librarypack.Limits{})
	if err != nil {
		b.Fatal(err)
	}
	manager, err := librarypack.OpenManager(ctx, filepath.Join(root, "managed"), librarypack.Limits{})
	if err != nil {
		b.Fatal(err)
	}
	defer manager.Close()
	staged, err := manager.Stage(ctx, path)
	if err != nil {
		b.Fatal(err)
	}
	if err := BuildIndexes(ctx, staged.Generation(), IndexBuildOptions{Workers: 2, ShardRows: 1024, MaxScratchBytes: 1 << 24}); err != nil {
		b.Fatal(err)
	}
	if err := manager.Activate(ctx, staged); err != nil {
		b.Fatal(err)
	}
	lease, err := manager.Pin()
	if err != nil {
		b.Fatal(err)
	}
	cat, err := Open(lease, Options{SourceID: "benchmark", PageSize: 256})
	if err != nil {
		b.Fatal(err)
	}
	defer cat.Close()
	executor, err := NewExecutor(cat, 4)
	if err != nil {
		b.Fatal(err)
	}
	queries := []Query{{Metadata: &MetadataQuery{Text: "Fixture", Limit: 100}}, {Metadata: &MetadataQuery{Text: "Song", Limit: 100}}, {MERT: &NeighborQuery{SeedID: cat.prefix + "0", Limit: 100}}, {MERT: &NeighborQuery{SeedID: cat.prefix + "1", Limit: 100}}}
	for _, parallel := range []bool{false, true} {
		b.Run("parallel="+strconv.FormatBool(parallel), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				results := make([]QueryResult, len(queries))
				errs := make([]error, len(queries))
				run := func(i int) { results[i], errs[i] = executor.Query(ctx, queries[i]) }
				if parallel {
					searchwork.Run(ctx, len(queries), run)
				} else {
					for i := range queries {
						run(i)
					}
				}
				for _, err := range errs {
					if err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

type fixedCandidates []core.Candidate

func (f fixedCandidates) Retrieve(context.Context, ports.RetrievalRequest) ([]core.Candidate, error) {
	return f, nil
}

func TestMultiplePackOverlapMatchesSerialLayerMerge(t *testing.T) {
	first, manager := openTestCatalog(t, []librarypack.Track{{ID: "one", Artist: "Artist", Title: "Song", ISRC: "USAAA2600001", RecordingIdentity: "isrc:USAAA2600001", MERT: []float32{1, 0, 0}}}, nil)
	defer first.Close()
	lease, err := manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(lease, Options{SourceID: "second", Shared: true})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	inner, err := NewCombinedRetriever(baseRetriever{}, first, ModeCombined, 2)
	if err != nil {
		t.Fatal(err)
	}
	request := ports.RetrievalRequest{Intent: core.MusicIntent{References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Artist", Influence: core.InfluencePositive}}}, Seed: 42}
	firstResult, err := inner.Retrieve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := NewCombinedRetriever(fixedCandidates(firstResult), second, ModeCombined, 2)
	if err != nil {
		t.Fatal(err)
	}
	want, err := serial.Retrieve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	outer, err := NewCombinedRetriever(inner, second, ModeCombined, 2)
	if err != nil {
		t.Fatal(err)
	}
	firstExecutor, err := NewExecutor(first, 2)
	if err != nil {
		t.Fatal(err)
	}
	secondExecutor, err := NewExecutor(second, 2)
	if err != nil {
		t.Fatal(err)
	}
	originalFirst, originalSecond := firstExecutor.metadata, secondExecutor.metadata
	secondDone := make(chan struct{})
	firstExecutor.metadata = func(ctx context.Context, value any) ([]Hit, error) {
		select {
		case <-secondDone:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return originalFirst(ctx, value)
	}
	secondExecutor.metadata = func(ctx context.Context, value any) ([]Hit, error) {
		hits, err := originalSecond(ctx, value)
		close(secondDone)
		return hits, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := outer.Retrieve(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parallel pack merge differs\nwant=%+v\ngot=%+v", want, got)
	}
	if len(got) != 2 || got[1].Track.ID != "local:main:one" || len(got[1].Sources) != 1 {
		t.Fatalf("first pack identity must survive with duplicate generation evidence collapsed: %+v", got)
	}
}
