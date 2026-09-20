package libraryindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScanManifestFreezesInventoryAndPendingDiff(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	for _, name := range []string{"already.mp3", "pending.flac"} {
		if err := os.WriteFile(filepath.Join(rootPath, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stateDir := t.TempDir()
	state, err := OpenState(ctx, stateDir, "manifest-test", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, _ := state.EnsureAdditionalRoot(ctx, rootPath, "archive")
	jobs := map[string]string{"metadata": "probe/v1"}
	scan, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 1, QueueDepth: 1, SemanticJobs: jobs})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := state.ClaimJobs(ctx, "metadata", 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if err := state.CommitJob(ctx, JobResult{Job: claimed[0], Contract: "probe/v1", Metadata: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	report, err := state.WriteScanManifest(ctx, scan.Epoch, jobs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Version != ScanManifestVersion || report.InventoryCount != 1 || report.DiffCount != 1 || report.JobCount != 1 {
		t.Fatalf("manifest report=%+v", report)
	}
	for relative, wantHash := range map[string]string{report.InventoryPath: report.InventoryHash, report.DiffPath: report.DiffHash} {
		raw, err := os.ReadFile(filepath.Join(stateDir, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		got := sha256.Sum256(raw)
		if hex.EncodeToString(got[:]) != wantHash {
			t.Fatalf("hash mismatch for %s", relative)
		}
		if strings.Contains(string(raw), rootPath) {
			t.Fatalf("manifest leaked absolute root: %s", raw)
		}
	}
	var diff scanDiffFile
	raw, _ := os.ReadFile(filepath.Join(stateDir, filepath.FromSlash(report.DiffPath)))
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &diff); err != nil {
		t.Fatal(err)
	}
	if diff.RootAlias != "archive" || diff.Size <= 0 || len(diff.Jobs) != 1 || diff.Jobs[0].Kind != "metadata" {
		t.Fatalf("diff=%+v", diff)
	}
}

func TestScanManifestAndProgressCountUniquePendingAudioFiles(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "directory-only"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "song.flac"), []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "notes.txt"), []byte("not audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	state, err := OpenState(ctx, stateDir, "unique-audio-manifest-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, err := state.EnsureRoot(ctx, rootPath, "music")
	if err != nil {
		t.Fatal(err)
	}
	semantic := map[string]string{"metadata": "probe/v1", "audio": "mert/v1"}
	scan, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 1, QueueDepth: 1, SemanticJobs: semantic})
	if err != nil {
		t.Fatal(err)
	}
	if scan.Files != 1 || scan.AudioFiles != 1 {
		t.Fatalf("scan counted non-processing entries: %+v", scan)
	}
	manifest, err := state.WriteScanManifest(ctx, scan.Epoch, semantic)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.InventoryCount != 1 || manifest.DiffCount != 1 || manifest.JobCount != 2 {
		t.Fatalf("manifest counts=%+v", manifest)
	}
	inventoryRaw, err := os.ReadFile(filepath.Join(stateDir, filepath.FromSlash(manifest.InventoryPath)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(inventoryRaw), "notes.txt") || strings.Contains(string(inventoryRaw), "directory-only") || !strings.Contains(string(inventoryRaw), "song.flac") {
		t.Fatalf("inventory contains a non-processing entry: %s", inventoryRaw)
	}
	diffRaw, err := os.ReadFile(filepath.Join(stateDir, filepath.FromSlash(manifest.DiffPath)))
	if err != nil {
		t.Fatal(err)
	}
	var diff scanDiffFile
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(diffRaw))), &diff); err != nil {
		t.Fatal(err)
	}
	if diff.Path != "song.flac" || len(diff.Jobs) != 2 || diff.Jobs[0].Kind != "audio" || diff.Jobs[1].Kind != "metadata" {
		t.Fatalf("diff=%+v", diff)
	}
	progress, err := state.ScanDiffProgress(ctx, scan.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Files != 1 || progress.Total != 1 || progress.Queued != 1 {
		t.Fatalf("progress counted stage jobs instead of the audio file: %+v", progress)
	}
	metadataJobs, err := state.ClaimScanDiffJobs(ctx, scan.Epoch, "metadata", 1, time.Minute)
	if err != nil || len(metadataJobs) != 1 {
		t.Fatalf("claim metadata=%+v err=%v", metadataJobs, err)
	}
	if err := state.CommitJob(ctx, JobResult{Job: metadataJobs[0], Contract: "probe/v1", Metadata: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	metadataProgress, err := state.ScanDiffProgressForJobs(ctx, scan.Epoch, map[string]string{"metadata": "probe/v1"})
	if err != nil || metadataProgress.Total != 1 || metadataProgress.Finished != 1 || metadataProgress.Queued != 0 {
		t.Fatalf("metadata phase progress=%+v err=%v", metadataProgress, err)
	}
	audioProgress, err := state.ScanDiffProgressForJobs(ctx, scan.Epoch, map[string]string{"audio": "mert/v1"})
	if err != nil || audioProgress.Total != 1 || audioProgress.Finished != 0 || audioProgress.Queued != 1 {
		t.Fatalf("audio phase progress=%+v err=%v", audioProgress, err)
	}
	status, err := state.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Files != 1 || status.QueuedFiles != 1 || status.JobsByState["pending"] != 1 || status.JobsByState["completed"] != 1 {
		t.Fatalf("status did not separate file and stage-job counts: %+v", status)
	}
}

func TestChangedFileAfterManifestIsSkippedUntilNextScan(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	path := filepath.Join(rootPath, "growing.flac")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := OpenState(ctx, t.TempDir(), "manifest-change-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, _ := state.EnsureAdditionalRoot(ctx, rootPath, "incoming")
	semantic := map[string]string{"metadata": "probe/v1"}
	first, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 1, QueueDepth: 1, SemanticJobs: semantic})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.WriteScanManifest(ctx, first.Epoch, semantic); err != nil {
		t.Fatal(err)
	}
	jobs, err := state.ClaimScanDiffJobs(ctx, first.Epoch, "metadata", 1, time.Minute)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim=%+v err=%v", jobs, err)
	}
	file, err := state.File(ctx, jobs[0].FileID)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := handle.Write([]byte("-larger"))
	if err := errors.Join(writeErr, handle.Close()); err != nil {
		t.Fatal(err)
	}
	analyzer := &Analyzer{State: state, FreezeManifest: true}
	err = analyzer.verifySourceRevision(ctx, jobs[0], file, path)
	if !errors.Is(err, errSourceChangedAfterManifest) {
		t.Fatalf("revision error=%v", err)
	}
	if err := state.FailJob(ctx, jobs[0], "source_changed_after_manifest", err.Error(), false); err != nil {
		t.Fatal(err)
	}
	if more, err := state.ClaimScanDiffJobs(ctx, first.Epoch, "metadata", 1, time.Minute); err != nil || len(more) != 0 {
		t.Fatalf("changed file re-entered frozen diff: jobs=%+v err=%v", more, err)
	}

	second, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 1, QueueDepth: 1, SemanticJobs: semantic})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := state.WriteScanManifest(ctx, second.Epoch, semantic)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.DiffCount != 1 {
		t.Fatalf("changed file was not queued on rerun: %+v", manifest)
	}
	retry, err := state.ClaimScanDiffJobs(ctx, second.Epoch, "metadata", 1, time.Minute)
	if err != nil || len(retry) != 1 || retry[0].SourceRevision == jobs[0].SourceRevision {
		t.Fatalf("rerun claim=%+v err=%v", retry, err)
	}
}

func TestAppendManifestDoesNotClaimPendingJobFromOtherRoot(t *testing.T) {
	ctx := context.Background()
	firstPath, secondPath := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(firstPath, "first.mp3"), []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondPath, "second.flac"), []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := OpenState(ctx, t.TempDir(), "append-diff-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	semantic := map[string]string{"metadata": "probe/v1"}
	first, _ := state.EnsureAdditionalRoot(ctx, firstPath, "first")
	if _, err := state.Scan(ctx, ScanOptions{Roots: []Root{first}, Workers: 1, QueueDepth: 1, SemanticJobs: semantic}); err != nil {
		t.Fatal(err)
	}
	second, _ := state.EnsureAdditionalRoot(ctx, secondPath, "second")
	scan, err := state.Scan(ctx, ScanOptions{Roots: []Root{second}, Workers: 1, QueueDepth: 1, SemanticJobs: semantic})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := state.WriteScanManifest(ctx, scan.Epoch, semantic)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.InventoryCount != 1 || manifest.DiffCount != 1 {
		t.Fatalf("append manifest=%+v", manifest)
	}
	jobs, err := state.ClaimScanDiffJobs(ctx, scan.Epoch, "metadata", 4, time.Minute)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim=%+v err=%v", jobs, err)
	}
	file, err := state.File(ctx, jobs[0].FileID)
	if err != nil {
		t.Fatal(err)
	}
	if file.RootAlias != "second" || file.RelativePath != "second.flac" {
		t.Fatalf("other root leaked into append diff: %+v", file)
	}
}
