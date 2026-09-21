package ports

import "context"

// CandidatePrefetcher optionally overlaps raw discovery I/O with local search
// and assessment. Start is nonblocking; Stop cancels and joins all background
// work and must run before snapshots are finalized or catalog pins released.
// Start and candidate consumption belong to the stream's single coordinator.
type CandidatePrefetcher interface {
	StartPrefetch(context.Context)
	StopPrefetch()
}
