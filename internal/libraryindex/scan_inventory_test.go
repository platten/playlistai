package libraryindex

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// observeEpochDirectly applies the per-file ObserveFiles path that the scan
// fast path must be equivalent to.
func observeEpochDirectly(t *testing.T, state *State, root Root, semantic map[string]string) {
	t.Helper()
	ctx := context.Background()
	epoch, err := state.BeginEpoch(ctx, []Root{root})
	if err != nil {
		t.Fatal(err)
	}
	var files []SourceFile
	if err := filepath.WalkDir(root.Path, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		ext := strings.ToLower(filepath.Ext(path))
		if _, ok := audioExtensions[ext]; !ok {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root.Path, path)
		if err != nil {
			return err
		}
		device, inode := fileIdentity(info)
		files = append(files, SourceFile{RootID: root.ID, RelativePath: relative, Device: device, Inode: inode, Size: info.Size(), MTimeNS: info.ModTime().UnixNano(), Extension: ext})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ObserveFiles(ctx, epoch, files, semantic); err != nil {
		t.Fatal(err)
	}
	if err := state.write(ctx, true, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `UPDATE scan_scopes SET outstanding_dirs=0 WHERE epoch_id=?`, epoch)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if finished, err := state.FinishEpoch(ctx, epoch); err != nil || !finished {
		t.Fatalf("finish=%v err=%v", finished, err)
	}
}

// durableInventory renders files and jobs keyed by path. IDs are rendered as
// the path whose stable ID they carry, so a preserved rename is visible.
func durableInventory(t *testing.T, state *State, root Root) []string {
	t.Helper()
	rows, err := state.Reader().QueryContext(context.Background(), `SELECT f.id,f.relative_path,f.status,f.tombstoned_at IS NULL,f.last_seen_epoch,f.source_revision,
			COALESCE(j.kind,''),COALESCE(j.semantic_key,''),COALESCE(j.state,''),COALESCE(j.source_revision,''),COALESCE(j.attempt,0),COALESCE(j.retry_count,0),COALESCE(j.error_code,'')
		FROM files f LEFT JOIN jobs j ON j.file_id=f.id ORDER BY f.relative_path,j.kind,j.semantic_key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, path, status, revision, kind, key, jobState, jobRevision, code string
		var live bool
		var seen int64
		var attempt, retries int
		if err := rows.Scan(&id, &path, &status, &live, &seen, &revision, &kind, &key, &jobState, &jobRevision, &attempt, &retries, &code); err != nil {
			t.Fatal(err)
		}
		origin := "other"
		for _, candidate := range []string{"keep.flac", "retry.flac", "old-name.flac", "modified.flac", "deleted.flac", "added.flac"} {
			if id == stableFileID(root.ID, candidate) {
				origin = candidate
			}
		}
		out = append(out, fmt.Sprintf("%s id=%s status=%s live=%v seen=%d rev=%s job=%s/%s/%s rev=%v attempt=%d retries=%d code=%s",
			path, origin, status, live, seen, revision[:8], kind, key, jobState, jobRevision == revision, attempt, retries, code))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestInMemoryScanMatchesPerFileObservation(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(rootPath, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"keep.flac", "retry.flac", "old-name.flac", "modified.flac", "deleted.flac"} {
		write(name, name)
	}
	firstKeys := map[string]string{"metadata": "probe/v1", "audio": "audio/v1"}
	open := func(name string) (*State, Root) {
		state, err := OpenState(ctx, t.TempDir(), name, 2)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = state.Close() })
		root, err := state.EnsureRoot(ctx, rootPath, "music")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 2, QueueDepth: 2, SemanticJobs: firstKeys}); err != nil {
			t.Fatal(err)
		}
		// Settle keep.flac, and leave retry.flac pending with a retry error.
		for _, path := range []string{"keep.flac", "retry.flac"} {
			setClaimTestStates(t, state, fmt.Sprintf(`UPDATE jobs SET state='leased',fence='f',attempt=1 WHERE kind='metadata' AND file_id=(SELECT id FROM files WHERE relative_path='%s')`, path))
		}
		var keep, retry Job
		for _, target := range []struct {
			path string
			job  *Job
		}{{"keep.flac", &keep}, {"retry.flac", &retry}} {
			if err := state.Reader().QueryRowContext(ctx, `SELECT j.id,j.kind,j.file_id,j.source_revision,j.semantic_key,j.attempt,j.fence
				FROM jobs j JOIN files f ON f.id=j.file_id WHERE j.kind='metadata' AND f.relative_path=?`, target.path).Scan(
				&target.job.ID, &target.job.Kind, &target.job.FileID, &target.job.SourceRevision, &target.job.SemanticKey, &target.job.Attempt, &target.job.Fence); err != nil {
				t.Fatal(err)
			}
		}
		if err := state.CommitJob(ctx, JobResult{Job: keep, Contract: "probe/v1", Metadata: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
		if err := state.FailJob(ctx, retry, "native_worker_transient", "retry me", true); err != nil {
			t.Fatal(err)
		}
		return state, root
	}
	scanned, scannedRoot := open("in-memory")
	direct, directRoot := open("direct")

	if err := os.Rename(filepath.Join(rootPath, "old-name.flac"), filepath.Join(rootPath, "new-name.flac")); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	write("modified.flac", "modified with different size")
	if err := os.Chtimes(filepath.Join(rootPath, "modified.flac"), future, future); err != nil {
		t.Fatal(err)
	}
	// Create before deleting so the filesystem cannot hand the deleted inode to
	// the new file, which identity tracking would treat as a move.
	write("added.flac", "added")
	if err := os.Remove(filepath.Join(rootPath, "deleted.flac")); err != nil {
		t.Fatal(err)
	}
	rescan := func(keys map[string]string) []string {
		t.Helper()
		report, err := scanned.Scan(ctx, ScanOptions{Roots: []Root{scannedRoot}, Workers: 2, QueueDepth: 2, SemanticJobs: keys})
		if err != nil || !report.Complete {
			t.Fatalf("rescan=%+v err=%v", report, err)
		}
		observeEpochDirectly(t, direct, directRoot, keys)
		got, want := durableInventory(t, scanned, scannedRoot), durableInventory(t, direct, directRoot)
		if !slices.Equal(got, want) {
			t.Fatalf("in-memory scan diverged from per-file observation:\n got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
		}
		return got
	}
	// Same semantic keys: every unchanged file is eligible for the fast path,
	// so this pass exercises its guards.
	got := rescan(firstKeys)
	joined := strings.Join(got, "\n")
	for _, expected := range []string{
		"keep.flac id=keep.flac status=present live=true seen=2", "job=metadata/probe/v1/completed",
		"new-name.flac id=old-name.flac", "deleted.flac id=deleted.flac status=missing live=false seen=1",
		"added.flac id=added.flac status=present live=true seen=2",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("scenario missing %q:\n%s", expected, joined)
		}
	}
	// The pending retry's error is cleared on rescan, as ObserveFiles does.
	if !slices.ContainsFunc(got, func(line string) bool {
		return strings.HasPrefix(line, "retry.flac ") && strings.HasSuffix(line, "job=metadata/probe/v1/pending rev=true attempt=1 retries=1 code=")
	}) {
		t.Fatalf("retry job was not reset:\n%s", joined)
	}
	// A semantic key change supersedes old jobs for every file.
	joined = strings.Join(rescan(map[string]string{"metadata": "probe/v1", "audio": "audio/v2"}), "\n")
	for _, expected := range []string{"job=audio/audio/v1/superseded", "job=audio/audio/v2/pending"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("scenario missing %q:\n%s", expected, joined)
		}
	}
}

// A second key-stable rescan exercises the fast path for every file.
func TestUnchangedRescanTakesFastPathAndKeepsSettledJobs(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	for index := 0; index < 20; index++ {
		if err := os.WriteFile(filepath.Join(rootPath, fmt.Sprintf("track-%02d.flac", index)), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	state, err := OpenState(ctx, t.TempDir(), "fast-rescan", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, err := state.EnsureRoot(ctx, rootPath, "music")
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{"metadata": "probe/v1"}
	if _, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 2, QueueDepth: 2, SemanticJobs: keys}); err != nil {
		t.Fatal(err)
	}
	setClaimTestStates(t, state, `UPDATE jobs SET state='completed'`)
	inventory, err := state.loadScanInventory(ctx, []Root{root}, keys)
	if err != nil {
		t.Fatal(err)
	}
	fast := 0
	for path := range inventory.byPath {
		_, relative, _ := strings.Cut(path, "\x00")
		info, err := os.Stat(filepath.Join(rootPath, relative))
		if err != nil {
			t.Fatal(err)
		}
		device, inode := fileIdentity(info)
		file := SourceFile{RootID: root.ID, RelativePath: relative, Device: device, Inode: inode, Size: info.Size(), MTimeNS: info.ModTime().UnixNano(), Extension: ".flac"}
		if err := normalizeSourceFile(&file); err != nil {
			t.Fatal(err)
		}
		if inventory.unchanged(&file) {
			fast++
		}
	}
	if fast != 20 {
		t.Fatalf("fast-path files=%d, want all 20 unchanged files", fast)
	}
	report, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 2, QueueDepth: 2, SemanticJobs: keys})
	if err != nil || !report.Complete || report.Files != 20 {
		t.Fatalf("rescan=%+v err=%v", report, err)
	}
	var stale, notCompleted int
	if err := state.Reader().QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM files WHERE last_seen_epoch<>?),(SELECT COUNT(*) FROM jobs WHERE state<>'completed')`, report.Epoch).Scan(&stale, &notCompleted); err != nil {
		t.Fatal(err)
	}
	if stale != 0 || notCompleted != 0 {
		t.Fatalf("fast rescan left %d files unseen and %d jobs reset", stale, notCompleted)
	}
}

func TestScanManifestKeepsOnlyNewestDiff(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "song.flac"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := OpenState(ctx, t.TempDir(), "diff-prune", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	root, err := state.EnsureRoot(ctx, rootPath, "music")
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{"metadata": "probe/v1"}
	var latest int64
	for range 2 {
		report, err := state.Scan(ctx, ScanOptions{Roots: []Root{root}, Workers: 1, QueueDepth: 1, SemanticJobs: keys})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := state.WriteScanManifest(ctx, report.Epoch, keys); err != nil {
			t.Fatal(err)
		}
		latest = report.Epoch
	}
	var epochs, rows int
	if err := state.Reader().QueryRowContext(ctx, `SELECT COUNT(DISTINCT epoch_id),COUNT(*) FROM scan_diff_jobs`).Scan(&epochs, &rows); err != nil {
		t.Fatal(err)
	}
	if epochs != 1 || rows != 1 {
		t.Fatalf("diff rows epochs=%d rows=%d, want only epoch %d", epochs, rows, latest)
	}
}

func TestIndexedUnchangedScanGuards(t *testing.T) {
	for _, test := range []struct {
		name, mutation           string
		want                     bool
		zeroIdentity, retainedID bool
	}{
		{name: "pending", want: true},
		{name: "completed", mutation: `UPDATE jobs SET state='completed'`, want: true},
		{name: "settled-failure", mutation: `UPDATE jobs SET state='failed',error_code='invalid'`, want: true},
		{name: "retry", mutation: `UPDATE jobs SET error_code='transient'`, want: false},
		{name: "leased", mutation: `UPDATE jobs SET state='leased',fence='active'`, want: false},
		{name: "missing-job", mutation: `DELETE FROM jobs WHERE kind='audio'`, want: false},
		{name: "stale-job", mutation: `INSERT INTO jobs(kind,file_id,source_revision,semantic_key,state,updated_at) SELECT kind,file_id,source_revision,'old-key','pending',updated_at FROM jobs WHERE kind='audio'`, want: false},
		{name: "changed-revision", mutation: `UPDATE files SET source_revision='changed'`, want: false},
		{name: "tombstone", mutation: `UPDATE files SET tombstoned_at='removed'`, want: false},
		{name: "ambiguous-identity", mutation: `INSERT INTO files SELECT 'other-id',root_id,'other.flac',device,inode,size,mtime_ns,source_revision,extension,status,first_seen_epoch,last_seen_epoch,tombstoned_at FROM files`, want: false},
		{name: "preserved-native-id", want: true, retainedID: true},
		{name: "unknown-native-id", want: true, zeroIdentity: true},
		{name: "mismatched-unknown-native-id", want: false, zeroIdentity: true, retainedID: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			state, err := OpenState(ctx, t.TempDir(), "indexed-guard", 1)
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
			file := SourceFile{RootID: root.ID, RelativePath: "track.flac", Device: 1, Inode: 2, Size: 42, Extension: ".flac"}
			if test.zeroIdentity {
				file.Device = 0
				file.Inode = 0
			}
			if err := normalizeSourceFile(&file); err != nil {
				t.Fatal(err)
			}
			stored := file
			if test.retainedID {
				stored.ID = "retained-id"
			}
			keys := map[string]string{"metadata": "v1", "audio": "v1"}
			if _, err := state.ObserveFiles(ctx, epoch, []SourceFile{stored}, keys); err != nil {
				t.Fatal(err)
			}
			err = state.write(ctx, true, func(conn *sql.Conn) error {
				tx, err := conn.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer func() { _ = tx.Rollback() }()
				if test.mutation != "" {
					if _, err := tx.ExecContext(ctx, test.mutation); err != nil {
						return err
					}
				}
				lookup, err := prepareScanUnchanged(ctx, tx, keys)
				if err != nil {
					return err
				}
				defer lookup.statement.Close()
				got, err := lookup.unchanged(ctx, &file)
				if err != nil {
					return err
				}
				if got != test.want {
					t.Errorf("unchanged=%v want=%v", got, test.want)
				}
				if got && file.ID != stored.ID {
					t.Errorf("identity=%q want=%q", file.ID, stored.ID)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Compare identical 10k-file observations on one temporary database. Each
// iteration gets a fresh epoch; both paths retain the same bounded transaction
// size. This isolates the guarded SQL lookup from directory enumeration/spill IO.
func BenchmarkScanIndexedUnchanged(b *testing.B) {
	ctx := context.Background()
	state, err := OpenState(ctx, b.TempDir(), "indexed-unchanged-benchmark", 2)
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()
	root, err := state.EnsureRoot(ctx, b.TempDir(), "music")
	if err != nil {
		b.Fatal(err)
	}
	keys := map[string]string{"metadata": "v1", "audio": "v1"}
	files := make([]SourceFile, 10000)
	for i := range files {
		files[i] = SourceFile{RootID: root.ID, RelativePath: fmt.Sprintf("%05d.flac", i), Device: 1, Inode: uint64(i + 1), Size: 42, Extension: ".flac"}
		if err := normalizeSourceFile(&files[i]); err != nil {
			b.Fatal(err)
		}
	}
	initial, err := state.BeginEpoch(ctx, []Root{root})
	if err != nil {
		b.Fatal(err)
	}
	for start := 0; start < len(files); start += scanDirectoryEntries {
		if _, err := state.ObserveFiles(ctx, initial, files[start:min(start+scanDirectoryEntries, len(files))], keys); err != nil {
			b.Fatal(err)
		}
	}
	if err := state.write(ctx, true, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `UPDATE jobs SET state='completed'`)
		return err
	}); err != nil {
		b.Fatal(err)
	}
	for _, guarded := range []bool{false, true} {
		name := "full-observation"
		if guarded {
			name = "indexed-unchanged"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				epoch, err := state.BeginEpoch(ctx, []Root{root})
				if err != nil {
					b.Fatal(err)
				}
				for start := 0; start < len(files); start += scanDirectoryEntries {
					chunk := files[start:min(start+scanDirectoryEntries, len(files))]
					if err := state.write(ctx, true, func(conn *sql.Conn) error {
						tx, err := conn.BeginTx(ctx, nil)
						if err != nil {
							return err
						}
						defer func() { _ = tx.Rollback() }()
						observation, err := prepareObservation(ctx, tx)
						if err != nil {
							return err
						}
						defer observation.close()
						var lookup *scanUnchangedLookup
						if guarded {
							lookup, err = prepareScanUnchanged(ctx, tx, keys)
							if err != nil {
								return err
							}
							defer lookup.statement.Close()
						}
						touch, err := tx.PrepareContext(ctx, `UPDATE files SET last_seen_epoch=? WHERE id=?`)
						if err != nil {
							return err
						}
						defer touch.Close()
						now := time.Now().UTC().Format(time.RFC3339Nano)
						for _, file := range chunk {
							unchanged := false
							if lookup != nil {
								unchanged, err = lookup.unchanged(ctx, &file)
								if err != nil {
									return err
								}
							}
							if unchanged {
								if _, err := touch.ExecContext(ctx, epoch, file.ID); err != nil {
									return err
								}
							} else {
								if err := observation.observe(ctx, epoch, &file, keys, now); err != nil {
									return err
								}
							}
						}
						return tx.Commit()
					}); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func TestScanInventoryLongPathsRespectByteBudget(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(ctx, t.TempDir(), "inventory-byte-budget", 2)
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
	// Valid relative components, with a total length unavailable on many test
	// hosts. Synthetic observations exercise database inventory independently
	// of the operating system's per-path limit.
	prefix := strings.Repeat("segment/", 1024)
	const count = 3000 // Below the row cap, but more than 23 MiB of path bytes.
	for start := 0; start < count; start += 256 {
		files := make([]SourceFile, min(256, count-start))
		for i := range files {
			files[i] = SourceFile{RootID: root.ID, RelativePath: fmt.Sprintf("%s%04d.flac", prefix, start+i), Extension: ".flac"}
		}
		if _, err := state.ObserveFiles(ctx, epoch, files, nil); err != nil {
			t.Fatal(err)
		}
	}
	inventory, err := state.loadScanInventory(ctx, []Root{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.disabled || inventory.retainedBytes != 0 || len(inventory.byPath) != 0 {
		t.Fatalf("long-path inventory exceeded byte budget: disabled=%v bytes=%d paths=%d", inventory.disabled, inventory.retainedBytes, len(inventory.byPath))
	}
	file := SourceFile{RootID: root.ID, RelativePath: fmt.Sprintf("%s%04d.flac", prefix, count-1), Extension: ".flac"}
	if err := normalizeSourceFile(&file); err != nil {
		t.Fatal(err)
	}
	if err := state.write(ctx, true, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		lookup, err := prepareScanUnchanged(ctx, tx, nil)
		if err != nil {
			return err
		}
		defer lookup.statement.Close()
		unchanged, err := lookup.unchanged(ctx, &file)
		if err != nil {
			return err
		}
		if !unchanged {
			t.Error("byte-capped cache lost indexed unchanged semantics")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
