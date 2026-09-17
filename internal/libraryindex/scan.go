package libraryindex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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

type directoryLeaseTracker struct {
	mu    sync.Mutex
	tasks map[string]DirectoryTask
}

func newDirectoryLeaseTracker() *directoryLeaseTracker {
	return &directoryLeaseTracker{tasks: make(map[string]DirectoryTask)}
}

func directoryTaskKey(task DirectoryTask) string {
	return fmt.Sprintf("%d\x00%s\x00%s", task.EpochID, task.RootID, task.RelativePath)
}

func (t *directoryLeaseTracker) add(tasks ...DirectoryTask) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, task := range tasks {
		t.tasks[directoryTaskKey(task)] = task
	}
}

func (t *directoryLeaseTracker) remove(task DirectoryTask) {
	t.mu.Lock()
	delete(t.tasks, directoryTaskKey(task))
	t.mu.Unlock()
}

func (t *directoryLeaseTracker) snapshot() []DirectoryTask {
	t.mu.Lock()
	defer t.mu.Unlock()
	tasks := make([]DirectoryTask, 0, len(t.tasks))
	for _, task := range t.tasks {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return directoryTaskKey(tasks[i]) < directoryTaskKey(tasks[j]) })
	return tasks
}

// Scan enumerates through the durable frontier. The in-memory task channel is
// bounded; child discovery writes directly to SQLite, so directory workers can
// never deadlock while all are trying to enqueue children.
func (s *State) Scan(ctx context.Context, options ScanOptions) (ScanReport, error) {
	if len(options.Roots) == 0 {
		return ScanReport{}, errors.New("library indexer: at least one root is required")
	}
	options.Workers = max(1, options.Workers)
	options.QueueDepth = max(options.Workers, options.QueueDepth)
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	leases := newDirectoryLeaseTracker()
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(jobHeartbeatPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.RenewDirectories(ctx, leases.snapshot(), jobLeaseDuration); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	defer func() { cancel(nil); <-heartbeatDone }()
	epoch, resumed, rescanned, err := s.BeginOrResumeEpoch(ctx, options.Roots)
	if err != nil {
		return ScanReport{}, err
	}
	report := ScanReport{Epoch: epoch, Resumed: resumed, RescannedDirectories: rescanned}
	if options.OnEpoch != nil {
		options.OnEpoch(epoch)
	}
	rootByID := make(map[string]Root, len(options.Roots))
	for _, root := range options.Roots {
		rootByID[root.ID] = root
	}
	tasks := make(chan DirectoryTask, options.QueueDepth)
	var workers sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	recordFatal := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
		cancel(err)
	}
	for range options.Workers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for task := range tasks {
				release := func() {}
				if options.Admission != nil {
					var err error
					release, err = options.Admission.Acquire(ctx, Reservation{SourceIO: 1, Files: 1, Memory: 1 << 20})
					if err != nil {
						recordFatal(err)
						continue
					}
				}
				root, ok := rootByID[task.RootID]
				if !ok {
					release()
					leases.remove(task)
					recordFatal(fmt.Errorf("library indexer: unknown root %s", task.RootID))
					continue
				}
				children, _, _, revision, scanErr := s.scanDirectory(ctx, task, root, options)
				release()
				if scanErr != nil {
					retryable := errors.Is(scanErr, errDirectoryChanged) && task.Attempt < maxDirectoryChangeAttempts
					if options.OnIssue != nil {
						code := "directory_error"
						if errors.Is(scanErr, errDirectoryChanged) {
							code = "directory_changed"
						}
						options.OnIssue(NewProcessingIssue("scan", root.Alias, task.RelativePath, code, scanErr, retryable))
					}
					if retryable {
						if err := s.RetryDirectory(ctx, task, scanErr.Error()); err != nil {
							recordFatal(err)
						}
						leases.remove(task)
						continue
					}
					atomic.AddInt64(&report.Errors, 1)
					if err := s.FailDirectory(ctx, task, scanErr.Error()); err != nil {
						recordFatal(err)
					}
					leases.remove(task)
					continue
				}
				if err := s.CompleteDirectory(ctx, task, children, revision); err != nil {
					recordFatal(err)
					leases.remove(task)
					continue
				}
				atomic.AddInt64(&report.Directories, 1)
				leases.remove(task)
			}
		}()
	}
	// The dispatcher continuously drains the disk frontier in small claims and
	// waits only when no durable task is currently claimable.
	dispatchErr := func() error {
		defer close(tasks)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			claimed, err := s.ClaimDirectories(ctx, epoch, options.QueueDepth, jobLeaseDuration)
			if err != nil {
				return err
			}
			if len(claimed) == 0 {
				finished, err := s.FinishEpoch(ctx, epoch)
				if err != nil {
					return err
				}
				if finished {
					report.Complete = report.Errors == 0
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(10 * time.Millisecond):
				}
				continue
			}
			leases.add(claimed...)
			for _, task := range claimed {
				select {
				case tasks <- task:
				case <-ctx.Done():
					leases.remove(task)
					return ctx.Err()
				}
			}
		}
	}()
	workers.Wait()
	if dispatchErr != nil {
		return report, dispatchErr
	}
	errMu.Lock()
	err = firstErr
	errMu.Unlock()
	if err == nil {
		progress, progressErr := s.ScanCandidateProgress(ctx, epoch, options.SemanticJobs)
		if progressErr != nil {
			return report, progressErr
		}
		report.Files = progress.Total
		report.AudioFiles = progress.Total
	}
	return report, err
}

