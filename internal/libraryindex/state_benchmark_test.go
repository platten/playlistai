package libraryindex

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func BenchmarkObserveFiles(b *testing.B) {
	const chunkSize = 256
	semantic := map[string]string{"metadata": "probe/v1", "audio": "audio/v1"}

	b.Run("individual", func(b *testing.B) {
		state, root, epoch := newObservationBenchmarkState(b)
		b.ResetTimer()
		started := time.Now()
		for index := 0; index < b.N; index++ {
			if _, err := state.ObserveFile(context.Background(), epoch, SourceFile{
				RootID: root.ID, RelativePath: fmt.Sprintf("individual-%09d.flac", index), Size: int64(index + 1), MTimeNS: 1, Extension: ".flac",
			}, semantic); err != nil {
				b.Fatal(err)
			}
		}
		elapsed := time.Since(started)
		b.StopTimer()
		b.ReportMetric(float64(elapsed.Nanoseconds())/float64(max(1, b.N)), "ns/file")
	})

	b.Run("directory_chunk", func(b *testing.B) {
		state, root, epoch := newObservationBenchmarkState(b)
		b.ResetTimer()
		started := time.Now()
		for iteration := 0; iteration < b.N; iteration++ {
			files := make([]SourceFile, chunkSize)
			for offset := range files {
				index := iteration*chunkSize + offset
				files[offset] = SourceFile{
					RootID: root.ID, RelativePath: fmt.Sprintf("chunk-%09d.flac", index), Size: int64(index + 1), MTimeNS: 1, Extension: ".flac",
				}
			}
			if _, err := state.ObserveFiles(context.Background(), epoch, files, semantic); err != nil {
				b.Fatal(err)
			}
		}
		elapsed := time.Since(started)
		b.StopTimer()
		b.ReportMetric(float64(elapsed.Nanoseconds())/float64(max(1, b.N*chunkSize)), "ns/file")
	})
}

func newObservationBenchmarkState(b *testing.B) (*State, Root, int64) {
	b.Helper()
	ctx := context.Background()
	state, err := OpenState(ctx, b.TempDir(), "observe-files-benchmark", 1)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := state.Close(); err != nil {
			b.Error(err)
		}
	})
	root, err := state.EnsureRoot(ctx, b.TempDir(), "music")
	if err != nil {
		b.Fatal(err)
	}
	epoch, err := state.BeginEpoch(ctx, []Root{root})
	if err != nil {
		b.Fatal(err)
	}
	return state, root, epoch
}
