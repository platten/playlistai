package brute

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type countedSearch struct {
	ports.SimilarityEngine
	calls int
	fail  bool
}

func (s *countedSearch) Search(ctx context.Context, q ports.SimilarityQuery) ([]ports.Match, error) {
	s.calls++
	if s.fail {
		return nil, context.Canceled
	}
	return s.SimilarityEngine.Search(ctx, q)
}

func sessionFixture(n int) *Engine {
	rng := rand.New(rand.NewSource(42))
	tracks := make([]fakes.CatalogTrack, n)
	for i := range tracks {
		a, c := make([]float32, 4), make([]float32, 4)
		for j := range a {
			a[j], c[j] = rng.Float32()*2-1, rng.Float32()*2-1
		}
		tracks[i] = fakes.CatalogTrack{ID: fmt.Sprint(i), Audio: a, Track: c}
	}
	return New(fakes.NewCatalog(4, tracks...))
}

func TestSearchSessionExactEquivalence(t *testing.T) {
	engine := sessionFixture(400)
	session := engine.NewSearchSession()
	rng := rand.New(rand.NewSource(17))
	q := ports.SimilarityQuery{AudioSum: []float32{1, .5, .2, .1}, TrackSum: []float32{.4, .3, .2, 1}, Weights: [2]float32{.5, .5}}
	for trial := 0; trial < 100; trial++ {
		q.K = []int{0, 1, 32, 127, 256, 8192}[trial%6]
		q.Exclude = map[string]struct{}{}
		for i := 0; i < trial*5; i++ {
			q.Exclude[fmt.Sprint(rng.Intn(500))] = struct{}{}
		}
		if trial%10 == 0 {
			q.AudioSum[0] = rng.Float32() // mutate a caller-owned slice
			q.Weights = [2]float32{rng.Float32(), rng.Float32()}
		}
		if trial == 60 {
			q.TrackSum = nil
		}
		if trial == 80 {
			q.AudioSum = []float32{1} // wrong dimensions retain exact-engine semantics
		}
		want, err := engine.Search(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		got, err := session.Search(context.Background(), q)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: cached search differs from exact: %v", trial, err)
		}
	}
}

func TestSearchSessionReuseExpansionAndIsolation(t *testing.T) {
	engine := sessionFixture(6000)
	session := engine.NewSearchSession().(*searchSession)
	counted := &countedSearch{SimilarityEngine: engine}
	session.engine = counted
	q := ports.SimilarityQuery{AudioSum: []float32{1, .5, .2, .1}, Weights: [2]float32{1, 0}, K: 8}
	first, err := session.Search(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	q.Exclude = map[string]struct{}{first[0].ID: {}}
	first[1].ID = "caller mutation must not alter cache"
	got, err := session.Search(context.Background(), q)
	want, _ := engine.Search(context.Background(), q)
	if err != nil || counted.calls != 1 || !reflect.DeepEqual(got, want) {
		t.Fatalf("reuse/ownership: calls=%d err=%v", counted.calls, err)
	}
	all, _ := engine.Search(context.Background(), ports.SimilarityQuery{AudioSum: q.AudioSum, Weights: q.Weights, K: engine.Len()})
	for _, m := range all[:5000] {
		q.Exclude[m.ID] = struct{}{}
	}
	got, err = session.Search(context.Background(), q)
	want, _ = engine.Search(context.Background(), q)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("bounded prefix fallback changed results: %v", err)
	}
	for _, element := range session.queries {
		if len(element.Value.(*searchPrefix).matches) > sessionMaxMatches {
			t.Fatal("unbounded cache")
		}
	}
	for i := 0; i < sessionQueries+4; i++ {
		q.AudioSum[0] = float32(i)
		q.Exclude = nil
		if _, err := session.Search(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	if len(session.queries) != sessionQueries || session.lru.Len() != sessionQueries {
		t.Fatal("query cache not bounded")
	}
	other := engine.NewSearchSession().(*searchSession)
	if len(other.queries) != 0 {
		t.Fatal("generation cache leaked across requests")
	}
}

func TestSearchSessionCancellationAndFailedFill(t *testing.T) {
	engine := sessionFixture(100)
	session := engine.NewSearchSession().(*searchSession)
	counted := &countedSearch{SimilarityEngine: engine, fail: true}
	session.engine = counted
	q := ports.SimilarityQuery{AudioSum: []float32{1, 0, 0, 0}, Weights: [2]float32{1, 0}, K: 10}
	if _, err := session.Search(context.Background(), q); !errors.Is(err, context.Canceled) {
		t.Fatal("search error lost", err)
	}
	counted.fail = false
	if got, err := session.Search(context.Background(), q); err != nil || len(got) != 10 || counted.calls != 2 {
		t.Fatal("failed fill poisoned cache", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := session.Search(ctx, q); !errors.Is(err, context.Canceled) || counted.calls != 2 {
		t.Fatal("cache hit ignored cancellation", err)
	}
}

func TestSearchSessionParallelIsolation(t *testing.T) {
	engine := sessionFixture(400)
	for i := range 4 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			session := engine.NewSearchSession()
			q := ports.SimilarityQuery{AudioSum: []float32{1, float32(i), .5, .1}, Weights: [2]float32{1, 0}, K: 20, Exclude: map[string]struct{}{}}
			for step := 0; step <= engine.Len(); step++ {
				q.Exclude[fmt.Sprint(step)] = struct{}{}
				got, err := session.Search(context.Background(), q)
				want, _ := engine.Search(context.Background(), q)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("step %d: changed exact results or exhaustion: %v", step, err)
				}
			}
		})
	}
}