func (s *State) scanDirectory(ctx context.Context, task DirectoryTask, root Root, options ScanOptions) ([]string, int64, int64, DirectoryRevision, error) {
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
	dir := filepath.Join(root.Path, rel)
	directory, err := os.Open(dir)
	if err != nil {
		return nil, 0, 0, DirectoryRevision{}, err
	}
	defer directory.Close()
	startInfo, err := directory.Stat()
	if err != nil {
		return nil, 0, 0, DirectoryRevision{}, err
	}
	var files, audio int64
	for {
		entries, readErr := directory.ReadDir(256)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, files, audio, DirectoryRevision{}, readErr
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		children := make([]string, 0, min(len(entries), 32))
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, files, audio, DirectoryRevision{}, err
			}
			childRel := filepath.Join(rel, entry.Name())
			if excludedPath(childRel, options.Exclusions) {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return nil, files, audio, DirectoryRevision{}, err
			}
			mode := info.Mode()
			if mode&os.ModeSymlink != 0 {
				// Following symlinks requires cycle/out-of-root handling and is kept
				// disabled until explicitly implemented; never follow implicitly.
				continue
			}
			if info.IsDir() {
				children = append(children, childRel)
				continue
			}
			if !mode.IsRegular() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if _, ok := audioExtensions[ext]; !ok {
				continue
			}
			files++
			device, inode := fileIdentity(info)
			observed, err := s.ObserveFile(ctx, task.EpochID, SourceFile{RootID: root.ID, RelativePath: childRel, Device: device, Inode: inode, Size: info.Size(), MTimeNS: info.ModTime().UnixNano(), Extension: ext}, options.SemanticJobs)
			if err != nil {
				return nil, files, audio, DirectoryRevision{}, err
			}
			if observed.needsProcessing && options.OnFile != nil {
				options.OnFile(FileActivity{RelativePath: childRel, Size: info.Size(), Extension: ext})
			}
			audio++
		}
		if err := s.AddDirectoryChildren(ctx, task, children); err != nil {
			return nil, files, audio, DirectoryRevision{}, err
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	endInfo, err := directory.Stat()
	if err != nil {
		return nil, files, audio, DirectoryRevision{}, err
	}
	if startInfo.ModTime() != endInfo.ModTime() || startInfo.Size() != endInfo.Size() {
		return nil, files, audio, DirectoryRevision{}, errDirectoryChanged
	}
	return nil, files, audio, DirectoryRevision{MTimeNS: endInfo.ModTime().UnixNano(), Size: endInfo.Size()}, nil
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
