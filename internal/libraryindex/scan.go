package libraryindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type ScanOptions struct {
	Roots          []Root
	Workers        int
	QueueDepth     int
	FollowSymlinks bool
	Exclusions     []string
	SemanticJobs   map[string]string
	Admission      *Admission
	OnFile         func(FileActivity)
	OnDirectory    func(string)
	OnIssue        func(ProcessingIssue)
	OnEpoch        func(int64)
	// StopAdmission closes on graceful shutdown. Already-claimed directory
	// tasks drain; no new frontier batch is claimed afterward.
	StopAdmission <-chan struct{}
}

// FileActivity is bounded progress metadata. It never includes an absolute
// source path and is not part of the durable analysis contract.
type FileActivity struct {
	RelativePath string
	Size         int64
	Extension    string
}

type ScanReport struct {
	Epoch                int64              `json:"epoch"`
	Directories          int64              `json:"directories"`
	Files                int64              `json:"files"`
	AudioFiles           int64              `json:"audioFiles"`
	Errors               int64              `json:"errors"`
	Complete             bool               `json:"complete"`
	Resumed              bool               `json:"resumed"`
	RescannedDirectories int64              `json:"rescannedDirectories"`
	Manifest             ScanManifestReport `json:"manifest"`
}

var audioExtensions = map[string]struct{}{`.flac`: {}, `.mp3`: {}, `.aac`: {}, `.m4a`: {}, `.mp4`: {}}

var errDirectoryChanged = errors.New("library indexer: directory changed during enumeration")

const maxDirectoryChangeAttempts = 8

const (
	// A commit batch closes at whichever bound is reached first. Walkers keep
	// enumerating while a batch commits, so batches grow when SQLite is slower
	// than the filesystem and stay small when it is not.
	scanCommitFiles       = 8192
	scanCommitDirectories = 256
	scanDirectoryEntries  = 256
	scanBufferBytes       = 8 << 20
	// Reserve 4 MiB for the 1 MiB spill cache, ReadDir entries, a decoded
	// record and JSON/SQLite transient copies. The other 4 MiB bounds retained
	// records with conservative string/slice overhead charges.
	scanDirectoryFixedBytes    = 4 << 20
	scanDirectoryRetainedBytes = scanBufferBytes - scanDirectoryFixedBytes
	scanDirectoryRecordBytes   = 1 << 20
)

// Results retain their bounded memory reservation until the committer releases
// them. Oversized directories spill to private temporary files, never source roots.
type scanDirectoryResult struct {
	task         DirectoryTask
	files        []SourceFile
	children     []string
	revision     DirectoryRevision
	err          error
	spill        string
	release      func()
	continuation bool
	reconciled   bool
}

type scanDirectoryChunk struct {
	Files    []SourceFile
	Children []string
}

// scanChunkBuffer bounds retained records by bytes and count for both walking
// and spill replay. Charges include spare slice capacity, string copies and
// JSON's worst-case escaping; a single unusually large record fails closed.
type scanChunkBuffer struct {
	chunk scanDirectoryChunk
	bytes int64
}

func scanFileBufferBytes(file SourceFile) int64 {
	return 1024 + 8*int64(len(file.RelativePath)) + 2*int64(len(file.RootID)+len(file.ID)+len(file.SourceRevision)+len(file.Extension))
}

func scanChildBufferBytes(path string) int64 { return 256 + 8*int64(len(path)) }

func (b *scanChunkBuffer) reserve(bytes int64, flush func(scanDirectoryChunk) error) error {
	if bytes > scanDirectoryRecordBytes {
		return errors.New("library indexer: scan record exceeds bounded directory buffer")
	}
	if b.bytes+bytes > scanDirectoryRetainedBytes || len(b.chunk.Files)+len(b.chunk.Children) >= scanDirectoryEntries {
		if err := b.flush(flush); err != nil {
			return err
		}
	}
	b.bytes += bytes
	return nil
}

