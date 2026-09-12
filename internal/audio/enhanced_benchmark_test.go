package audio

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

// MPEG-1 zero-data frames are generated in memory; no recorded audio is used.
func BenchmarkEnhancedOriginalDecode(b *testing.B) {
	encoded := bytes.Repeat(syntheticMP3()[:417], 1148)
	probe, err := DecodeOriginalMP3(context.Background(), encoded)
	if err != nil {
		b.Fatal(err)
	}
	seconds := float64(len(probe.Samples)) / float64(probe.SampleRate*probe.Channels)
	clear(probe.Samples)
	b.ReportAllocs()
	b.SetBytes(int64(len(encoded)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pcm, err := DecodeOriginalMP3(context.Background(), encoded)
		if err != nil {
			b.Fatal(err)
		}
		clear(pcm.Samples)
	}
	b.ReportMetric(seconds, "source-sec/op")
}
func benchmarkMERTRecord() core.AudioRepresentation {
	a := storedRepresentation()
	a.Model = mertTestModel()
	a.Pooled = make([]float32, MERTDimension)
	a.Pooled[0] = 1
	a.Segments = nil
	for i := 0; i < 6; i++ {
		v := make([]float32, MERTDimension)
		v[0] = 1
		a.Segments = append(a.Segments, core.AudioRepresentationSegment{StartSeconds: float64(i * 5), EndSeconds: float64((i + 1) * 5), Vector: v})
	}
	a.Coverage.StartSeconds = 0
	a.Coverage.EndSeconds = 30
	a.Coverage.CoveredSeconds = 30
	a.ID = ""
	a.ID = Fingerprint(a)
	return a
}
func BenchmarkEnhancedRepresentationCache(b *testing.B) {
	b.Run("read", func(b *testing.B) {
		store, err := OpenStore(b.TempDir())
		if err != nil {
			b.Fatal(err)
		}
		defer store.Close()
		a := benchmarkMERTRecord()
		ctx := context.Background()
		if err = store.Representations().Put(ctx, a); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, ok, err := store.Representations().Find(ctx, a.CatalogVersion, a.TrackID, a.TrackKey, a.Model)
			if err != nil || !ok {
				b.Fatal(err)
			}
		}
	})
	b.Run("insert", func(b *testing.B) {
		store, err := OpenStore(b.TempDir())
		if err != nil {
			b.Fatal(err)
		}
		defer store.Close()
		a := benchmarkMERTRecord()
		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			a.TrackID = fmt.Sprint(i)
			a.ID = ""
			a.ID = Fingerprint(a)
			if err := store.Representations().Put(ctx, a); err != nil {
				b.Fatal(err)
			}
		}
	})
}
