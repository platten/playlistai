package multichannel

import (
	"context"
	"errors"
	"math/rand"
	"reflect"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/similarity/brute"
)

type concurrentBackend bool

func (b concurrentBackend) ConcurrentSearch() bool { return bool(b) }

func TestRetrievalJobsCommitInPlanOrder(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("requires two worker slots")
	}
	lastDone := make(chan struct{})
	jobs := []retrievalJob{
		{backend: concurrentBackend(true), run: func(out map[string]*core.Candidate, exploration *[]explorationOption) error {
			<-lastDone
			out["same"] = &core.Candidate{Track: core.TrackRef{ID: "same", Title: "first"}, Sources: []core.RetrievalEvidence{{QueryID: "first"}}}
			*exploration = append(*exploration, explorationOption{queryID: "first"})
			return nil
		}},
		{backend: concurrentBackend(true), run: func(out map[string]*core.Candidate, exploration *[]explorationOption) error {
			out["same"] = &core.Candidate{Track: core.TrackRef{ID: "same", Title: "last"}, Sources: []core.RetrievalEvidence{{QueryID: "last"}}}
			*exploration = append(*exploration, explorationOption{queryID: "last"})
			close(lastDone)
			return nil
		}},
	}
	out := map[string]*core.Candidate{}
	var exploration []explorationOption
	if err := runRetrievalJobs(context.Background(), jobs, out, &exploration); err != nil {
		t.Fatal(err)
	}
	if out["same"].Track.Title != "first" || out["same"].Sources[0].QueryID != "first" || out["same"].Sources[1].QueryID != "last" || exploration[0].queryID != "first" {
		t.Fatalf("out of order: %+v %+v", out["same"], exploration)
	}
}

func TestRetrievalJobsSerialFallbackAndOrderedErrors(t *testing.T) {
	firstErr := errors.New("first")
	secondErr := errors.New("second")
	for _, concurrent := range []bool{false, true} {
		var order []int
		jobs := make([]retrievalJob, 3)
		for i := range jobs {
			jobs[i] = retrievalJob{backend: concurrentBackend(concurrent), run: func(_ map[string]*core.Candidate, _ *[]explorationOption) error {
				if !concurrent {
					order = append(order, i)
				}
				if i == 1 {
					return firstErr
				}
				if i == 2 {
					return secondErr
				}
				return nil
			}}
		}
		var exploration []explorationOption
		err := runRetrievalJobs(context.Background(), jobs, map[string]*core.Candidate{}, &exploration)
		if !errors.Is(err, firstErr) {
			t.Fatalf("error=%v", err)
		}
		if !concurrent && !reflect.DeepEqual(order, []int{0, 1}) {
			t.Fatalf("serial calls=%v", order)
		}
	}
}

// hiddenConcurrency exposes exactly the old serial search contract.
type hiddenConcurrency struct{ ports.SimilarityEngine }

func TestParallelRetrieverEquivalentToSerial(t *testing.T) {
	cat := testCatalog()
	engine := brute.New(cat)
	request := ports.RetrievalRequest{Intent: testIntent(4), RecentSelections: refs(cat, "last"), Seed: 42,
		Profile: core.TasteProfile{Clusters: []core.TasteCluster{{ID: "cluster", Weight: 1, Affinity: core.EmbeddingAffinity{Audio: []float32{1, 0}, Cooccurrence: []float32{0, 1}}}}}}
	serial, err := NewRetriever(cat, hiddenConcurrency{engine}, DefaultConfig()).Retrieve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	parallel, err := NewRetriever(cat, engine, DefaultConfig()).Retrieve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(serial, parallel) {
		t.Fatalf("parallel result differs\nserial=%+v\nparallel=%+v", serial, parallel)
	}
}

type delayedSearch struct {
	ports.SimilarityEngine
	parallel bool
}

func (s delayedSearch) ConcurrentSearch() bool { return s.parallel }
func (s delayedSearch) Search(ctx context.Context, q ports.SimilarityQuery) ([]ports.Match, error) {
	select {
	case <-time.After(2 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.SimilarityEngine.Search(ctx, q)
}

func BenchmarkRetrievalScheduling(b *testing.B) {
	for _, delayed := range []bool{false, true} {
		for _, count := range []int{1, 4} {
			for _, parallel := range []bool{false, true} {
				name := "warm"
				if delayed {
					name = "delayed"
				}
				name += "/references=" + strconv.Itoa(count) + "/parallel=" + strconv.FormatBool(parallel)
				b.Run(name, func(b *testing.B) {
					cat := testCatalog()
					engine := brute.New(cat)
					sim := engine.NewSearchSession()
					if delayed {
						sim = delayedSearch{SimilarityEngine: sim, parallel: parallel}
					} else if !parallel {
						sim = hiddenConcurrency{sim}
					}
					intent := testIntent(4)
					for _, id := range []string{"last", "other", "taste"}[:count-1] {
						intent.References = append(intent.References, core.IntentReference{Kind: core.ReferenceTrack, TrackID: id, Influence: core.InfluencePositive})
					}
					request := ports.RetrievalRequest{Intent: intent, Seed: 42}
					r := NewRetriever(cat, sim, DefaultConfig())
					if _, err := r.Retrieve(context.Background(), request); err != nil {
						b.Fatal(err)
					}
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						if _, err := r.Retrieve(context.Background(), request); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}

// BenchmarkRetrievalScans uses a fixed synthetic 16K x 32 catalog and seed 42.
// Cold means a new request-local prefix cache; warm reuses populated prefixes.
func BenchmarkRetrievalScans(b *testing.B) {
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // reproducible fixture
	rows := make([]fakes.CatalogTrack, 16384)
	for i := range rows {
		audio, track := make([]float32, 32), make([]float32, 32)
		for j := range audio {
			audio[j] = rng.Float32()*2 - 1
			track[j] = rng.Float32()*2 - 1
		}
		id := strconv.Itoa(i)
		rows[i] = fakes.CatalogTrack{ID: id, Display: "Artist " + id + " - Track " + id, Audio: audio, Track: track}
	}
	cat := fakes.NewCatalog(32, rows...)
	engine := brute.New(cat)
	for _, warm := range []bool{false, true} {
		for _, count := range []int{1, 4} {
			for _, parallel := range []bool{false, true} {
				b.Run("warm="+strconv.FormatBool(warm)+"/references="+strconv.Itoa(count)+"/parallel="+strconv.FormatBool(parallel), func(b *testing.B) {
					intent := testIntent(4)
					intent.References = nil
					for i := range count {
						intent.References = append(intent.References, core.IntentReference{Kind: core.ReferenceTrack, TrackID: strconv.Itoa(i), Influence: core.InfluencePositive})
					}
					request := ports.RetrievalRequest{Intent: intent, Seed: 42}
					newRetriever := func() *Retriever {
						sim := engine.NewSearchSession()
						if !parallel {
							sim = hiddenConcurrency{sim}
						}
						return NewRetriever(cat, sim, DefaultConfig())
					}
					retriever := newRetriever()
					if _, err := retriever.Retrieve(context.Background(), request); err != nil {
						b.Fatal(err)
					}
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						if !warm {
							retriever = newRetriever()
						}
						if _, err := retriever.Retrieve(context.Background(), request); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
