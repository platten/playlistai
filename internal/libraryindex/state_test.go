package libraryindex

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/localaudio"
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
	if version != "5" {
		t.Fatalf("schema version = %q", version)
	}
	if _, err := state.Reader().ExecContext(ctx, `SELECT completed_mtime_ns,completed_size FROM directory_frontier LIMIT 1`); err != nil {
		t.Fatalf("directory revision columns were not migrated: %v", err)
	}
	if _, err := state.Reader().ExecContext(ctx, `SELECT follow_directory_symlinks FROM scan_epochs LIMIT 1`); err != nil {
		t.Fatalf("scan policy column was not migrated: %v", err)
	}
}

func TestStateMigratesV4RecordingKeysInBoundedBatches(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "library-index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	record := MetadataRecord{Probe: localaudio.ProbeResult{Metadata: localaudio.Metadata{MusicBrainzIDs: map[string]string{"MUSICBRAINZ_TRACKID": "12345678-1234-1234-1234-123456789abc"}}}}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `
		CREATE TABLE state_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO state_meta(key,value) VALUES('schema_version','4');
		CREATE TABLE track_metadata (
			file_id TEXT NOT NULL, source_revision TEXT NOT NULL, contract TEXT NOT NULL,
			data BLOB NOT NULL, PRIMARY KEY(file_id,contract)
		);
		INSERT INTO track_metadata(file_id,source_revision,contract,data) VALUES('file','revision','metadata/v1',?);`, raw); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	state, err := OpenState(ctx, dir, "migration-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	var recordingKey string
	if err := state.Reader().QueryRowContext(ctx, `SELECT recording_key FROM track_metadata WHERE file_id='file'`).Scan(&recordingKey); err != nil {
		t.Fatal(err)
	}
	if recordingKey != "musicbrainz:12345678-1234-1234-1234-123456789abc" {
		t.Fatalf("recording key = %q", recordingKey)
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

func TestReusableMERTUsesStrongRecordingIdentityAndExactContract(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "reuse-test", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, err := state.EnsureRoot(ctx, t.TempDir(), "music")
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := state.BeginEpoch(ctx, []Root{root})
	if err != nil {
		t.Fatal(err)
	}
	jobs := map[string]string{"metadata": "metadata/v1", "audio": "audio/v1"}
	for i := range 2 {
		_, err = state.ObserveFile(ctx, epoch, SourceFile{RootID: root.ID, RelativePath: fmt.Sprintf("song-%d.flac", i), Device: 1, Inode: uint64(i + 1), Size: 100, MTimeNS: int64(i + 1), Extension: ".flac"}, jobs)
		if err != nil {
			t.Fatal(err)
		}
	}
	metadataJobs, err := state.ClaimJobs(ctx, "metadata", 2, time.Minute)
	if err != nil || len(metadataJobs) != 2 {
		t.Fatalf("claim metadata: %v jobs=%d", err, len(metadataJobs))
	}
	for _, job := range metadataJobs {
		record := MetadataRecord{Probe: localaudio.ProbeResult{Metadata: localaudio.Metadata{MusicBrainzIDs: map[string]string{"MUSICBRAINZ_TRACKID": "12345678-1234-1234-1234-123456789abc"}}}}
		raw, _ := json.Marshal(record)
		if err := state.CommitJob(ctx, JobResult{Job: job, Contract: job.SemanticKey, Metadata: raw}); err != nil {
			t.Fatal(err)
		}
	}
	audioJobs, err := state.ClaimJobs(ctx, "audio", 2, time.Minute)
	if err != nil || len(audioJobs) != 2 {
		t.Fatalf("claim audio: %v jobs=%d", err, len(audioJobs))
	}
	vector := make([]byte, audio.MERTDimension*4)
	binary.LittleEndian.PutUint32(vector, math.Float32bits(1))
	if err := state.CommitJob(ctx, JobResult{Job: audioJobs[0], Contract: "audio/v1", DSP: []byte(`{"version":"dsp"}`), DSPCacheContract: "dsp/v1", Vector: vector, MERTData: []byte(`{"model":{}}`), Dimension: audio.MERTDimension}); err != nil {
		t.Fatal(err)
	}
	if cached, found, err := state.CachedDSP(ctx, audioJobs[0].FileID, audioJobs[0].SourceRevision, "dsp/v1"); err != nil || !found || !strings.Contains(string(cached), "dsp") {
		t.Fatalf("cached DSP: found=%v data=%q err=%v", found, cached, err)
	}
	recordingKey := "musicbrainz:12345678-1234-1234-1234-123456789abc"
	cache, err := state.LoadReusableMERT(ctx, "audio/v1")
	got, found := cache[mertReuseKey(recordingKey, "audio/v1")]
	if err != nil || !found || got.Dimension != audio.MERTDimension || len(got.Vector) != len(vector) {
		t.Fatalf("reusable MERT: found=%v result=%+v err=%v", found, got, err)
	}
	incompatible, err := state.LoadReusableMERT(ctx, "audio/v2")
	if _, found := incompatible[mertReuseKey(recordingKey, "audio/v2")]; err != nil || found {
		t.Fatalf("incompatible contract reused: found=%v err=%v", found, err)
	}
}

func TestStableRootAliasSurvivesRemountAndRootOrderChanges(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "test", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	firstMount := filepath.Join(t.TempDir(), "music-a")
	secondMount := filepath.Join(t.TempDir(), "music-b")
	first, err := state.EnsureRoot(ctx, firstMount, "music-main")
	if err != nil {
		t.Fatal(err)
	}
	other, err := state.EnsureRoot(ctx, filepath.Join(t.TempDir(), "other"), "music-other")
	if err != nil {
		t.Fatal(err)
	}
	remounted, err := state.EnsureRoot(ctx, secondMount, "music-main")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != remounted.ID || remounted.Path != filepath.Clean(secondMount) || other.ID == remounted.ID {
		t.Fatalf("alias identity changed across remount: first=%+v remounted=%+v other=%+v", first, remounted, other)
	}
	var roots int
	if err := state.Reader().QueryRowContext(ctx, "SELECT COUNT(*) FROM roots").Scan(&roots); err != nil || roots != 2 {
		t.Fatalf("remount created a duplicate root: count=%d err=%v", roots, err)
	}
}

func TestAdditionalRootDoesNotSilentlyRemountExistingAlias(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "additional-root-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	firstPath := filepath.Join(t.TempDir(), "first")
	secondPath := filepath.Join(t.TempDir(), "second")
	first, err := state.EnsureAdditionalRoot(ctx, firstPath, "archive")
	if err != nil {
		t.Fatal(err)
	}
	again, err := state.EnsureAdditionalRoot(ctx, firstPath, "archive")
	if err != nil || again != first {
		t.Fatalf("same append root was not idempotent: first=%+v again=%+v err=%v", first, again, err)
	}
	if _, err := state.EnsureAdditionalRoot(ctx, secondPath, "archive"); err == nil || !strings.Contains(err.Error(), "--root-alias") {
		t.Fatalf("append root silently remounted alias: %v", err)
	}
	remounted, err := state.EnsureRoot(ctx, secondPath, "archive")
	if err != nil || remounted.Path != secondPath {
		t.Fatalf("explicit root remount failed: root=%+v err=%v", remounted, err)
	}
}

func TestBatchedWriterSaturationPreservesEveryFenceAndObservation(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "batch-saturation", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, err := state.EnsureRoot(ctx, t.TempDir(), "music")
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := state.BeginEpoch(ctx, []Root{root})
	if err != nil {
		t.Fatal(err)
	}
	const count = 128
	var wg sync.WaitGroup
	errorsOut := make(chan error, count)
	for index := 0; index < count; index++ {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := state.ObserveFile(ctx, epoch, SourceFile{RootID: root.ID, RelativePath: fmt.Sprintf("track-%03d.flac", index), Size: int64(index + 1), MTimeNS: 1, Extension: ".flac"}, map[string]string{"metadata": "probe/v1"})
			errorsOut <- err
		}()
	}
	wg.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := state.ClaimJobs(ctx, "metadata", count, time.Minute)
	if err != nil || len(jobs) != count {
		t.Fatalf("claimed=%d err=%v", len(jobs), err)
	}
	errorsOut = make(chan error, count)
	for _, job := range jobs {
		job := job
		wg.Add(1)
		go func() {
			defer wg.Done()
			errorsOut <- state.CommitJob(ctx, JobResult{Job: job, Contract: job.SemanticKey, Metadata: []byte(`{"ok":true}`)})
		}()
	}
	wg.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatal(err)
		}
	}
	var completed int
	if err := state.Reader().QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs WHERE state='completed'").Scan(&completed); err != nil || completed != count {
		t.Fatalf("completed=%d err=%v", completed, err)
	}
}

func TestObserveFilesCommitsDirectoryChunkAtomically(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "observe-files-batch", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, err := state.EnsureRoot(ctx, t.TempDir(), "music")
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := state.BeginEpoch(ctx, []Root{root})
	if err != nil {
		t.Fatal(err)
	}
	observed, err := state.ObserveFiles(ctx, epoch, []SourceFile{
		{RootID: root.ID, RelativePath: "a.flac", Size: 1, MTimeNS: 1, Extension: ".flac"},
		{RootID: root.ID, RelativePath: "b.mp3", Size: 2, MTimeNS: 2, Extension: ".mp3"},
		{RootID: root.ID, RelativePath: "c.m4a", Size: 3, MTimeNS: 3, Extension: ".m4a"},
	}, map[string]string{"metadata": "probe/v1", "audio": "audio/v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range observed {
		if file.ID == "" || file.SourceRevision == "" || !file.needsProcessing {
			t.Fatalf("incomplete observed file: %+v", file)
		}
	}
	status, err := state.Status(ctx)
	if err != nil || status.Files != 3 || status.JobsByState["pending"] != 6 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if _, err := state.ObserveFiles(ctx, epoch, []SourceFile{
		{RootID: root.ID, RelativePath: "valid.flac", Size: 4, MTimeNS: 4, Extension: ".flac"},
		{RootID: root.ID, RelativePath: "../escape.flac", Size: 5, MTimeNS: 5, Extension: ".flac"},
	}, map[string]string{"metadata": "probe/v1"}); err == nil {
		t.Fatal("invalid batch committed")
	}
	status, err = state.Status(ctx)
	if err != nil || status.Files != 3 || status.JobsByState["pending"] != 6 {
		t.Fatalf("invalid batch changed state: status=%+v err=%v", status, err)
	}
}

func TestUnavailableNativeFileIdentityDoesNotCollapsePaths(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "zero-file-identity-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, err := state.EnsureRoot(ctx, t.TempDir(), "root")
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := state.BeginEpoch(ctx, []Root{root})
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"first.mp3", "second.flac"} {
		if _, err := state.ObserveFile(ctx, epoch, SourceFile{RootID: root.ID, RelativePath: relative, Size: 7, MTimeNS: 11, Extension: filepath.Ext(relative)}, map[string]string{"metadata": "v1"}); err != nil {
			t.Fatal(err)
		}
	}
	status, err := state.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Files != 2 || status.JobsByState["pending"] != 2 {
		t.Fatalf("zero native identities collapsed distinct paths: %+v", status)
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

func TestPermanentCorruptionIsNotCountedOrRequeuedAsRetry(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "permanent-failure-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, _ := state.EnsureRoot(ctx, t.TempDir(), "root")
	epoch, _ := state.BeginEpoch(ctx, []Root{root})
	_, err = state.ObserveFile(ctx, epoch, SourceFile{RootID: root.ID, RelativePath: "damaged.flac", Size: 10, MTimeNS: 2, Extension: ".flac"}, map[string]string{"metadata": "v1"})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := state.ClaimJobs(ctx, "metadata", 1, time.Minute)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim=%+v err=%v", jobs, err)
	}
	if err := state.FailJob(ctx, jobs[0], "corrupt_media", "damaged stream", false); err != nil {
		t.Fatal(err)
	}
	var jobState string
	var retryCount int
	if err := state.Reader().QueryRowContext(ctx, `SELECT state,retry_count FROM jobs WHERE id=?`, jobs[0].ID).Scan(&jobState, &retryCount); err != nil {
		t.Fatal(err)
	}
	if jobState != "failed" || retryCount != 0 {
		t.Fatalf("state=%q retryCount=%d", jobState, retryCount)
	}
	if changed, err := state.RetryFailed(ctx); err != nil || changed != 0 {
		t.Fatalf("permanent corruption requeued: changed=%d err=%v", changed, err)
	}
	if _, err := state.ObserveFile(ctx, epoch, SourceFile{RootID: root.ID, RelativePath: "damaged.flac", Size: 10, MTimeNS: 2, Extension: ".flac"}, map[string]string{"metadata": "v1"}); err != nil {
		t.Fatal(err)
	}
	var errorCode string
	if err := state.Reader().QueryRowContext(ctx, `SELECT state,retry_count,error_code FROM jobs WHERE id=?`, jobs[0].ID).Scan(&jobState, &retryCount, &errorCode); err != nil {
		t.Fatal(err)
	}
	if jobState != "failed" || retryCount != 0 || errorCode != "corrupt_media" {
		t.Fatalf("unchanged rescan requeued permanent failure: state=%q retry=%d code=%q", jobState, retryCount, errorCode)
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
	hasSignal := func(samples []float32) bool {
		t.Helper()
		resampled, metrics, err := audio.MERTResampleLocalWithMetrics(context.Background(), audio.DecodedPCM{Samples: samples, SampleRate: 24_000, Channels: 1})
		if err != nil {
			t.Fatal(err)
		}
		clear(resampled)
		return metrics.HasSignal
	}
	if hasSignal(make([]float32, 100)) {
		t.Fatal("silence passed signal gate")
	}
	dc := make([]float32, 100)
	for i := range dc {
		dc[i] = 0.25
	}
	if hasSignal(dc) {
		t.Fatal("DC passed signal gate")
	}
	dc[50] = -0.25
	if !hasSignal(dc) {
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
