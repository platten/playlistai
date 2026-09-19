package libraryindex

import (
	"context"
	"testing"
	"time"
)

func TestPartialDSPOutageReleasePreservesCacheAndRetryBudget(t *testing.T) {
	ctx := context.Background()
	state, epoch := newClaimTestState(t, 1)
	metadata, err := state.ClaimScanDiffJobs(ctx, epoch, "metadata", 1, time.Minute)
	if err != nil || len(metadata) != 1 {
		t.Fatalf("metadata=%v %v", metadata, err)
	}
	if err := state.CommitJob(ctx, JobResult{Job: metadata[0], Contract: metadata[0].SemanticKey, Metadata: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	claimed, err := state.ClaimScanDiffJobs(ctx, epoch, "audio", 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("audio=%v %v", claimed, err)
	}
	job := claimed[0]
	dsp := []byte(`{"version":"local-dsp-test","windows":[{"index":0}]}`)
	if err := state.CommitPartialDSP(ctx, job, job.SemanticKey, "dsp-test", dsp); err != nil {
		t.Fatal(err)
	}
	if err := state.ReleaseJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	retry, err := state.ClaimScanDiffJobs(ctx, epoch, "audio", 1, time.Minute)
	if err != nil || len(retry) != 1 {
		t.Fatalf("retry=%v %v", retry, err)
	}
	if retry[0].RetryCount != job.RetryCount || retry[0].Fence == job.Fence {
		t.Fatalf("retry budget/fence changed incorrectly: %+v", retry[0])
	}
	cached, found, err := state.CachedDSP(ctx, job.FileID, job.SourceRevision, "dsp-test")
	if err != nil || !found || string(cached) != string(dsp) {
		t.Fatalf("cached=%s found=%v err=%v", cached, found, err)
	}
	if err := state.CommitPartialDSP(ctx, job, job.SemanticKey, "dsp-test", []byte(`{}`)); err == nil {
		t.Fatal("old outage attempt overwrote DSP")
	}
	if _, found, err := state.CachedDSP(ctx, job.FileID, "different-source", "dsp-test"); err != nil || found {
		t.Fatalf("stale source reused=%v %v", found, err)
	}
}
