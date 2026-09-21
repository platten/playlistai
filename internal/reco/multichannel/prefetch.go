package multichannel

import (
	"context"

	"github.com/platten/playlistai/internal/ports"
)

// The prefetch context stops independently of the generation context: an
// explicit stop retains accepted tracks, whereas parent cancellation discards
// the generation. Always join both the stream and the stop watcher on return.
func startCandidatePrefetch(parent context.Context, stream ports.MusicCandidateStream, stop <-chan struct{}) func() {
	prefetch, ok := stream.(ports.CandidatePrefetcher)
	if !ok {
		return func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	prefetch.StartPrefetch(ctx)
	return func() {
		cancel()
		prefetch.StopPrefetch()
		<-done
	}
}
