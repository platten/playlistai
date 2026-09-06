// Package brute is a brute-force blended-cosine similarity engine over a
// ports.Catalog. It mirrors deej-ai.online-app's most_similar: a per-track score
// that is a weighted sum of the cosine similarity in each of the two embedding
// spaces (audio-content, playlist co-occurrence), with the query sums left
// un-normalized and the catalog rows normalized.
//
// It holds no float32 copy of the catalog — it reads int8 rows via RawRow at
// query time and folds a precomputed per-row inverse norm into the score. The
// engine's lifetime must not outlast the catalog it was built from.
package brute

import (
	"container/heap"
	"context"
	"math"

	"github.com/platten/playlistai/internal/ports"
)

const dequantScale = 1.0 / 127.0

// Engine implements ports.SimilarityEngine.
type Engine struct {
	cat ports.Catalog
	n   int
	d   int
	// invNorm[row*2+0] = 1/L2(dequant(audio row)); +1 = track row.
	invNorm []float32
}

// New builds an engine over cat, precomputing per-row inverse norms in one pass.
func New(cat ports.Catalog) *Engine {
	n, d := cat.Len(), cat.Dim()
	e := &Engine{cat: cat, n: n, d: d, invNorm: make([]float32, n*2)}
	for row := 0; row < n; row++ {
		a, t, ok := cat.RawRow(row)
		if !ok {
			continue
		}
		e.invNorm[row*2] = invNormI8(a)
		e.invNorm[row*2+1] = invNormI8(t)
	}
	return e
}

// Len implements ports.SimilarityEngine.
func (e *Engine) Len() int { return e.n }

// Search implements ports.SimilarityEngine. Results are score-descending with
// ties broken by ascending row, so the ordering is deterministic.
func (e *Engine) Search(ctx context.Context, q ports.SimilarityQuery) ([]ports.Match, error) {
	if e.n == 0 {
		return nil, nil
	}
	k := q.K
	if k <= 0 {
		k = 20
	}
	if k > e.n {
		k = e.n
	}

	useAudio := len(q.AudioSum) == e.d && q.Weights[0] != 0
	useTrack := len(q.TrackSum) == e.d && q.Weights[1] != 0
	w0, w1 := q.Weights[0], q.Weights[1]

	excludeRows := make(map[int]struct{}, len(q.Exclude))
	for id := range q.Exclude {
		if r, ok := e.cat.RowOf(id); ok {
			excludeRows[r] = struct{}{}
		}
	}

	h := make(worstFirst, 0, k)
	for row := 0; row < e.n; row++ {
		if row&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if _, skip := excludeRows[row]; skip {
			continue
		}
		a, t, ok := e.cat.RawRow(row)
		if !ok {
			continue
		}

		var score float32
		if useAudio {
			score += w0 * dotF32I8(q.AudioSum, a) * dequantScale * e.invNorm[row*2]
		}
		if useTrack {
			score += w1 * dotF32I8(q.TrackSum, t) * dequantScale * e.invNorm[row*2+1]
		}

		if len(h) < k {
			heap.Push(&h, ports.Match{ID: e.cat.ID(row), Row: row, Score: score})
		} else if betterThan(score, row, h[0]) {
			h[0] = ports.Match{ID: e.cat.ID(row), Row: row, Score: score}
			heap.Fix(&h, 0)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	out := make([]ports.Match, len(h))
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = heap.Pop(&h).(ports.Match)
	}
	return out, nil
}

// betterThan reports whether (score,row) should displace the current worst m.
func betterThan(score float32, row int, m ports.Match) bool {
	if score != m.Score {
		return score > m.Score
	}
	return row < m.Row
}

func dotF32I8(q []float32, v []int8) float32 {
	var s float32
	for i := range v {
		s += q[i] * float32(v[i])
	}
	return s
}

func invNormI8(v []int8) float32 {
	var s float64
	for _, x := range v {
		f := float64(x) * dequantScale
		s += f * f
	}
	if s == 0 {
		return 0
	}
	return float32(1.0 / math.Sqrt(s))
}

// worstFirst is a bounded min-heap keyed so the root is the *worst* of the
// current top-k: lowest score, and on a tie the highest row.
type worstFirst []ports.Match

func (h worstFirst) Len() int { return len(h) }
func (h worstFirst) Less(i, j int) bool {
	if h[i].Score != h[j].Score {
		return h[i].Score < h[j].Score
	}
	return h[i].Row > h[j].Row
}
func (h worstFirst) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *worstFirst) Push(x any)   { *h = append(*h, x.(ports.Match)) }
func (h *worstFirst) Pop() any {
	old := *h
	n := len(old)
	m := old[n-1]
	*h = old[:n-1]
	return m
}

var _ ports.SimilarityEngine = (*Engine)(nil)
