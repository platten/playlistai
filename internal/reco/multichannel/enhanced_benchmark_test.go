package multichannel

import (
	"context"
	"fmt"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

// Synthetic policy CPU/allocation measurement; not musical quality or model
// inference latency. Every candidate has compatible cached derived evidence.
func BenchmarkEnhancedPolicy(b *testing.B) {
	for _, count := range []int{24, 128} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			rows := make([]fakes.CatalogTrack, count+1)
			for i := range rows {
				rows[i] = fakes.CatalogTrack{ID: fmt.Sprint(i), Display: fmt.Sprintf("Artist %d - Track", i), Audio: []float32{1, 0}, Track: []float32{1, 0}}
			}
			cat := fakes.NewCatalog(2, rows...)
			input := core.EnhancedAudioInput{CatalogVersion: "benchmark", Model: core.AudioRepresentationIdentity{Model: "MERT", Revision: "v1", Runtime: "onnx", Preprocessing: "pcm", Dimension: 2, Pooling: "mean", WeightsSHA256: "fixture"}, Representations: map[string]core.AudioRepresentation{}, PositiveCentroid: []float32{1, 0}}
			var candidates []core.Candidate
			for i, row := range rows {
				meta, _ := cat.Meta(row.ID)
				input.Representations[row.ID] = core.AudioRepresentation{TrackID: row.ID, TrackKey: core.ProvisionalRecordingKey(meta.Ref), CatalogVersion: input.CatalogVersion, Model: input.Model, Pooled: []float32{1, float32(i % 3)}}
				if i > 0 {
					candidates = append(candidates, core.Candidate{Track: meta.Ref})
				}
			}
			snapshot, err := core.NewEnhancedAudioSnapshot(input)
			if err != nil {
				b.Fatal(err)
			}
			intent := testIntent(count)
			intent.References = nil
			intent.Controls.RecommendationMode = core.EnhancedHybrid
			ranker := NewRanker(cat, DefaultConfig())
			sequencer := NewSequencer(cat, DefaultConfig())
			b.Run("rank", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := ranker.Rank(context.Background(), candidates, ports.RankRequest{Intent: intent, EnhancedAudio: snapshot}); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("sequence", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := sequencer.Sequence(context.Background(), ports.SequenceRequest{Intent: intent, Candidates: candidates, EnhancedAudio: snapshot}); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}
