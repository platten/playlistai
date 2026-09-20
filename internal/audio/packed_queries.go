package audio

import (
	"context"
	"fmt"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// EncodeClauseQueries uses the same reviewed captions as preview comparisons.
// Every variant must succeed; failures never silently change an ensemble.
// Its request-local cache is discarded with the generation, not shared history.
func EncodeClauseQueries(ctx context.Context, analyzer ports.AudioAnalyzer, intent core.MusicIntent) ([]core.AudioClauseVector, error) {
	if analyzer == nil {
		return nil, nil
	}
	clauses := Clauses(intent)
	if len(clauses) > 64 {
		return nil, fmt.Errorf("audio: too many clauses for packed text retrieval")
	}
	cache := map[string][]float32{}
	out := make([]core.AudioClauseVector, 0, len(clauses))
	for _, clause := range clauses {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch clause.Kind {
		case "genre", "style", "mood", "instrumentation", "texture", "vocal", "description":
		default:
			continue
		}
		texts := ClauseQueries(clause)
		var mean []float32
		for _, text := range texts {
			vector, ok := cache[text]
			if !ok {
				var err error
				vector, err = analyzer.EmbedText(ctx, text)
				if err != nil {
					return nil, err
				}
				if !validVector(vector, analyzer.Identity().Dimension) {
					return nil, fmt.Errorf("audio: incompatible packed text query")
				}
				cache[text] = append([]float32(nil), vector...)
			}
			if mean == nil {
				mean = make([]float32, len(vector))
			}
			for i, value := range vector {
				mean[i] += value / float32(len(texts))
			}
		}
		if len(mean) > 0 {
			out = append(out, core.AudioClauseVector{Clause: clause, Values: mean})
		}
	}
	return out, nil
}
