package brute

import (
	"container/list"
	"context"
	"encoding/binary"
	"math"

	"github.com/platten/playlistai/internal/ports"
)

const sessionQueries = 64
const sessionMaxMatches = 4096

// Exact ordering is independent of K and exclusions. A cached global prefix
// therefore gives precisely the same top-K after filtering whenever enough
// survivors remain. Otherwise expand the prefix or fall back to exact search.
// This optimization is deliberately owned by the exact backend, not assumed
// of arbitrary approximate implementations of SimilarityEngine.
type searchSession struct {
	engine  ports.SimilarityEngine
	queries map[string]*list.Element
	lru     list.List
}

type searchPrefix struct {
	key       string
	matches   []ports.Match
	limit     int
	exhausted bool
}

func (e *Engine) NewSearchSession() ports.SimilarityEngine {
	return &searchSession{engine: e, queries: make(map[string]*list.Element)}
}

func (s *searchSession) Len() int { return s.engine.Len() }

func (s *searchSession) Search(ctx context.Context, q ports.SimilarityQuery) ([]ports.Match, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	k := q.K
	if k <= 0 {
		k = 20
	}
	k = min(k, s.Len())
	if k == 0 || k > sessionMaxMatches {
		return s.engine.Search(ctx, q)
	}
	key := searchKey(q)
	element := s.queries[key]
	if element != nil {
		s.lru.MoveToFront(element)
	} else {
		if s.lru.Len() == sessionQueries {
			oldest := s.lru.Back()
			delete(s.queries, oldest.Value.(*searchPrefix).key)
			s.lru.Remove(oldest)
		}
		element = s.lru.PushFront(&searchPrefix{key: key})
		s.queries[key] = element
	}
	prefix := element.Value.(*searchPrefix)
	for {
		matches := make([]ports.Match, 0, min(k, len(prefix.matches)))
		for i, match := range prefix.matches {
			if i&255 == 0 && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if _, excluded := q.Exclude[match.ID]; !excluded {
				matches = append(matches, match)
				if len(matches) == k {
					break
				}
			}
		}
		if len(matches) == k || prefix.exhausted {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return matches, nil
		}
		if prefix.limit >= sessionMaxMatches {
			return s.engine.Search(ctx, q) // bound memory even for large exclusion sets
		}
		limit := min(s.Len(), sessionMaxMatches, max(128, 2*k, 2*prefix.limit))
		expanded := q
		expanded.K, expanded.Exclude = limit, nil
		found, err := s.engine.Search(ctx, expanded)
		if err != nil {
			return nil, err // failed/canceled work never becomes a complete prefix
		}
		prefix.matches, prefix.limit = found, limit
		prefix.exhausted = len(found) < limit || limit == s.Len()
	}
}

// Own the exact vector/weight bits, including dimensions, without retaining
// caller slices. K/exclusions change which prefix survivors are needed, not
// the underlying scores. The session's engine fixes the catalog/version.
func searchKey(q ports.SimilarityQuery) string {
	raw := make([]byte, 0, 24+4*(len(q.AudioSum)+len(q.TrackSum)))
	for _, weight := range q.Weights {
		raw = binary.LittleEndian.AppendUint32(raw, math.Float32bits(weight))
	}
	for _, vector := range [][]float32{q.AudioSum, q.TrackSum} {
		raw = binary.LittleEndian.AppendUint64(raw, uint64(len(vector)))
		for _, value := range vector {
			raw = binary.LittleEndian.AppendUint32(raw, math.Float32bits(value))
		}
	}
	return string(raw)
}
