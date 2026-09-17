package libraryindex

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/audio"
)

func TestStateMigratesV1DirectoryFrontierForResumableRescans(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "library-index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `
		CREATE TABLE state_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO state_meta(key,value) VALUES('schema_version','1');
		CREATE TABLE directory_frontier (
			epoch_id INTEGER NOT NULL, root_id TEXT NOT NULL, relative_path TEXT NOT NULL,
			state TEXT NOT NULL, attempt INTEGER NOT NULL DEFAULT 0, fence TEXT NOT NULL DEFAULT '',
			lease_until TEXT, error TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(epoch_id,root_id,relative_path)
		);`)
	if closeErr := db.Close(); err != nil || closeErr != nil {
		t.Fatalf("prepare v1 state: %v close=%v", err, closeErr)
	}
	state, err := OpenState(ctx, dir, "migration-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	var version string
	if err := state.Reader().QueryRowContext(ctx, `SELECT value FROM state_meta WHERE key='schema_version'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != "2" {
		t.Fatalf("schema version = %q", version)
	}
	if _, err := state.Reader().ExecContext(ctx, `SELECT completed_mtime_ns,completed_size FROM directory_frontier LIMIT 1`); err != nil {
		t.Fatalf("directory revision columns were not migrated: %v", err)
	}
}

