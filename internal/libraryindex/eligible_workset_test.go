package libraryindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestEligibleWorksetMetadataAndRetryTransitions(t *testing.T) {
	ctx := context.Background()
	state, epoch := newClaimTestState(t, 1)
	claim := func(kind string, want int) []Job {
		t.Helper()
		jobs, err := state.ClaimScanDiffJobs(ctx, epoch, kind, 1, time.Minute)
		if err != nil || len(jobs) != want {
			t.Fatalf("claim %s: jobs=%+v want=%d err=%v", kind, jobs, want, err)
		}
		return jobs
	}
	claim("audio", 0)
	metadata := claim("metadata", 1)[0]
	if err := state.CommitJob(ctx, JobResult{Job: metadata, Contract: "probe/v1", Metadata: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	audio := claim("audio", 1)[0]
	if err := state.FailJob(ctx, audio, "test", "temporary", true); err != nil {
		t.Fatal(err)
	}
	audio = claim("audio", 1)[0]
	if audio.RetryCount != 1 {
		t.Fatalf("retry count=%d", audio.RetryCount)
	}
	if err := state.FailJob(ctx, audio, "test", "terminal", false); err != nil {
		t.Fatal(err)
	}
	claim("audio", 0)
	if n, err := state.RetryFailed(ctx); err != nil || n != 1 {
		t.Fatalf("retry failed: n=%d err=%v", n, err)
	}
	audio = claim("audio", 1)[0]
	if err := state.CommitJob(ctx, JobResult{Job: audio, Contract: "audio/v1"}); err != nil {
		t.Fatal(err)
	}
	claim("audio", 0)
}

func TestEligibleWorksetRechecksDependencyAndManifestAtLease(t *testing.T) {
	ctx := context.Background()
	state, epoch := newClaimTestState(t, 1)
	setClaimTestStates(t, state, `UPDATE jobs SET state='completed' WHERE kind='metadata'`)
	candidates, err := state.claimCandidates(ctx, "audio", epoch, time.Now().UTC(), 1)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
	setClaimTestStates(t, state, `UPDATE jobs SET state='superseded' WHERE kind='metadata'`)
	if jobs, err := state.leaseCandidates(ctx, candidates, time.Minute); err != nil || len(jobs) != 0 {
		t.Fatalf("lost prerequisite was leased: %+v err=%v", jobs, err)
	}
	setClaimTestStates(t, state, `UPDATE jobs SET state='completed' WHERE kind='metadata'`, `DELETE FROM scan_diff_jobs`)
	if jobs, err := state.leaseCandidates(ctx, candidates, time.Minute); err != nil || len(jobs) != 0 {
		t.Fatalf("removed manifest was leased: %+v err=%v", jobs, err)
	}
	if jobs, err := state.ClaimJobs(ctx, "audio", 1, time.Minute); err != nil || len(jobs) != 1 {
		t.Fatalf("unscoped work disappeared with manifest: %+v err=%v", jobs, err)
	}
}

func TestEligibleWorksetRebuildsAfterRestart(t *testing.T) {
	ctx := context.Background()
	state, epoch := newClaimTestState(t, 2)
	claimed, err := state.ClaimScanDiffJobs(ctx, epoch, "metadata", 1, time.Hour)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	// The derived table is disposable. Durable jobs alone must recover it.
	setClaimTestStates(t, state, `DELETE FROM eligible_jobs`)
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenState(ctx, state.dir, "workset-restart", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	jobs, err := reopened.ClaimScanDiffJobs(ctx, epoch, "metadata", 2, time.Minute)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("recovered jobs=%+v err=%v", jobs, err)
	}
	if jobs[0].ID != claimed[0].ID || jobs[0].Fence == claimed[0].Fence || jobs[0].Attempt != claimed[0].Attempt+1 {
		t.Fatalf("recovered lease did not fence old owner: old=%+v new=%+v", claimed[0], jobs[0])
	}
}

func TestEligibleWorksetRollsBackWithDurableTransition(t *testing.T) {
	ctx := context.Background()
	state, epoch := newClaimTestState(t, 1)
	rollback := errors.New("intentional rollback")
	err := state.writeBatch(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state='completed' WHERE kind='metadata'`); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if jobs, err := state.ClaimScanDiffJobs(ctx, epoch, "audio", 1, time.Minute); err != nil || len(jobs) != 0 {
		t.Fatalf("rolled-back dependency leaked: %+v err=%v", jobs, err)
	}
	if jobs, err := state.ClaimScanDiffJobs(ctx, epoch, "metadata", 1, time.Minute); err != nil || len(jobs) != 1 {
		t.Fatalf("rollback removed pending metadata: %+v err=%v", jobs, err)
	}
}

func TestEligibleWorksetPreservesManifestRevision(t *testing.T) {
	ctx := context.Background()
	state, epoch := newClaimTestState(t, 1)
	setClaimTestStates(t, state, `UPDATE jobs SET source_revision='replacement'`)
	if jobs, err := state.ClaimScanDiffJobs(ctx, epoch, "metadata", 1, time.Minute); err != nil || len(jobs) != 0 {
		t.Fatalf("replacement revision entered old manifest: %+v err=%v", jobs, err)
	}
	if jobs, err := state.ClaimJobs(ctx, "metadata", 1, time.Minute); err != nil || len(jobs) != 1 || jobs[0].SourceRevision != "replacement" {
		t.Fatalf("replacement revision missing globally: %+v err=%v", jobs, err)
	}
}

func TestEligibleWorksetExcludesUnrelatedAndBlockedWork(t *testing.T) {
	ctx := context.Background()
	state, epoch := newClaimTestState(t, 64)
	// Only the final track's audio becomes ready; all other audio remains blocked.
	setClaimTestStates(t, state, `UPDATE jobs SET state='completed' WHERE kind='metadata' AND file_id=(SELECT id FROM files ORDER BY relative_path DESC LIMIT 1)`)
	var eligible int
	if err := state.reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM eligible_jobs WHERE epoch_id=? AND kind='audio' AND ready=1`, epoch).Scan(&eligible); err != nil || eligible != 1 {
		t.Fatalf("eligible=%d err=%v", eligible, err)
	}
	setClaimTestStates(t, state, fmt.Sprintf(`DELETE FROM scan_diff_jobs WHERE epoch_id=%d AND kind='metadata'`, epoch))
	if jobs, err := state.ClaimScanDiffJobs(ctx, epoch, "metadata", 64, time.Minute); err != nil || len(jobs) != 0 {
		t.Fatalf("unrelated global work entered epoch: %+v err=%v", jobs, err)
	}
	if jobs, err := state.ClaimScanDiffJobs(ctx, epoch, "audio", 64, time.Minute); err != nil || len(jobs) != 1 {
		t.Fatalf("eligible audio=%+v err=%v", jobs, err)
	}
}
