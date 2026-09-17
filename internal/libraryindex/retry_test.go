package libraryindex

import (
	"context"
	"errors"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/localaudio"
)

func TestTransientAnalysisRetryClassificationIsBounded(t *testing.T) {
	for _, err := range []error{localaudio.ErrSourceChanged, audio.ErrNativeWorker, context.DeadlineExceeded} {
		if !retryableAnalysisError(context.Background(), err, 0) || retryableAnalysisError(context.Background(), err, 2) {
			t.Fatalf("unexpected retry policy for %v", err)
		}
	}
	if retryableAnalysisError(context.Background(), errors.New("corrupt media"), 0) {
		t.Fatal("permanent media error was retried")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if retryableAnalysisError(canceled, audio.ErrNativeWorker, 0) {
		t.Fatal("shutdown scheduled a retry")
	}
	if got := classifyAnalysisError(audio.ErrNativeWorker); got != "native_worker_transient" {
		t.Fatalf("classification=%q", got)
	}
}
