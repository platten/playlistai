package libraryindex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFailedParentDoesNotPublishOrWalkChildren(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "failed-parent", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	path := t.TempDir()
	for _, name := range []string{"a-child/track.flac", "b-trigger.flac", "z-disappears.flac"} {
		dest := filepath.Join(path, name)
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := state.EnsureRoot(ctx, path, "music")
	if err != nil {
		t.Fatal(err)
	}
	var walked []string
	report, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 1, QueueDepth: 1,
		OnDirectory: func(path string) { walked = append(walked, path) },
		OnFile: func(file FileActivity) {
			if file.RelativePath == "b-trigger.flac" {
				if err := os.Remove(filepath.Join(path, "z-disappears.flac")); err != nil {
					t.Error(err)
				}
			}
		},
	})
	if err != nil || report.Errors != 1 || report.Complete {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if len(walked) != 1 {
		t.Fatalf("unpublished child walked: %v", walked)
	}
	var frontier, files int
	if err := state.Reader().QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM directory_frontier),(SELECT COUNT(*) FROM files)`).Scan(&frontier, &files); err != nil {
		t.Fatal(err)
	}
	if frontier != 1 || files != 0 {
		t.Fatalf("failed enumeration published frontier=%d files=%d", frontier, files)
	}
}

func TestSpilledScanBuffersStayReservedUntilCommitAndResume(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "bounded-spill", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	path := t.TempDir()
	count := scanDirectoryEntries*3 + 13
	for i := 0; i < count; i++ {
		if err := os.WriteFile(filepath.Join(path, fmt.Sprintf("track-%04d.flac", i)), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := state.EnsureRoot(ctx, path, "music")
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := state.BeginEpoch(ctx, []Root{root})
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := state.ClaimDirectories(ctx, epoch, 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(state.dir, "scan-staging"), 0o700); err != nil {
		t.Fatal(err)
	}
	admission := NewAdmission(ResourcePlan{MaxRAM: scanBufferBytes, HeavyWorkers: 1, IOWorkers: 1, MaxOpenFiles: 3}, 0)
	result := state.walkScanDirectory(ctx, tasks[0], map[string]Root{root.ID: root}, ScanOptions{Admission: admission})
	defer result.cleanup()
	if result.err != nil || result.spill == "" || len(result.files) != 0 || len(result.children) != 0 {
		t.Fatalf("result err=%v spill=%q files=%d children=%d", result.err, result.spill, len(result.files), len(result.children))
	}
	used, _ := admission.Usage()
	if used.Memory != scanBufferBytes || used.SourceIO != 0 || used.Files != 1 {
		t.Fatalf("buffer ownership before commit: %+v", used)
	}

	total := 0
	var first scanDirectoryChunk
	if err := readScanSpill(ctx, result.spill, func(chunk scanDirectoryChunk) error {
		if len(chunk.Files)+len(chunk.Children) > scanDirectoryEntries {
			t.Fatalf("unbounded chunk: %d", len(chunk.Files)+len(chunk.Children))
		}
		if total == 0 {
			first = chunk
		}
		total += len(chunk.Files)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if total != count {
		t.Fatalf("spilled files=%d want=%d", total, count)
	}
	inventory, err := state.loadScanInventory(ctx, []Root{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate interruption after a durable chunk, before parent completion.
	if err := state.commitScanBatch(ctx, epoch, []scanDirectoryResult{{task: tasks[0], files: first.Files, continuation: true}}, inventory, nil, &ScanReport{}); err != nil {
		t.Fatal(err)
	}

	removed := first.Files[0].RelativePath
	if err := os.Remove(filepath.Join(path, removed)); err != nil {
		t.Fatal(err)
	}
	result.cleanup()
	used, _ = admission.Usage()
	if used.Memory != 0 {
		t.Fatalf("leaked buffer reservation: %+v", used)
	}
	report, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 1, QueueDepth: 1, SemanticJobs: map[string]string{"metadata": "v1"}})
	if err != nil || !report.Complete || !report.Resumed || report.Files != int64(count-1) {
		t.Fatalf("resume=%+v err=%v", report, err)
	}
	var status string
	if err := state.Reader().QueryRowContext(ctx, `SELECT status FROM files WHERE root_id=? AND relative_path=?`, root.ID, removed).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "missing" {
		t.Fatalf("deleted file from interrupted chunk retained status %q", status)
	}
	if _, err := os.Stat(filepath.Join(state.dir, "scan-staging")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging not removed: %v", err)
	}
}

func TestInventoryCapFallsBackWithoutGuessingIdentity(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "inventory-cap", 2)
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
	files := make([]SourceFile, scanInventoryLimit+1)
	for i := range files {
		files[i] = SourceFile{RootID: root.ID, RelativePath: fmt.Sprintf("%05d.flac", i), Size: 1, Extension: ".flac"}
	}
	for start := 0; start < len(files); start += 1024 {
		if _, err := state.ObserveFiles(ctx, epoch, files[start:min(start+1024, len(files))], nil); err != nil {
			t.Fatal(err)
		}
	}
	inventory, err := state.loadScanInventory(ctx, []Root{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.disabled || len(inventory.byPath) != 0 {
		t.Fatalf("oversized inventory retained %d paths", len(inventory.byPath))
	}
	file := files[len(files)-1]
	if err := normalizeSourceFile(&file); err != nil {
		t.Fatal(err)
	}
	if inventory.unseen(file) || inventory.unchanged(&file) {
		t.Fatal("partial cache guessed identity")
	}
}

func BenchmarkScanFlatDirectory(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			ctx := context.Background()
			path := b.TempDir()
			for i := 0; i < count; i++ {
				if err := os.WriteFile(filepath.Join(path, fmt.Sprintf("%05d.flac", i)), nil, 0o600); err != nil {
					b.Fatal(err)
				}
			}
			state, err := OpenState(ctx, b.TempDir(), "flat-benchmark", 2)
			if err != nil {
				b.Fatal(err)
			}
			defer state.Close()
			root, err := state.EnsureRoot(ctx, path, "music")
			if err != nil {
				b.Fatal(err)
			}
			options := ScanOptions{Roots: []Root{root}, Workers: 4, QueueDepth: 8, SemanticJobs: map[string]string{"metadata": "probe/v1"}}
			if _, err := state.Scan(ctx, options); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				report, err := state.Scan(ctx, options)
				if err != nil || report.Files != int64(count) {
					b.Fatalf("report=%+v err=%v", report, err)
				}
			}
		})
	}
}

func TestSpilledScanPreservesSerialHardlinkIdentity(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "hardlink-sort", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	path := t.TempDir()
	// Directory insertion order deliberately disagrees with lexical order.
	if err := os.WriteFile(filepath.Join(path, "z.flac"), []byte("same recording"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < scanDirectoryEntries+30; i++ {
		if err := os.WriteFile(filepath.Join(path, fmt.Sprintf("middle-%04d.flac", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(filepath.Join(path, "z.flac"), filepath.Join(path, "a.flac")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	root, err := state.EnsureRoot(ctx, path, "music")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 1, QueueDepth: 1, SemanticJobs: map[string]string{"metadata": "v1"}}); err != nil {
		t.Fatal(err)
	}
	var id, relative string
	if err := state.Reader().QueryRowContext(ctx, `SELECT id,relative_path FROM files WHERE relative_path IN ('a.flac','z.flac')`).Scan(&id, &relative); err != nil {
		t.Fatal(err)
	}
	if id != stableFileID(root.ID, "a.flac") || relative != "z.flac" {
		t.Fatalf("hardlink identity diverged from sorted serial scan: id=%q path=%q", id, relative)
	}
}

func TestCanceledSpilledScanReleasesBuffersAndResumes(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "cancel-spill", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	path := t.TempDir()
	for i := 0; i < 600; i++ {
		if err := os.WriteFile(filepath.Join(path, fmt.Sprintf("track-%04d.flac", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := state.EnsureRoot(ctx, path, "music")
	if err != nil {
		t.Fatal(err)
	}
	admission := NewAdmission(ResourcePlan{MaxRAM: 96 << 20, HeavyWorkers: 1, IOWorkers: 1, MaxOpenFiles: 3}, 0)
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	seen := 0
	_, err = state.Scan(canceled, ScanOptions{Roots: []Root{root}, Workers: 1, QueueDepth: 1, Admission: admission, OnFile: func(FileActivity) {
		seen++
		if seen == 300 {
			cancel()
		}
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err=%v", err)
	}
	used, _ := admission.Usage()
	if used.Memory != 0 || used.Files != 0 || used.SourceIO != 0 {
		t.Fatalf("canceled scan leaked admission: %+v", used)
	}
	if _, err := os.Stat(filepath.Join(state.dir, "scan-staging")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled scan retained staging: %v", err)
	}
	report, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 1, QueueDepth: 1, SemanticJobs: map[string]string{"metadata": "v1"}})
	if err != nil || !report.Complete || !report.Resumed || report.Files != 600 {
		t.Fatalf("resume=%+v err=%v", report, err)
	}
}

// A process can exit after a failed directory commit and before FinishEpoch.
// The next invocation must report that durable failure even with no new errors.
func TestResumedScanReportsDurableFailures(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "resume-failures", 2)
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
	tasks, err := state.ClaimDirectories(ctx, epoch, 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := state.loadScanInventory(ctx, []Root{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.commitScanBatch(ctx, epoch, []scanDirectoryResult{{task: tasks[0], err: os.ErrPermission}}, inventory, nil, &ScanReport{}); err != nil {
		t.Fatal(err)
	}
	report, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 1, QueueDepth: 1})
	if err != nil || !report.Resumed || report.Complete || report.Errors != 1 {
		t.Fatalf("durable failure lost on resume: report=%+v err=%v", report, err)
	}
}

func TestReplayMembershipResetPreservesPartialRootInventory(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "partial-replay", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	path := t.TempDir()
	if err := os.Mkdir(filepath.Join(path, "unreadable"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "removed.flac"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := state.EnsureRoot(ctx, path, "music")
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := state.BeginEpoch(ctx, []Root{root})
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := state.ClaimDirectories(ctx, epoch, 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	result := state.walkScanDirectory(ctx, tasks[0], map[string]Root{root.ID: root}, ScanOptions{})
	defer result.cleanup()
	if result.err != nil {
		t.Fatal(result.err)
	}
	result.continuation = true
	inventory, err := state.loadScanInventory(ctx, []Root{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.commitScanBatch(ctx, epoch, []scanDirectoryResult{result}, inventory, nil, &ScanReport{}); err != nil {
		t.Fatal(err)
	}
	children, err := state.ClaimDirectories(ctx, epoch, 1, time.Hour)
	if err != nil || len(children) != 1 {
		t.Fatalf("children=%v err=%v", children, err)
	}
	if err := state.commitScanBatch(ctx, epoch, []scanDirectoryResult{{task: children[0], err: os.ErrPermission}}, inventory, nil, &ScanReport{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(path, "removed.flac")); err != nil {
		t.Fatal(err)
	}
	report, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 1, QueueDepth: 1})
	if err != nil || report.Complete || report.Errors != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	var status string
	if err := state.Reader().QueryRowContext(ctx, `SELECT status FROM files WHERE root_id=? AND relative_path='removed.flac'`, root.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "present" {
		t.Fatalf("partial-root replay destroyed prior inventory: %q", status)
	}
}

func TestScanAccountsSQLiteCachesWithoutAdmissionDeadlock(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "scan-cache-budget", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	path := t.TempDir()
	if err := os.WriteFile(filepath.Join(path, "track.flac"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := state.EnsureRoot(ctx, path, "music")
	if err != nil {
		t.Fatal(err)
	}
	for _, enough := range []bool{false, true} {
		budget := state.pageCacheBytes + scanBufferBytes
		if !enough {
			budget--
		}
		admission := NewAdmission(ResourcePlan{MaxRAM: budget, HeavyWorkers: 1, IOWorkers: 1, MaxOpenFiles: 3}, 0)
		bounded, cancel := context.WithTimeout(ctx, time.Second)
		report, err := state.Scan(bounded, ScanOptions{Roots: []Root{root}, Workers: 1, QueueDepth: 1, Admission: admission, SemanticJobs: map[string]string{"metadata": "v1"}, OnFile: func(FileActivity) {
			used, _ := admission.Usage()
			if used.Memory != budget {
				t.Errorf("accounted memory=%d want=%d", used.Memory, budget)
			}
		}})
		cancel()
		if enough {
			if err != nil || !report.Complete {
				t.Fatalf("exact budget scan=%+v err=%v", report, err)
			}
		} else if err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("insufficient budget did not fail promptly: %v", err)
		}
		used, _ := admission.Usage()
		if used.Memory != 0 {
			t.Fatalf("cache reservation leaked: %+v", used)
		}
	}
}

func TestLongPathScanBuffersAndReplayStayWithinReservation(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "long-scan-buffers", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := os.MkdirAll(filepath.Join(state.dir, "scan-staging"), 0o700); err != nil {
		t.Fatal(err)
	}
	db, tx, path, err := newScanSpill(ctx, state.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	defer db.Close()
	defer func() { _ = tx.Rollback() }()
	prefix := strings.Repeat("segment/", 4096)
	const count = 300
	check := func(chunk scanDirectoryChunk) {
		t.Helper()
		bytes := int64(0)
		for _, file := range chunk.Files {
			bytes += scanFileBufferBytes(file)
		}
		for _, child := range chunk.Children {
			bytes += scanChildBufferBytes(child)
		}
		if bytes+scanDirectoryFixedBytes > scanBufferBytes {
			t.Fatalf("retained chunk exceeds reservation: records=%d bytes=%d", len(chunk.Files)+len(chunk.Children), bytes)
		}
		if len(chunk.Files)+len(chunk.Children) >= scanDirectoryEntries {
			t.Fatalf("long paths reached count cap before byte spilling")
		}
	}
	flushed := 0
	spill := func(chunk scanDirectoryChunk) error { check(chunk); flushed++; return writeScanSpill(ctx, tx, chunk) }
	var buffer scanChunkBuffer
	for i := count - 1; i >= 0; i-- {
		relative := fmt.Sprintf("%s%04d.flac", prefix, i)
		if i%5 == 0 {
			err = buffer.addChild(relative, spill)
		} else {
			file := SourceFile{RootID: "root", RelativePath: relative, Extension: ".flac"}
			if err := normalizeSourceFile(&file); err != nil {
				t.Fatal(err)
			}
			err = buffer.addFile(file, spill)
		}
		if err != nil {
			t.Fatal(err)
		}
		if buffer.bytes+scanDirectoryFixedBytes > scanBufferBytes {
			t.Fatalf("buffer exceeded reservation before flush: %d", buffer.bytes)
		}
	}
	if err := buffer.flush(spill); err != nil {
		t.Fatal(err)
	}
	if flushed < 2 || buffer.bytes != 0 {
		t.Fatalf("spill count=%d retained bytes=%d", flushed, buffer.bytes)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	seen := 0
	lastFile, lastChild := "", ""
	if err := readScanSpill(ctx, path, func(chunk scanDirectoryChunk) error {
		check(chunk)
		for _, file := range chunk.Files {
			if file.RelativePath <= lastFile {
				t.Error("replay lost lexical file order")
			}
			lastFile = file.RelativePath
			seen++
		}
		for _, child := range chunk.Children {
			if child <= lastChild {
				t.Error("replay lost lexical child order")
			}
			lastChild = child
			seen++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if seen != count {
		t.Fatalf("replayed=%d want=%d", seen, count)
	}
}

func TestScanBufferRejectsOversizedSingleRecord(t *testing.T) {
	for _, file := range []bool{false, true} {
		var buffer scanChunkBuffer
		flush := func(scanDirectoryChunk) error { t.Error("oversized record caused publication"); return nil }
		path := strings.Repeat("x", scanDirectoryRecordBytes)
		var err error
		if file {
			err = buffer.addFile(SourceFile{RelativePath: path}, flush)
		} else {
			err = buffer.addChild(path, flush)
		}
		if err == nil || buffer.bytes != 0 || len(buffer.chunk.Files)+len(buffer.chunk.Children) != 0 {
			t.Fatalf("oversized record retained: file=%v err=%v bytes=%d", file, err, buffer.bytes)
		}
	}
}