func (b *scanChunkBuffer) addFile(file SourceFile, flush func(scanDirectoryChunk) error) error {
	if err := b.reserve(scanFileBufferBytes(file), flush); err != nil {
		return err
	}
	b.chunk.Files = append(b.chunk.Files, file)
	return nil
}

func (b *scanChunkBuffer) addChild(path string, flush func(scanDirectoryChunk) error) error {
	if err := b.reserve(scanChildBufferBytes(path), flush); err != nil {
		return err
	}
	b.chunk.Children = append(b.chunk.Children, path)
	return nil
}

func (b *scanChunkBuffer) flush(consume func(scanDirectoryChunk) error) error {
	if len(b.chunk.Files)+len(b.chunk.Children) == 0 {
		return nil
	}
	if err := consume(b.chunk); err != nil {
		return err
	}
	b.chunk = scanDirectoryChunk{}
	b.bytes = 0
	return nil
}

func (r *scanDirectoryResult) cleanup() {
	if r.spill != "" {
		_ = os.Remove(r.spill)
		r.spill = ""
	}
	if r.release != nil {
		r.release()
		r.release = nil
	}
}

// Scan claims bounded batches from the durable frontier. Successful directory
// enumeration is published by one committer before children can be claimed.
// Incomplete parents remain recoverable, including between spilled chunks.
func (s *State) Scan(ctx context.Context, options ScanOptions) (ScanReport, error) {
	if len(options.Roots) == 0 {
		return ScanReport{}, errors.New("library indexer: at least one root is required")
	}
	options.Workers = max(1, options.Workers)
	options.QueueDepth = max(options.Workers, options.QueueDepth)
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	epoch, resumed, rescanned, err := s.BeginOrResumeEpoch(ctx, options.Roots, options.FollowSymlinks)
	if err != nil {
		return ScanReport{}, err
	}
	report := ScanReport{Epoch: epoch, Resumed: resumed, RescannedDirectories: rescanned}
	// The coordinator exclusively owns this state directory. Staging is never
	// authoritative, so discard an interrupted process's spills before resume.
	staging := filepath.Join(s.dir, "scan-staging")
	if err := os.RemoveAll(staging); err != nil {
		return report, err
	}
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return report, err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if options.OnEpoch != nil {
		options.OnEpoch(epoch)
	}
	rootByID := make(map[string]Root, len(options.Roots))
	for _, root := range options.Roots {
		rootByID[root.ID] = root
	}
	var inventory *scanInventory
	if options.Admission != nil {
		used, capacity := options.Admission.Usage()
		if capacity.Memory-used.Memory < s.pageCacheBytes+scanBufferBytes {
			return report, errors.New("library indexer: scan SQLite caches and directory buffer exceed available memory capacity")
		}
		releasePages, err := options.Admission.Acquire(ctx, Reservation{Memory: s.pageCacheBytes})
		if err != nil {
			return report, err
		}
		defer releasePages()
		used, capacity = options.Admission.Usage()
		const cacheBytes = scanInventoryBytes
		if capacity.Memory-used.Memory >= cacheBytes+scanBufferBytes {
			release, err := options.Admission.Acquire(ctx, Reservation{Memory: cacheBytes})
			if err != nil {
				return report, err
			}
			defer release()
		} else {
			inventory = &scanInventory{disabled: true}
		}
	}
	if inventory == nil {
		inventory, err = s.loadScanInventory(ctx, options.Roots, options.SemanticJobs)
		if err != nil {
			return report, err
		}
	}
	// Keep SQLite's periodic checkpoints enabled throughout the scan. A large
	// tree must not retain every rewritten page until the scan finishes.
	for {
		if shutdownRequested(options.StopAdmission) {
			return report, ErrShutdownRequested
		}
		tasks, err := s.ClaimDirectories(ctx, epoch, min(options.QueueDepth, scanCommitDirectories), 24*time.Hour)
		if err != nil {
			return report, err
		}
		if len(tasks) == 0 {
			break
		}
		work := make(chan DirectoryTask, len(tasks))
		for _, task := range tasks {
			work <- task
		}
		close(work)
		results := make(chan scanDirectoryResult, min(options.Workers, len(tasks)))
		var workers sync.WaitGroup
		for range min(options.Workers, len(tasks)) {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for task := range work {
					var result scanDirectoryResult
					for {
						result = s.walkScanDirectory(ctx, task, rootByID, options)
						retry := errors.Is(result.err, errDirectoryChanged) && task.Attempt < maxDirectoryChangeAttempts
						if result.err != nil && options.OnIssue != nil {
							code := "directory_error"
							if errors.Is(result.err, errDirectoryChanged) {
								code = "directory_changed"
							}
							options.OnIssue(NewProcessingIssue("scan", rootByID[task.RootID].Alias, task.RelativePath, code, result.err, retry))
						}
						if !retry {
							break
						}
						result.cleanup()
						task.Attempt++
					}
					select {
					case results <- result:
					case <-ctx.Done():
						result.cleanup()
						return
					}
				}
			}()
		}
		go func() { workers.Wait(); close(results) }()
		if err := s.commitScanResults(ctx, epoch, results, func() (*scanInventory, error) { return inventory, nil }, options.SemanticJobs, &report); err != nil {
			cancel(err)
			for result := range results {
				result.cleanup()
			}
			return report, err
		}
	}
	if err := context.Cause(ctx); err != nil {
		return report, err
	}
	finished, err := s.FinishEpoch(ctx, epoch)
	if err != nil {
		return report, err
	}
	if !finished {
		return report, errors.New("library indexer: scan finished with outstanding directories")
	}
	if err := s.reader.QueryRowContext(ctx, `SELECT COALESCE(SUM(permission_errors),0) FROM scan_scopes WHERE epoch_id=?`, epoch).Scan(&report.Errors); err != nil {
		return report, err
	}
	report.Complete = report.Errors == 0
	progress, err := s.ScanCandidateProgress(ctx, epoch, options.SemanticJobs)
	if err != nil {
		return report, err
	}
	report.Files = progress.Files
	report.AudioFiles = progress.Files
	return report, nil
}

