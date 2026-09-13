package audio

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
)

type parallelTestAnalyzer struct {
	active  atomic.Int32
	peak    atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (*parallelTestAnalyzer) Identity() core.AudioModelIdentity {
	return core.AudioModelIdentity{Model: "parallel-fixture", Revision: "1", Preprocessing: PreprocessingVersion, Runtime: "fixture", Dimension: 2}
}
func (*parallelTestAnalyzer) Parallelism() int { return 4 }
func (a *parallelTestAnalyzer) EmbedAudio(ctx context.Context, _ []float32) ([]float32, error) {
	active := a.active.Add(1)
	defer a.active.Add(-1)
	for peak := a.peak.Load(); active > peak && !a.peak.CompareAndSwap(peak, active); peak = a.peak.Load() {
	}
	a.entered <- struct{}{}
	select {
	case <-a.release:
		return []float32{1, 0}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (*parallelTestAnalyzer) EmbedText(context.Context, string) ([]float32, error) {
	return []float32{1, 0}, nil
}

type parallelTestResolver struct{ address string }

func (r parallelTestResolver) ResolveAudioPreview(_ context.Context, track core.TrackRef, _ core.EnrichedTrack) (core.ResolvedAudioPreview, error) {
	return core.ResolvedAudioPreview{URL: r.address, Identity: core.PreviewIdentity{Provider: "deezer", ProviderID: track.ID, Status: core.ResolutionResolved}}, nil
}

func TestSessionCheckManyUsesAnalyzerParallelismAndPreservesOrder(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(syntheticMP3()) }))
	defer server.Close()
	analyzer := &parallelTestAnalyzer{entered: make(chan struct{}, 4), release: make(chan struct{})}
	service := &Service{
		Resolver: parallelTestResolver{server.URL}, Analyzer: analyzer, Store: store, Authorized: true, ParityValidated: true,
		Policy:          Policy{Version: "fixture", DevelopmentSet: "synthetic", MinimumPositive: .6, MaximumNegative: .2},
		AllowPreviewURL: func(u *url.URL) bool { return u.String() == server.URL },
	}
	session, err := service.Begin(context.Background(), core.MusicIntent{Controls: core.IntentControls{TotalTrackCount: 4}}.Normalized(), "catalog", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tracks := []core.TrackRef{{ID: "one"}, {ID: "two"}, {ID: "three"}, {ID: "four"}}
	done := make(chan error, 1)
	go func() {
		results, errs := session.CheckMany(context.Background(), tracks, false)
		var err error
		for _, checkErr := range errs {
			if checkErr != nil {
				err = checkErr
				break
			}
		}
		if err == nil {
			for index := range tracks {
				if results[index].TrackID != tracks[index].ID {
					err = fmt.Errorf("result %d is for %q, want %q", index, results[index].TrackID, tracks[index].ID)
					break
				}
			}
		}
		done <- err
	}()
	for range tracks {
		select {
		case <-analyzer.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("candidate analysis remained serial")
		}
	}
	close(analyzer.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if analyzer.peak.Load() != 4 {
		t.Fatalf("peak parallelism = %d", analyzer.peak.Load())
	}
}

func TestAnalysisParallelismBoundsCPUAndMemoryUse(t *testing.T) {
	for processors, want := range map[int]int{1: 1, 2: 1, 3: 1, 4: 2, 6: 3, 8: 4, 64: 4} {
		if got := analysisParallelism(processors); got != want {
			t.Fatalf("processors=%d: got %d, want %d", processors, got, want)
		}
	}
}
