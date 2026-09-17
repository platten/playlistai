package libraryindex

import "errors"

// ErrShutdownRequested is returned after new work admission has stopped and
// all already-admitted work has drained. Callers should close durable state and
// exit as interrupted; pending frontier and job rows remain resumable.
var ErrShutdownRequested = errors.New("library indexer: graceful shutdown requested")

func shutdownRequested(stop <-chan struct{}) bool {
	if stop == nil {
		return false
	}
	select {
	case <-stop:
		return true
	default:
		return false
	}
}