// commitScanResults is the only scan writer. It drains whatever results are
// ready into one transaction, then commits, so fsync cost is shared by many
// directories while the walkers continue.
func (s *State) commitScanResults(ctx context.Context, epoch int64, results <-chan scanDirectoryResult, loadInventory func() (*scanInventory, error), semanticKeys map[string]string, report *ScanReport) error {
	inventory, err := loadInventory()
	if err != nil {
		return err
	}
	for first := range results {
		batch := []scanDirectoryResult{first}
		files := len(first.files)
	drain:
		for len(batch) < scanCommitDirectories && files < scanCommitFiles {
			select {
			case result, ok := <-results:
				if !ok {
					break drain
				}
				batch = append(batch, result)
				files += len(result.files)
			default:
				break drain
			}
		}
		err := func() error {
			defer func() {
				for i := range batch {
					batch[i].cleanup()
				}
			}()
			// Spilled results are streamed through bounded transactions. Enumeration
			// succeeded before any chunk is published. The parent stays leased until
			// its final chunk, so interruption re-enumerates it safely on resume.
			var small []scanDirectoryResult
			for _, result := range batch {
				if result.spill == "" || result.err != nil {
					small = append(small, result)
					continue
				}

				err := readScanSpill(ctx, result.spill, func(chunk scanDirectoryChunk) error {
					part := scanDirectoryResult{task: result.task, files: chunk.Files, children: chunk.Children, continuation: true, reconciled: result.reconciled}
					if err := s.commitScanBatch(ctx, epoch, []scanDirectoryResult{part}, inventory, semanticKeys, report); err != nil {
						return err
					}
					result.reconciled = true
					return nil
				})
				if err != nil {
					return err
				}

				result.files = nil
				result.children = nil
				small = append(small, result)
			}
			return s.commitScanBatch(ctx, epoch, small, inventory, semanticKeys, report)
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *State) commitScanBatch(ctx context.Context, epoch int64, batch []scanDirectoryResult, inventory *scanInventory, semanticKeys map[string]string, report *ScanReport) error {
	var completed, failed int64
	err := s.write(ctx, true, func(conn *sql.Conn) error {
		completed, failed = 0, 0
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
		touch, err := tx.PrepareContext(ctx, `UPDATE files SET last_seen_epoch=? WHERE id=?`)
		if err != nil {
			return err
		}
		defer touch.Close()
		child, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO directory_frontier(epoch_id,root_id,relative_path,state) VALUES(?,?,?,'pending')`)
		if err != nil {
			return err
		}
		defer child.Close()
		complete, err := tx.PrepareContext(ctx, `UPDATE directory_frontier
			SET state='completed',fence='',lease_until=NULL,error='',completed_mtime_ns=?,completed_size=?
			WHERE epoch_id=? AND root_id=? AND relative_path=? AND state='leased' AND fence=?`)
		if err != nil {
			return err
		}
		defer complete.Close()
		fail, err := tx.PrepareContext(ctx, `UPDATE directory_frontier SET state='failed',error=?,fence='',lease_until=NULL
			WHERE epoch_id=? AND root_id=? AND relative_path=? AND state='leased' AND fence=?`)
		if err != nil {
			return err
		}
		defer fail.Close()
		scope, err := tx.PrepareContext(ctx, `UPDATE scan_scopes SET outstanding_dirs=outstanding_dirs+?,
			permission_errors=permission_errors+?,status=CASE WHEN ?>0 THEN 'partial' ELSE status END
			WHERE epoch_id=? AND root_id=?`)
		if err != nil {
			return err
		}
		defer scope.Close()
		var indexed *scanUnchangedLookup
		defer func() {
			if indexed != nil {
				_ = indexed.statement.Close()
			}
		}()
		updatedAt := time.Now().UTC().Format(time.RFC3339Nano)
		for _, result := range batch {
			task := result.task
			if result.err != nil {
				detail := result.err.Error()
				if len(detail) > 4096 {
					detail = detail[:4096]
				}
				if err := requireOneRow(fail.ExecContext(ctx, detail, epoch, task.RootID, task.RelativePath, task.Fence)); err != nil {
					return fmt.Errorf("library indexer: directory failure lost its frontier row: %w", err)
				}
				if _, err := scope.ExecContext(ctx, -1, 1, 1, epoch, task.RootID); err != nil {
					return err
				}
				failed++
				continue
			}
			if result.continuation {
				var valid int
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM directory_frontier WHERE epoch_id=? AND root_id=? AND relative_path=? AND state='leased' AND fence=?`, epoch, task.RootID, task.RelativePath, task.Fence).Scan(&valid); err != nil {
					return err
				}
				if valid != 1 {
					return errors.New("library indexer: stale directory fence")
				}
			}

			if !result.reconciled && task.Attempt > 1 {
				// A successful replay replaces this directory's membership, including
				// observations from chunks committed before an interrupted attempt.
				// Only epoch marks are reset: a later failed scope still preserves all
				// prior inventory, and successful observations restore their marks.
				prefix := ""
				if task.RelativePath != "." && task.RelativePath != "" {
					prefix = filepath.Clean(task.RelativePath) + string(filepath.Separator)
				}
				query := `UPDATE files SET last_seen_epoch=? WHERE root_id=? AND last_seen_epoch=? AND instr(substr(relative_path,length(?)+1),?)=0`
				args := []any{epoch - 1, task.RootID, epoch, prefix, string(filepath.Separator)}
				if prefix != "" {
					upper := prefix[:len(prefix)-1] + string(filepath.Separator+1)
					query += ` AND relative_path>=? AND relative_path<?`
					args = append(args, prefix, upper)
				}
				if _, err := tx.ExecContext(ctx, query, args...); err != nil {
					return err
				}
			}
			for index := range result.files {
				file := &result.files[index]
				unchanged := inventory.unchanged(file)
				if !unchanged && inventory.disabled {
					if indexed == nil {
						indexed, err = prepareScanUnchanged(ctx, tx, semanticKeys)
						if err != nil {
							return err
						}
					}
					unchanged, err = indexed.unchanged(ctx, file)
					if err != nil {
						return err
					}
				}
				if unchanged {
					if _, err := touch.ExecContext(ctx, epoch, file.ID); err != nil {
						return err
					}
					continue
				}
				if err := observation.observeKnown(ctx, epoch, file, semanticKeys, updatedAt, !inventory.unseen(*file)); err != nil {
					return err
				}
				inventory.touched(*file)
			}
			added := int64(0)
			for _, path := range result.children {
				res, err := child.ExecContext(ctx, epoch, task.RootID, path)
				if err != nil {
					return err
				}
				n, _ := res.RowsAffected()
				added += n
			}
			if result.continuation {
				if _, err := scope.ExecContext(ctx, added, 0, 0, epoch, task.RootID); err != nil {
					return err
				}
				continue
			}
			if err := requireOneRow(complete.ExecContext(ctx, result.revision.MTimeNS, result.revision.Size, epoch, task.RootID, task.RelativePath, task.Fence)); err != nil {
				return fmt.Errorf("library indexer: directory completion lost its frontier row: %w", err)
			}
			if _, err := scope.ExecContext(ctx, added-1, 0, 0, epoch, task.RootID); err != nil {
				return err
			}
			completed++
		}
		return tx.Commit()
	})
	if err == nil {
		report.Directories += completed
		report.Errors += failed
	}
	return err
}

func requireOneRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("expected one row, updated %d", n)
	}
	return nil
}

// walkScanDirectory reads one directory without touching durable state. Files
// are sorted in memory or a bounded private SQLite spill for the committer;
// a directory whose revision
// changed while it was read reports errDirectoryChanged and nothing else.
func (s *State) walkScanDirectory(ctx context.Context, task DirectoryTask, rootByID map[string]Root, options ScanOptions) (result scanDirectoryResult) {
	result.task = task
	root, ok := rootByID[task.RootID]
	if !ok {
		result.err = fmt.Errorf("library indexer: unknown root %s", task.RootID)
		return result
	}
	if options.Admission != nil {
		lease, err := options.Admission.AcquireLease(ctx, Reservation{SourceIO: 1, Files: 3, Memory: scanBufferBytes})
		if err != nil {
			result.err = err
			return result
		}
		result.release = lease.Release
		defer func() { _ = lease.ReleasePart(Reservation{SourceIO: 1, Files: 2}) }()
	}
	defer func() {
		if result.err != nil {
			result.cleanup()
			result.files = nil
			result.children = nil
		}
	}()
	rel := task.RelativePath
	if rel == "." {
		rel = ""
	}
	if options.OnDirectory != nil {
		display := root.Alias
		if rel != "" {
			display = filepath.Join(display, rel)
		}
		options.OnDirectory(display)
	}
	directory, err := os.Open(filepath.Join(root.Path, rel))
	if err != nil {
		result.err = err
		return result
	}
	defer directory.Close()
	startInfo, err := directory.Stat()
	if err != nil {
		result.err = err
		return result
	}
	var spill *sql.DB
	var spillTx *sql.Tx
	defer func() {
		if spillTx != nil {
			_ = spillTx.Rollback()
		}
		if spill != nil {
			_ = spill.Close()
		}
	}()
	var buffer scanChunkBuffer
	flush := func(chunk scanDirectoryChunk) error {
		if spill == nil {
			var err error
			spill, spillTx, result.spill, err = newScanSpill(ctx, s.dir)
			if err != nil {
				return err
			}
		}
		return writeScanSpill(ctx, spillTx, chunk)
	}

	for {
		entries, readErr := directory.ReadDir(scanDirectoryEntries)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			result.err = readErr
			return result
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				result.err = err
				return result
			}
			childRel := filepath.Join(rel, entry.Name())
			if excludedPath(childRel, options.Exclusions) {
				continue
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if !options.FollowSymlinks {
					continue
				}
				targetInfo, statErr := os.Stat(filepath.Join(root.Path, childRel))
				if statErr != nil || !targetInfo.IsDir() {
					// Directory links are opt-in. Broken links and links to files
					// remain non-source entries, matching the default scan policy.
					continue
				}
				cycle, cycleErr := directorySymlinkCreatesCycle(root.Path, childRel, targetInfo)
				if cycleErr != nil {
					result.err = cycleErr
					return result
				}
				if cycle {
					if options.OnIssue != nil {
						options.OnIssue(NewProcessingIssue("scan", root.Alias, childRel, "symlink_cycle", errors.New("directory symlink resolves to an ancestor"), false))
					}
					continue
				}
				if err := buffer.addChild(childRel, flush); err != nil {
					result.err = err
					return result
				}
				continue
			}
			info, err := entry.Info()
			if err != nil {
				result.err = err
				return result
			}
			if info.IsDir() {
				if err := buffer.addChild(childRel, flush); err != nil {
					result.err = err
					return result
				}
				continue
			}
			if !info.Mode().IsRegular() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if _, ok := audioExtensions[ext]; !ok {
				continue
			}
			device, inode := fileIdentity(info)
			file := SourceFile{RootID: root.ID, RelativePath: childRel, Device: device, Inode: inode, Size: info.Size(), MTimeNS: info.ModTime().UnixNano(), Extension: ext}
			if err := normalizeSourceFile(&file); err != nil {
				result.err = err
				return result
			}
			if err := buffer.addFile(file, flush); err != nil {
				result.err = err
				return result
			}
			// Report discovery before the closing revision check, as a file seen
			// here can still be discarded if the directory changes under the walk.
			if options.OnFile != nil {
				options.OnFile(FileActivity{RelativePath: file.RelativePath, Size: file.Size, Extension: file.Extension})
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	if spill != nil {
		if err := buffer.flush(flush); err != nil {
			result.err = err
			return result
		}
	} else {
		result.files = buffer.chunk.Files
		result.children = buffer.chunk.Children
	}

	endInfo, err := directory.Stat()
	if err != nil {
		result.err = err
		return result
	}
	if startInfo.ModTime() != endInfo.ModTime() || startInfo.Size() != endInfo.Size() {
		result.err = errDirectoryChanged
		return result
	}
	for _, child := range result.children {
		clean := filepath.Clean(child)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			result.err = errors.New("library indexer: child directory escapes root")
			return result
		}
	}
	if spillTx != nil {
		if err := spillTx.Commit(); err != nil {
			result.err = err
			return result
		}
	} else {
		sort.Slice(result.files, func(i, j int) bool { return result.files[i].RelativePath < result.files[j].RelativePath })
		sort.Strings(result.children)
	}
	result.revision = DirectoryRevision{MTimeNS: endInfo.ModTime().UnixNano(), Size: endInfo.Size()}
	return result
}

// directorySymlinkCreatesCycle compares the resolved target with every logical
// ancestor. This permits links to directories outside the physical root while
// preventing self-links and longer ancestor cycles from expanding forever.
func directorySymlinkCreatesCycle(rootPath, childRelative string, targetInfo os.FileInfo) (bool, error) {
	parent := filepath.Dir(filepath.Clean(childRelative))
	for {
		ancestorPath := rootPath
		if parent != "." {
			ancestorPath = filepath.Join(rootPath, parent)
		}
		ancestorInfo, err := os.Stat(ancestorPath)
		if err != nil {
			return false, err
		}
		if os.SameFile(targetInfo, ancestorInfo) {
			return true, nil
		}
		if parent == "." {
			return false, nil
		}
		next := filepath.Dir(parent)
		if next == parent {
			return false, errors.New("library indexer: invalid directory symlink ancestry")
		}
		parent = next
	}
}

func excludedPath(relative string, exclusions []string) bool {
	clean := filepath.Clean(relative)
	for _, excluded := range exclusions {
		excluded = filepath.Clean(excluded)
		if clean == excluded || strings.HasPrefix(clean, excluded+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

var _ io.Closer = (*State)(nil)
