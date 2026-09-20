package audio

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

type packedQueryAnalyzer struct {
	testAnalyzer
	queries  []string
	failText string
}

func (a *packedQueryAnalyzer) EmbedText(_ context.Context, text string) ([]float32, error) {
	a.queries = append(a.queries, text)
	if text == a.failText {
		return nil, errors.New("synthetic unavailable")
	}
	if strings.HasPrefix(text, "Music") {
		return []float32{0, 1}, nil
	}
	return []float32{1, 0}, nil
}

func TestPackedQueryEnsemblePreservesMeanAndIntent(t *testing.T) {
	a := &packedQueryAnalyzer{testAnalyzer: testAnalyzer{model: core.AudioModelIdentity{Dimension: 2}}}
	intent := core.MusicIntent{EssentialCriteria: []core.MusicalCriterion{{Kind: "instrumentation", Value: "piano", Scope: "playlist"}}}
	got, err := EncodeClauseQueries(context.Background(), a, intent)
	if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0].Values, []float32{.5, .5}) {
		t.Fatalf("mean normalized or query lost: %+v %v", got, err)
	}
	if len(a.queries) != 2 || got[0].Clause.Text != "piano" {
		t.Fatalf("unexpected queries %+v", a.queries)
	}
	a.failText = a.queries[1]
	if _, err := EncodeClauseQueries(context.Background(), a, intent); err == nil {
		t.Fatal("partial ensemble accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := EncodeClauseQueries(ctx, a, intent); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestInstrumentProminenceChangesCaptionNotStrictPolicy(t *testing.T) {
	plain := core.AudioClause{Kind: "instrumentation", Text: "piano"}
	prominent := plain
	prominent.Degree = "mostly"
	reduced := plain
	reduced.Degree = "reduced"
	if reflect.DeepEqual(ClauseQueries(plain), ClauseQueries(prominent)) || reflect.DeepEqual(ClauseQueries(prominent), ClauseQueries(reduced)) {
		t.Fatal("prominence lost")
	}
	prominent.Strict = true
	if !reflect.DeepEqual(ClauseQueries(prominent), []string{"piano"}) {
		t.Fatal("strict calibrated query changed")
	}
}
