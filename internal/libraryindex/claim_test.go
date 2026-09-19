package libraryindex

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func newClaimTestState(t testing.TB, files int) (*State, int64) {
	t.Helper()
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "claim-test", 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	})
	root, err := state.EnsureRoot(ctx, t.TempDir(), "music")
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := state.BeginEpoch(ctx, []Root{root})
	if err != nil {
		t.Fatal(err)
	}
	semantic := map[string]string{"metadata": "probe/v1", "audio": "audio/v1"}
	for start := 0; start < files; start += 256 {
		chunk := make([]SourceFile, 0, 256)
		for index := start; index < min(files, start+256); index++ {
			chunk = append(chunk, SourceFile{RootID: root.ID, RelativePath: fmt.Sprintf("track-%06d.flac", index), Size: int64(index + 1), MTimeNS: 1, Extension: ".flac"})
		}
		if _, err := state.ObserveFiles(ctx, epoch, chunk, semantic); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := state.WriteScanManifest(ctx, epoch, semantic); err != nil {
		t.Fatal(err)
	}
	return state, epoch
}

// setClaimTestStates bypasses leases to place a large manifest into the state
// reached after part of a run.
func setClaimTestStates(t testing.TB, state *State, statements ...string) {
	t.Helper()
	ctx := context.Background()
	if err := state.write(ctx, true, func(conn *sql.Conn) error {
		for _, statement := range statements {
			if _, err := conn.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func jobIDs(jobs []Job) []int64 {
	ids := make([]int64, len(jobs))
	for i, job := range jobs {
		ids[i] = job.ID
	}
	return ids
}

func TestScanDiffClaimsSkipFinishedPrefixAndGateAudio(t *testing.T) {
	ctx := context.Background()
	state, epoch := newClaimTestState(t, 8)
	// Files 0-4 have metadata; audio for 0-1 is finished, 2 holds an expired
	// lease, and 5-7 remain blocked on metadata.
	setClaimTestStates(t, state,
		`UPDATE jobs SET state='completed' WHERE kind='metadata' AND file_id IN (SELECT id FROM files WHERE relative_path<'track-000005.flac')`,
		`UPDATE jobs SET state='completed' WHERE kind='audio' AND file_id IN (SELECT id FROM files WHERE relative_path<'track-000002.flac')`,
		`UPDATE jobs SET state='leased',fence='old',lease_until='2000-01-01T00:00:00Z' WHERE kind='audio' AND file_id=(SELECT id FROM files WHERE relative_path='track-000002.flac')`)
	var want []int64
	rows, err := state.reader.QueryContext(ctx, `SELECT j.id FROM jobs j JOIN files f ON f.id=j.file_id
		WHERE j.kind='audio' AND f.relative_path BETWEEN 'track-000002.flac' AND 'track-000004.flac' ORDER BY j.id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		want = append(want, id)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	pending, leased, err := state.ScanDiffJobCounts(ctx, epoch, "audio")
	if err != nil || pending != 5 || leased != 1 {
		t.Fatalf("audio counts pending=%d leased=%d err=%v", pending, leased, err)
	}
	first, err := state.ClaimScanDiffJobs(ctx, epoch, "audio", 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	rest, err := state.ClaimScanDiffJobs(ctx, epoch, "audio", 64, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got := append(jobIDs(first), jobIDs(rest)...)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("claimed audio %v, want eligible jobs %v in id order", got, want)
	}
	for _, job := range append(first, rest...) {
		if job.Fence == "" || job.Fence == "old" || job.Attempt != 1 {
			t.Fatalf("claimed job was not freshly leased: %+v", job)
		}
	}
	if more, err := state.ClaimScanDiffJobs(ctx, epoch, "audio", 64, time.Minute); err != nil || len(more) != 0 {
		t.Fatalf("blocked audio claimed: %+v err=%v", more, err)
	}
	pending, leased, err = state.ScanDiffJobCounts(ctx, epoch, "audio")
	if err != nil || pending != 3 || leased != 3 {
		t.Fatalf("audio counts after claim pending=%d leased=%d err=%v", pending, leased, err)
	}

	if err := state.FinalizeBlockedScanDiffAudio(ctx, epoch); err != nil {
		t.Fatal(err)
	}
	var failed, stillLeased int
	if err := state.reader.QueryRowContext(ctx, `SELECT COALESCE(SUM(state='failed' AND error_code='metadata_unavailable'),0),COALESCE(SUM(state='leased'),0)
		FROM jobs WHERE kind='audio'`).Scan(&failed, &stillLeased); err != nil {
		t.Fatal(err)
	}
	if failed != 3 || stillLeased != 3 {
		t.Fatalf("finalize failed=%d leased=%d, want only the 3 blocked audio jobs failed", failed, stillLeased)
	}
}

func TestLeaseCandidatesSkipsCandidatesChangedAfterSelection(t *testing.T) {
	ctx := context.Background()
	state, epoch := newClaimTestState(t, 3)
	candidates, err := state.claimCandidates(ctx, "metadata", epoch, time.Now().UTC(), 3)
	if err != nil || len(candidates) != 3 {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
	// Another claimant leases the first candidate after the read snapshot.
	winner, err := state.leaseCandidates(ctx, candidates[:1], time.Minute)
	if err != nil || len(winner) != 1 {
		t.Fatalf("winner=%+v err=%v", winner, err)
	}
	leased, err := state.leaseCandidates(ctx, candidates, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(jobIDs(leased)) != fmt.Sprint(jobIDs(candidates[1:])) {
		t.Fatalf("leased %v, want only unchanged candidates %v", jobIDs(leased), jobIDs(candidates[1:]))
	}
	var fence string
	if err := state.reader.QueryRowContext(ctx, `SELECT fence FROM jobs WHERE id=?`, winner[0].ID).Scan(&fence); err != nil {
		t.Fatal(err)
	}
	if fence != winner[0].Fence {
		t.Fatal("stale candidate overwrote the winning lease")
	}
}

func TestReleaseJobRequeuesWithoutChargingARetry(t *testing.T) {
	ctx := context.Background()
	state, epoch := newClaimTestState(t, 1)
	claimed, err := state.ClaimScanDiffJobs(ctx, epoch, "metadata", 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if err := state.ReleaseJob(ctx, claimed[0]); err != nil {
		t.Fatal(err)
	}
	if err := state.ReleaseJob(ctx, claimed[0]); err == nil {
		t.Fatal("stale fence released the job twice")
	}
	again, err := state.ClaimScanDiffJobs(ctx, epoch, "metadata", 1, time.Minute)
	if err != nil || len(again) != 1 || again[0].ID != claimed[0].ID {
		t.Fatalf("released job was not claimable: %+v err=%v", again, err)
	}
	if again[0].RetryCount != 0 || again[0].Attempt != claimed[0].Attempt+1 {
		t.Fatalf("release charged a retry: before=%+v after=%+v", claimed[0], again[0])
	}
}