func TestStateCoordinatorLockAndFencedCommit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	state, err := OpenState(ctx, dir, "test", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if _, err := OpenState(ctx, dir, "competing", 1); err == nil {
		t.Fatal("second mutating coordinator acquired the same state")
	}
	root, err := state.EnsureRoot(ctx, filepath.Join(dir, "music"), "music")
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := state.BeginEpoch(ctx, []Root{root})
	if err != nil {
		t.Fatal(err)
	}
	file, err := state.ObserveFile(ctx, epoch, SourceFile{RootID: root.ID, RelativePath: "artist/song.flac", Device: 1, Inode: 2, Size: 123, MTimeNS: 456, Extension: ".flac"}, map[string]string{"metadata": "ffprobe/v1"})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := state.ClaimJobs(ctx, "metadata", 1, time.Minute)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim: %+v %v", jobs, err)
	}
	stale := jobs[0]
	stale.Fence = "stale"
	if err := state.CommitJob(ctx, JobResult{Job: stale, Contract: "ffprobe/v1", Metadata: []byte(`{}`)}); err == nil {
		t.Fatal("stale fence committed")
	}
	if err := state.CommitJob(ctx, JobResult{Job: jobs[0], Contract: "ffprobe/v1", Metadata: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	status, err := state.Status(ctx)
	if err != nil || status.Files != 1 || status.Metadata != 1 || status.JobsByState["completed"] != 1 || file.ID == "" {
		t.Fatalf("status=%+v file=%+v err=%v", status, file, err)
	}
	progress, err := state.Progress(ctx, map[string]string{"metadata": "ffprobe/v1"})
	if err != nil || progress.Files != 1 || progress.Total != 1 || progress.Finished != 1 || progress.Queued != 0 || progress.Leased != 0 || progress.Failed != 0 {
		t.Fatalf("progress=%+v err=%v", progress, err)
	}
}

func TestExpiredLeaseSupersedesOldAttempt(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, _ := state.EnsureRoot(ctx, t.TempDir(), "root")
	epoch, _ := state.BeginEpoch(ctx, []Root{root})
	_, err = state.ObserveFile(ctx, epoch, SourceFile{RootID: root.ID, RelativePath: "x.mp3", Device: 1, Inode: 7, Size: 10, MTimeNS: 2, Extension: ".mp3"}, map[string]string{"metadata": "v1"})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := state.ClaimJobs(ctx, "metadata", 1, time.Nanosecond)
	time.Sleep(time.Millisecond)
	second, _ := state.ClaimJobs(ctx, "metadata", 1, time.Minute)
	if len(first) != 1 || len(second) != 1 || first[0].Fence == second[0].Fence || second[0].Attempt != first[0].Attempt+1 {
		t.Fatalf("bad attempts first=%+v second=%+v", first, second)
	}
	if err := state.CommitJob(ctx, JobResult{Job: first[0], Contract: "v1", Metadata: []byte(`{}`)}); err == nil {
		t.Fatal("superseded lease committed")
	}
	if err := state.CommitJob(ctx, JobResult{Job: second[0], Contract: "v1", Metadata: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
}

func TestRenewedLeaseCannotBeReclaimed(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "renew-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, _ := state.EnsureRoot(ctx, t.TempDir(), "root")
	epoch, _ := state.BeginEpoch(ctx, []Root{root})
	_, err = state.ObserveFile(ctx, epoch, SourceFile{RootID: root.ID, RelativePath: "x.mp3", Device: 1, Inode: 8, Size: 10, MTimeNS: 2, Extension: ".mp3"}, map[string]string{"metadata": "v1"})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := state.ClaimJobs(ctx, "metadata", 1, 20*time.Millisecond)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim: %+v %v", jobs, err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := state.RenewJobs(ctx, jobs, time.Minute); err != nil {
		t.Fatal(err)
	}
	time.Sleep(15 * time.Millisecond)
	reclaimed, err := state.ClaimJobs(ctx, "metadata", 1, time.Minute)
	if err != nil || len(reclaimed) != 0 {
		t.Fatalf("renewed job reclaimed: %+v %v", reclaimed, err)
	}
}

func TestRefreshFileRevisionRequeuesEverySemanticJob(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "refresh-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, _ := state.EnsureRoot(ctx, t.TempDir(), "root")
	epoch, _ := state.BeginEpoch(ctx, []Root{root})
	file, err := state.ObserveFile(ctx, epoch, SourceFile{RootID: root.ID, RelativePath: "x.mp3", Device: 1, Inode: 8, Size: 10, MTimeNS: 2, Extension: ".mp3"}, map[string]string{"metadata": "m1", "audio": "a1"})
	if err != nil {
		t.Fatal(err)
	}
	jobs, _ := state.ClaimJobs(ctx, "metadata", 1, time.Minute)
	if len(jobs) != 1 {
		t.Fatal("metadata job was not claimed")
	}
	changed, err := state.RefreshFileRevision(ctx, file.ID, file.SourceRevision, 11, 3, 1, 8)
	if err != nil || !changed {
		t.Fatalf("refresh changed=%v err=%v", changed, err)
	}
	if err := state.CommitJob(ctx, JobResult{Job: jobs[0], Contract: "m1", Metadata: []byte(`{}`)}); err == nil {
		t.Fatal("old source revision committed after refresh")
	}
	pending, leased, err := state.JobCounts(ctx, "metadata")
	if err != nil || pending != 1 || leased != 0 {
		t.Fatalf("metadata was not requeued: pending=%d leased=%d err=%v", pending, leased, err)
	}
}

func TestMERTSignalGateRejectsSilenceAndDC(t *testing.T) {
	if hasMERTSignal(audio.DecodedPCM{Samples: make([]float32, 100), SampleRate: 24_000, Channels: 1}) {
		t.Fatal("silence passed signal gate")
	}
	dc := make([]float32, 100)
	for i := range dc {
		dc[i] = 0.25
	}
	if hasMERTSignal(audio.DecodedPCM{Samples: dc, SampleRate: 24_000, Channels: 1}) {
		t.Fatal("DC passed signal gate")
	}
	dc[50] = -0.25
	if !hasMERTSignal(audio.DecodedPCM{Samples: dc, SampleRate: 24_000, Channels: 1}) {
		t.Fatal("varying signal was rejected")
	}
}

func TestCorpusSnapshotIsConsistentAndReusable(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "snapshot-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, _ := state.EnsureRoot(ctx, t.TempDir(), "root")
	epoch, _ := state.BeginEpoch(ctx, []Root{root})
	_, err = state.ObserveFile(ctx, epoch, SourceFile{RootID: root.ID, RelativePath: "x.flac", Device: 1, Inode: 1, Size: 1, MTimeNS: 1, Extension: ".flac"}, map[string]string{"metadata": "v1"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := state.CreateSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.CreateSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.Generation != second.Generation || first.SHA256 != second.SHA256 || first.Path != second.Path || first.Bytes <= 0 {
		t.Fatalf("snapshot changed without input change: first=%+v second=%+v", first, second)
	}
}
