package libraryindex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestGracefulScanStopDrainsClaimedDirectoryAndResumes(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootPath, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "nested", "song.flac"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	state, err := OpenState(ctx, stateDir, "graceful-scan-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	root, err := state.EnsureRoot(ctx, rootPath, "music")
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	entered := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, scanErr := state.Scan(ctx, ScanOptions{
			Roots: []Root{root}, Workers: 1, QueueDepth: 1,
			SemanticJobs: map[string]string{"metadata": "probe/v1"}, StopAdmission: stop,
			OnDirectory: func(path string) {
				if path == "music" {
					close(entered)
					<-release
				}
			},
		})
		result <- scanErr
	}()
	<-entered
	close(stop)
	close(release)
	if err := <-result; !errors.Is(err, ErrShutdownRequested) {
		t.Fatalf("scan shutdown error=%v", err)
	}
	var completed, pending, leased int
	if err := state.Reader().QueryRowContext(ctx, `SELECT
		COALESCE(SUM(state='completed'),0),COALESCE(SUM(state='pending'),0),COALESCE(SUM(state='leased'),0)
		FROM directory_frontier`).Scan(&completed, &pending, &leased); err != nil {
		t.Fatal(err)
	}
	if completed != 1 || pending != 1 || leased != 0 {
		t.Fatalf("frontier after drain: completed=%d pending=%d leased=%d", completed, pending, leased)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	resumed, err := OpenState(ctx, stateDir, "graceful-scan-resume-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	report, err := resumed.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 1, QueueDepth: 1, SemanticJobs: map[string]string{"metadata": "probe/v1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Complete || !report.Resumed || report.Files != 1 {
		t.Fatalf("resumed scan=%+v", report)
	}
}

func TestDurableScanBoundsFrontierAndIgnoresNonRegularFiles(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	for _, name := range []string{"a/one.FLAC", "a/two.mp3", "b/three.aac", "b/no.txt"} {
		path := filepath.Join(rootPath, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(rootPath, "a"), filepath.Join(rootPath, "link")); err != nil {
		t.Fatal(err)
	}
	state, err := OpenState(ctx, t.TempDir(), "scan-test", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, err := state.EnsureRoot(ctx, rootPath, "music")
	if err != nil {
		t.Fatal(err)
	}
	report, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 4, QueueDepth: 1, SemanticJobs: map[string]string{"metadata": "probe/v1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Complete || report.Files != 3 || report.AudioFiles != 3 || report.Directories != 3 {
		t.Fatalf("unexpected report: %+v", report)
	}
	status, err := state.Status(ctx)
	if err != nil || status.Files != 3 || status.JobsByState["pending"] != 3 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestScanBatchesLargeDirectoryAcrossReadChunks(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	const count = 300
	for index := 0; index < count; index++ {
		name := filepath.Join(rootPath, fmt.Sprintf("track-%03d.flac", index))
		if err := os.WriteFile(name, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	state, err := OpenState(ctx, t.TempDir(), "large-directory-batch-test", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, err := state.EnsureRoot(ctx, rootPath, "music")
	if err != nil {
		t.Fatal(err)
	}
	var activities int
	report, err := state.Scan(ctx, ScanOptions{
		Roots: []Root{root}, Workers: 1, QueueDepth: 1,
		SemanticJobs: map[string]string{"metadata": "probe/v1", "audio": "audio/v1"},
		OnFile:       func(FileActivity) { activities++ },
	})
	if err != nil || !report.Complete || report.AudioFiles != count || activities != count {
		t.Fatalf("report=%+v activities=%d err=%v", report, activities, err)
	}
	status, err := state.Status(ctx)
	if err != nil || status.Files != count || status.JobsByState["pending"] != 2*count {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestScanFollowsNestedDirectorySymlinksAndStopsAncestorCycles(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	externalPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootPath, "collection"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(externalPath, "song.flac"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rootPath, filepath.Join(externalPath, "back-to-root")); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	if err := os.Symlink(externalPath, filepath.Join(rootPath, "collection", "linked-album")); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(externalPath, "song.flac"), filepath.Join(rootPath, "collection", "linked-file.flac")); err != nil {
		t.Skipf("file symlinks unavailable: %v", err)
	}
	state, err := OpenState(ctx, t.TempDir(), "symlink-scan-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, err := state.EnsureRoot(ctx, rootPath, "music")
	if err != nil {
		t.Fatal(err)
	}
	var issues []ProcessingIssue
	report, err := state.Scan(ctx, ScanOptions{
		Roots:          []Root{root},
		Workers:        1,
		QueueDepth:     1,
		FollowSymlinks: true,
		SemanticJobs:   map[string]string{"metadata": "probe/v1"},
		OnIssue:        func(issue ProcessingIssue) { issues = append(issues, issue) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Complete || report.Files != 1 || report.AudioFiles != 1 || report.Directories != 3 || report.Errors != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	var relative string
	if err := state.Reader().QueryRowContext(ctx, `SELECT relative_path FROM files WHERE status='present'`).Scan(&relative); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("collection", "linked-album", "song.flac")
	if relative != want {
		t.Fatalf("relative path = %q, want %q", relative, want)
	}
	if len(issues) != 1 || issues[0].Code != "symlink_cycle" || issues[0].Path != filepath.Join("collection", "linked-album", "back-to-root") {
		t.Fatalf("cycle issues = %+v", issues)
	}
}

func TestScanReportsDirectoryActivityBeforeAudioDiscovery(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootPath, "empty", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "empty", "nested", "song.flac"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := OpenState(ctx, t.TempDir(), "scan-activity-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, err := state.EnsureRoot(ctx, rootPath, "archive")
	if err != nil {
		t.Fatal(err)
	}
	var directories []string
	var files []string
	report, err := state.Scan(ctx, ScanOptions{
		Roots:        []Root{root},
		Workers:      1,
		QueueDepth:   1,
		SemanticJobs: map[string]string{"metadata": "v1"},
		OnDirectory:  func(path string) { directories = append(directories, path) },
		OnFile:       func(file FileActivity) { files = append(files, file.RelativePath) },
	})
	if err != nil || !report.Complete {
		t.Fatalf("scan=%+v err=%v", report, err)
	}
	if len(directories) < 3 || directories[0] != "archive" || !slices.Contains(directories, filepath.Join("archive", "empty", "nested")) {
		t.Fatalf("directory activity = %v", directories)
	}
	if !slices.Equal(files, []string{filepath.Join("empty", "nested", "song.flac")}) {
		t.Fatalf("file activity = %v", files)
	}
}

func TestPartialScanNeverTombstonesPriorInventory(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "song.mp3"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, _ := OpenState(ctx, t.TempDir(), "scan-test", 1)
	defer state.Close()
	root, _ := state.EnsureRoot(ctx, rootPath, "music")
	if _, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 1, QueueDepth: 1, SemanticJobs: map[string]string{"metadata": "v1"}}); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(filepath.Dir(rootPath), "missing-root")
	if err := os.Rename(rootPath, missing); err != nil {
		t.Fatal(err)
	}
	report, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 1, QueueDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Complete || report.Errors != 1 {
		t.Fatalf("offline root incorrectly completed: %+v", report)
	}
	status, err := state.Status(ctx)
	if err != nil || status.Present != 1 || status.Tombstoned != 0 {
		t.Fatalf("offline root erased inventory: %+v err=%v", status, err)
	}
}

func TestLargeDirectoryIsPublishedInBoundedChunks(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	for i := 0; i < 600; i++ {
		dir := filepath.Join(rootPath, fmt.Sprintf("dir-%04d", i))
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	state, err := OpenState(ctx, t.TempDir(), "large-directory-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, _ := state.EnsureRoot(ctx, rootPath, "music")
	report, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 2, QueueDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Complete || report.Directories != 601 {
		t.Fatalf("unexpected bounded scan report: %+v", report)
	}
}

func TestRescanDiscoversAddedFilesWithoutReprocessingCompletedJobs(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	writeFixture := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(rootPath, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFixture("existing.mp3")
	state, err := OpenState(ctx, t.TempDir(), "rescan-test", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, err := state.EnsureRoot(ctx, rootPath, "music")
	if err != nil {
		t.Fatal(err)
	}
	jobs := map[string]string{"metadata": "probe/v1"}
	first, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 2, QueueDepth: 1, SemanticJobs: jobs})
	if err != nil || !first.Complete || first.AudioFiles != 1 {
		t.Fatalf("first scan=%+v err=%v", first, err)
	}
	claimed, err := state.ClaimJobs(ctx, "metadata", 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if err := state.CommitJob(ctx, JobResult{Job: claimed[0], Contract: "probe/v1", Metadata: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}

	writeFixture("added.flac")
	var observed []string
	var observedMu sync.Mutex
	second, err := state.Scan(ctx, ScanOptions{
		Roots:        []Root{root},
		Workers:      2,
		QueueDepth:   1,
		SemanticJobs: jobs,
		OnFile: func(file FileActivity) {
			observedMu.Lock()
			observed = append(observed, file.RelativePath)
			observedMu.Unlock()
		},
	})
	if err != nil || !second.Complete || second.Resumed || second.Files != 2 || second.AudioFiles != 2 {
		t.Fatalf("second scan=%+v err=%v", second, err)
	}
	sort.Strings(observed)
	if !slices.Equal(observed, []string{"added.flac", "existing.mp3"}) {
		t.Fatalf("file callback did not include every discovered audio file: %v", observed)
	}
	progress, err := state.ScanCandidateProgress(ctx, second.Epoch, jobs)
	if err != nil || progress.Files != 2 || progress.Total != 1 {
		t.Fatalf("rescan progress=%+v err=%v", progress, err)
	}
	rows, err := state.Reader().QueryContext(ctx, `SELECT f.relative_path,j.state,j.attempt
		FROM jobs j JOIN files f ON f.id=j.file_id ORDER BY f.relative_path`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type jobState struct {
		path    string
		state   string
		attempt int
	}
	var got []jobState
	for rows.Next() {
		var item jobState
		if err := rows.Scan(&item.path, &item.state, &item.attempt); err != nil {
			t.Fatal(err)
		}
		got = append(got, item)
	}
	want := []jobState{{path: "added.flac", state: "pending", attempt: 0}, {path: "existing.mp3", state: "completed", attempt: 1}}
	if !slices.Equal(got, want) {
		t.Fatalf("rescan reprocessed completed work or missed new work: got=%+v want=%+v", got, want)
	}
}

func TestScanRetriesDirectoryChangedDuringEnumerationWithoutDoubleCounting(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "first.flac"), []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := OpenState(ctx, t.TempDir(), "live-directory-change-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, err := state.EnsureRoot(ctx, rootPath, "live")
	if err != nil {
		t.Fatal(err)
	}
	var addOnce sync.Once
	report, err := state.Scan(ctx, ScanOptions{
		Roots:        []Root{root},
		Workers:      1,
		QueueDepth:   1,
		SemanticJobs: map[string]string{"metadata": "probe/v1"},
		OnFile: func(file FileActivity) {
			if file.RelativePath == "first.flac" {
				addOnce.Do(func() {
					if writeErr := os.WriteFile(filepath.Join(rootPath, "added-while-scanning.flac"), []byte("added"), 0o600); writeErr != nil {
						t.Errorf("add file during scan: %v", writeErr)
						return
					}
					future := time.Now().Add(2 * time.Second)
					if changeErr := os.Chtimes(rootPath, future, future); changeErr != nil {
						t.Errorf("advance directory revision: %v", changeErr)
					}
				})
			}
		},
	})
	if err != nil || !report.Complete {
		t.Fatalf("live-change scan=%+v err=%v", report, err)
	}
	if report.Directories != 1 || report.Files != 2 || report.AudioFiles != 2 {
		t.Fatalf("retry double-counted or missed live additions: %+v", report)
	}
	status, err := state.Status(ctx)
	if err != nil || status.Files != 2 || status.JobsByState["pending"] != 2 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestAdditionalRootAppendsInventoryWithoutReprocessingCompletedRoot(t *testing.T) {
	ctx := context.Background()
	firstPath := t.TempDir()
	secondPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(firstPath, "first.mp3"), []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondPath, "second.flac"), []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := OpenState(ctx, t.TempDir(), "append-root-test", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	firstRoot, err := state.EnsureRoot(ctx, firstPath, "primary")
	if err != nil {
		t.Fatal(err)
	}
	jobs := map[string]string{"metadata": "probe/v1"}
	if _, err := state.Scan(ctx, ScanOptions{Roots: []Root{firstRoot}, Workers: 1, QueueDepth: 1, SemanticJobs: jobs}); err != nil {
		t.Fatal(err)
	}
	claimed, err := state.ClaimJobs(ctx, "metadata", 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("initial claim=%+v err=%v", claimed, err)
	}
	if err := state.CommitJob(ctx, JobResult{Job: claimed[0], Contract: "probe/v1", Metadata: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}

	secondRoot, err := state.EnsureRoot(ctx, secondPath, "archive")
	if err != nil {
		t.Fatal(err)
	}
	report, err := state.Scan(ctx, ScanOptions{Roots: []Root{secondRoot}, Workers: 1, QueueDepth: 1, SemanticJobs: jobs})
	if err != nil || !report.Complete || report.AudioFiles != 1 {
		t.Fatalf("append scan=%+v err=%v", report, err)
	}
	rows, err := state.Reader().QueryContext(ctx, `SELECT r.alias,f.relative_path,j.state,j.attempt
		FROM jobs j JOIN files f ON f.id=j.file_id JOIN roots r ON r.id=f.root_id
		ORDER BY r.alias`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var alias, relative, jobState string
		var attempt int
		if err := rows.Scan(&alias, &relative, &jobState, &attempt); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%s:%s:%s:%d", alias, relative, jobState, attempt))
	}
	want := []string{"archive:second.flac:pending:0", "primary:first.mp3:completed:1"}
	if !slices.Equal(got, want) {
		t.Fatalf("append root changed completed work: got=%v want=%v", got, want)
	}
}

func TestMultipleUnrelatedAppendRootsShareOneInventory(t *testing.T) {
	ctx := context.Background()
	firstPath, secondPath := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(firstPath, "first.mp3"), []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondPath, "second.flac"), []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := OpenState(ctx, t.TempDir(), "two-append-roots-test", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	first, err := state.EnsureAdditionalRoot(ctx, firstPath, "primary")
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.EnsureAdditionalRoot(ctx, secondPath, "archive")
	if err != nil {
		t.Fatal(err)
	}
	report, err := state.Scan(ctx, ScanOptions{
		Roots: []Root{first, second}, Workers: 2, QueueDepth: 1,
		SemanticJobs: map[string]string{"metadata": "probe/v1"},
	})
	if err != nil || !report.Complete || report.AudioFiles != 2 || report.Directories != 2 {
		t.Fatalf("two-root scan=%+v err=%v", report, err)
	}
	rows, err := state.Reader().QueryContext(ctx, `SELECT r.alias,f.relative_path
		FROM files f JOIN roots r ON r.id=f.root_id ORDER BY r.alias,f.relative_path`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var alias, relative string
		if err := rows.Scan(&alias, &relative); err != nil {
			t.Fatal(err)
		}
		got = append(got, alias+":"+relative)
	}
	if want := []string{"archive:second.flac", "primary:first.mp3"}; !slices.Equal(got, want) {
		t.Fatalf("inventory=%v want=%v", got, want)
	}
}

func TestChangedScanScopeDoesNotClaimStaleDirectoryEpoch(t *testing.T) {
	ctx := context.Background()
	firstPath, secondPath := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(firstPath, "first.mp3"), []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondPath, "second.flac"), []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := OpenState(ctx, t.TempDir(), "scope-change-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	first, err := state.EnsureRoot(ctx, firstPath, "first")
	if err != nil {
		t.Fatal(err)
	}
	staleEpoch, err := state.BeginEpoch(ctx, []Root{first})
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.EnsureAdditionalRoot(ctx, secondPath, "second")
	if err != nil {
		t.Fatal(err)
	}
	report, err := state.Scan(ctx, ScanOptions{
		Roots: []Root{second}, Workers: 1, QueueDepth: 1,
		SemanticJobs: map[string]string{"metadata": "probe/v1"},
	})
	if err != nil || !report.Complete || report.AudioFiles != 1 {
		t.Fatalf("replacement-scope scan=%+v err=%v", report, err)
	}
	var epochStatus, frontierState string
	if err := state.Reader().QueryRowContext(ctx, `SELECT status FROM scan_epochs WHERE id=?`, staleEpoch).Scan(&epochStatus); err != nil {
		t.Fatal(err)
	}
	if err := state.Reader().QueryRowContext(ctx, `SELECT state FROM directory_frontier WHERE epoch_id=? AND relative_path='.'`, staleEpoch).Scan(&frontierState); err != nil {
		t.Fatal(err)
	}
	if epochStatus != "interrupted" || frontierState != "abandoned" {
		t.Fatalf("stale epoch status=%q frontier=%q", epochStatus, frontierState)
	}
}

func TestChangedSymlinkPolicyDoesNotResumeStaleDirectoryEpoch(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "song.flac"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := OpenState(ctx, t.TempDir(), "symlink-policy-change-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, err := state.EnsureRoot(ctx, rootPath, "music")
	if err != nil {
		t.Fatal(err)
	}
	staleEpoch, err := state.BeginEpoch(ctx, []Root{root})
	if err != nil {
		t.Fatal(err)
	}
	report, err := state.Scan(ctx, ScanOptions{
		Roots:          []Root{root},
		Workers:        1,
		QueueDepth:     1,
		FollowSymlinks: true,
		SemanticJobs:   map[string]string{"metadata": "probe/v1"},
	})
	if err != nil || !report.Complete || report.Resumed || report.Epoch == staleEpoch || report.AudioFiles != 1 {
		t.Fatalf("replacement scan=%+v err=%v", report, err)
	}
	var status, detail, frontier string
	if err := state.Reader().QueryRowContext(ctx, `SELECT status,error FROM scan_epochs WHERE id=?`, staleEpoch).Scan(&status, &detail); err != nil {
		t.Fatal(err)
	}
	if err := state.Reader().QueryRowContext(ctx, `SELECT state FROM directory_frontier WHERE epoch_id=? AND relative_path='.'`, staleEpoch).Scan(&frontier); err != nil {
		t.Fatal(err)
	}
	if status != "interrupted" || detail != "directory symlink policy changed before resume" || frontier != "abandoned" {
		t.Fatalf("stale epoch status=%q detail=%q frontier=%q", status, detail, frontier)
	}
}

func TestInterruptedResumeRequeuesCompletedDirectoryWhenFilesWereAdded(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	completedPath := filepath.Join(rootPath, "completed")
	pendingPath := filepath.Join(rootPath, "pending")
	for _, path := range []string{completedPath, pendingPath} {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oldPath := filepath.Join(completedPath, "old.mp3")
	if err := os.WriteFile(oldPath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pendingPath, "pending.mp3"), []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := OpenState(ctx, t.TempDir(), "resume-rescan-test", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, _ := state.EnsureRoot(ctx, rootPath, "music")
	epoch, err := state.BeginEpoch(ctx, []Root{root})
	if err != nil {
		t.Fatal(err)
	}
	rootTasks, err := state.ClaimDirectories(ctx, epoch, 1, time.Minute)
	if err != nil || len(rootTasks) != 1 {
		t.Fatalf("root claim=%+v err=%v", rootTasks, err)
	}
	if err := state.CompleteDirectory(ctx, rootTasks[0], []string{"completed", "pending"}, testDirectoryRevision(t, rootPath)); err != nil {
		t.Fatal(err)
	}
	completedTasks, err := state.ClaimDirectories(ctx, epoch, 1, time.Minute)
	if err != nil || len(completedTasks) != 1 || completedTasks[0].RelativePath != "completed" {
		t.Fatalf("completed claim=%+v err=%v", completedTasks, err)
	}
	oldInfo, err := os.Stat(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	device, inode := fileIdentity(oldInfo)
	if _, err := state.ObserveFile(ctx, epoch, SourceFile{RootID: root.ID, RelativePath: filepath.Join("completed", "old.mp3"), Device: device, Inode: inode, Size: oldInfo.Size(), MTimeNS: oldInfo.ModTime().UnixNano(), Extension: ".mp3"}, map[string]string{"metadata": "probe/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := state.CompleteDirectory(ctx, completedTasks[0], nil, testDirectoryRevision(t, completedPath)); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(completedPath, "new.flac"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Filesystems with coarse timestamp resolution still need a deterministic
	// revision change in this regression.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(completedPath, future, future); err != nil {
		t.Fatal(err)
	}
	report, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 2, QueueDepth: 1, SemanticJobs: map[string]string{"metadata": "probe/v1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Complete || !report.Resumed || report.RescannedDirectories != 1 || report.AudioFiles != 3 {
		t.Fatalf("resume did not rescan the changed completed directory: %+v", report)
	}
	status, err := state.Status(ctx)
	if err != nil || status.Files != 3 || status.JobsByState["pending"] != 3 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func testDirectoryRevision(t *testing.T, path string) DirectoryRevision {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return DirectoryRevision{MTimeNS: info.ModTime().UnixNano(), Size: info.Size()}
}
