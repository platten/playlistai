package libraryindex

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/installlock"
	"github.com/platten/playlistai/internal/sqliteuri"
)

const stateSchemaVersion = 2

var rootAliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type writerRequest struct {
	ctx  context.Context
	fn   func(*sql.Conn) error
	txFn func(*sql.Tx) error
	done chan error
}

// State owns one mutating coordinator, one SQLite writer connection and a
// separate bounded read-only pool. Workers never receive a transaction handle.
type State struct {
	dir         string
	path        string
	writerDB    *sql.DB
	writer      *sql.Conn
	reader      *sql.DB
	control     chan writerRequest
	results     chan writerRequest
	batches     chan writerRequest
	stop        chan struct{}
	done        chan struct{}
	releaseLock func() error
	closeOnce   sync.Once
	closeErr    error
}

type LockOwner struct {
	PID       int    `json:"pid"`
	StartedAt string `json:"startedAt"`
	Command   string `json:"command"`
}

func OpenState(ctx context.Context, dir, command string, readConnections int) (*State, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(abs, ".coordinator.lock")
	release, err := installlock.TryAcquire(lockPath)
	if err != nil {
		owner, _ := os.ReadFile(lockPath)
		return nil, fmt.Errorf("library indexer: state is already being mutated (%s): %w", strings.TrimSpace(string(owner)), err)
	}
	locked := true
	defer func() {
		if locked {
			_ = release()
		}
	}()
	owner, _ := json.Marshal(LockOwner{PID: os.Getpid(), StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Command: command})
	if f, openErr := os.OpenFile(lockPath, os.O_WRONLY|os.O_TRUNC, 0o600); openErr == nil {
		_, _ = f.Write(append(owner, '\n'))
		_ = f.Sync()
		_ = f.Close()
	}
	path := filepath.Join(abs, "library-index.sqlite")
	dsn, err := sqliteuri.Writable(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := migrateState(ctx, conn); err != nil {
		_ = conn.Close()
		_ = db.Close()
		return nil, err
	}
	// Owning the kernel coordinator lock proves no prior mutator remains. Reclaim
	// unfinished leases immediately; their fencing tokens can no longer commit.
	if _, err := conn.ExecContext(ctx, `UPDATE jobs SET state='pending',fence='',lease_until=NULL,updated_at=? WHERE state='leased'`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		_ = conn.Close()
		_ = db.Close()
		return nil, err
	}
	roDSN, err := sqliteuri.ReadOnly(path, false)
	if err != nil {
		_ = conn.Close()
		_ = db.Close()
		return nil, err
	}
	reader, err := sql.Open("sqlite", roDSN+"&_pragma="+url.QueryEscape("busy_timeout(5000)"))
	if err != nil {
		_ = conn.Close()
		_ = db.Close()
		return nil, err
	}
	readConnections = max(1, readConnections)
	reader.SetMaxOpenConns(readConnections)
	reader.SetMaxIdleConns(readConnections)
	s := &State{dir: abs, path: path, writerDB: db, writer: conn, reader: reader,
		control: make(chan writerRequest, 32), results: make(chan writerRequest, 64), batches: make(chan writerRequest, 256), stop: make(chan struct{}), done: make(chan struct{}), releaseLock: release}
	go s.writerLoop()
	locked = false
	return s, nil
}

func migrateState(ctx context.Context, conn *sql.Conn) error {
	var version string
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA synchronous=FULL; PRAGMA wal_autocheckpoint=1000;
CREATE TABLE IF NOT EXISTS state_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);`); err != nil {
		return err
	}
	_ = conn.QueryRowContext(ctx, `SELECT value FROM state_meta WHERE key='schema_version'`).Scan(&version)
	if version != "" && version != "1" && version != strconv.Itoa(stateSchemaVersion) {
		return fmt.Errorf("library indexer: unsupported state schema %q", version)
	}
	_, err := conn.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS roots (
 id TEXT PRIMARY KEY, path TEXT NOT NULL UNIQUE, alias TEXT NOT NULL UNIQUE,
 last_successful_epoch INTEGER NOT NULL DEFAULT 0, offline INTEGER NOT NULL DEFAULT 0,
 created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS scan_epochs (
 id INTEGER PRIMARY KEY AUTOINCREMENT, started_at TEXT NOT NULL, finished_at TEXT,
 status TEXT NOT NULL, error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS scan_scopes (
 epoch_id INTEGER NOT NULL REFERENCES scan_epochs(id), root_id TEXT NOT NULL REFERENCES roots(id),
 status TEXT NOT NULL, outstanding_dirs INTEGER NOT NULL DEFAULT 0,
 permission_errors INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(epoch_id,root_id)
);
CREATE TABLE IF NOT EXISTS directory_frontier (
	 epoch_id INTEGER NOT NULL, root_id TEXT NOT NULL, relative_path TEXT NOT NULL,
	 state TEXT NOT NULL, attempt INTEGER NOT NULL DEFAULT 0, fence TEXT NOT NULL DEFAULT '',
	 lease_until TEXT, error TEXT NOT NULL DEFAULT '', completed_mtime_ns INTEGER NOT NULL DEFAULT -1,
	 completed_size INTEGER NOT NULL DEFAULT -1,
 PRIMARY KEY(epoch_id,root_id,relative_path)
);
CREATE TABLE IF NOT EXISTS files (
 id TEXT PRIMARY KEY, root_id TEXT NOT NULL REFERENCES roots(id), relative_path TEXT NOT NULL,
 device INTEGER NOT NULL, inode INTEGER NOT NULL, size INTEGER NOT NULL, mtime_ns INTEGER NOT NULL,
 source_revision TEXT NOT NULL, extension TEXT NOT NULL, status TEXT NOT NULL,
 first_seen_epoch INTEGER NOT NULL, last_seen_epoch INTEGER NOT NULL, tombstoned_at TEXT,
 UNIQUE(root_id,relative_path)
);
CREATE INDEX IF NOT EXISTS files_identity ON files(root_id,device,inode);
CREATE TABLE IF NOT EXISTS jobs (
 id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, file_id TEXT NOT NULL REFERENCES files(id),
 source_revision TEXT NOT NULL, semantic_key TEXT NOT NULL, state TEXT NOT NULL,
 attempt INTEGER NOT NULL DEFAULT 0, fence TEXT NOT NULL DEFAULT '', lease_until TEXT,
 retry_count INTEGER NOT NULL DEFAULT 0, error_code TEXT NOT NULL DEFAULT '', error_detail TEXT NOT NULL DEFAULT '',
 updated_at TEXT NOT NULL, UNIQUE(kind,file_id,semantic_key)
);
CREATE INDEX IF NOT EXISTS jobs_claim ON jobs(kind,state,updated_at,id);
CREATE TABLE IF NOT EXISTS track_metadata (
 file_id TEXT NOT NULL, source_revision TEXT NOT NULL, contract TEXT NOT NULL,
 data BLOB NOT NULL, PRIMARY KEY(file_id,contract)
);
CREATE TABLE IF NOT EXISTS dsp_results (
 file_id TEXT NOT NULL, source_revision TEXT NOT NULL, contract TEXT NOT NULL,
 data BLOB NOT NULL, PRIMARY KEY(file_id,contract)
);
CREATE TABLE IF NOT EXISTS mert_results (
 file_id TEXT NOT NULL, source_revision TEXT NOT NULL, contract TEXT NOT NULL,
 dimension INTEGER NOT NULL, vector BLOB NOT NULL, data BLOB NOT NULL,
 PRIMARY KEY(file_id,contract)
);
CREATE TABLE IF NOT EXISTS runs (
 id TEXT PRIMARY KEY, command TEXT NOT NULL, started_at TEXT NOT NULL, finished_at TEXT,
 status TEXT NOT NULL, plan BLOB NOT NULL, detail TEXT NOT NULL DEFAULT ''
);
`)
	if err != nil {
		return err
	}
	// Schema v1 did not retain a directory revision. Keep the migration
	// idempotent so a process interrupted between ALTER TABLE and the version
	// update can safely continue on its next start.
	for _, column := range []struct {
		name       string
		definition string
	}{
		{name: "completed_mtime_ns", definition: "INTEGER NOT NULL DEFAULT -1"},
		{name: "completed_size", definition: "INTEGER NOT NULL DEFAULT -1"},
	} {
		exists, err := sqliteColumnExists(ctx, conn, "directory_frontier", column.name)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := conn.ExecContext(ctx, `ALTER TABLE directory_frontier ADD COLUMN `+column.name+` `+column.definition); err != nil {
				return err
			}
		}
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO state_meta(key,value) VALUES('schema_version',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.Itoa(stateSchemaVersion))
	return err
}

func sqliteColumnExists(ctx context.Context, conn *sql.Conn, table, column string) (bool, error) {
	rows, err := conn.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *State) writerLoop() {
	defer close(s.done)
	for {
		var req writerRequest
		select {
		case <-s.stop:
			return
		default:
		}
		select {
		case req = <-s.control:
		default:
			select {
			case <-s.stop:
				return
			case req = <-s.control:
			case req = <-s.batches:
				s.executeWriterBatch(req)
				continue
			case req = <-s.results:
			}
		}
		err := req.ctx.Err()
		if err == nil {
			err = req.fn(s.writer)
		}
		select {
		case req.done <- err:
		case <-req.ctx.Done():
		}
	}
}

func (s *State) executeWriterBatch(first writerRequest) {
	const maximum = 64
	requests := make([]writerRequest, 0, maximum)
	requests = append(requests, first)
	timer := time.NewTimer(5 * time.Millisecond)
dequeue:
	for len(requests) < maximum {
		select {
		case request := <-s.batches:
			requests = append(requests, request)
		case <-timer.C:
			break dequeue
		default:
			// Give concurrently completing workers a short coalescing window.
			select {
			case request := <-s.batches:
				requests = append(requests, request)
			case <-timer.C:
				break dequeue
			}
		}
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	active := requests[:0]
	for _, request := range requests {
		if err := request.ctx.Err(); err != nil {
			request.done <- err
		} else {
			active = append(active, request)
		}
	}
	if len(active) == 0 {
		return
	}
	tx, err := s.writer.BeginTx(context.Background(), nil)
	if err == nil {
		for _, request := range active {
			if err = request.txFn(tx); err != nil {
				break
			}
		}
	}
	if err == nil {
		err = tx.Commit()
	} else if tx != nil {
		_ = tx.Rollback()
	}
	if err == nil {
		for _, request := range active {
			request.done <- nil
		}
		return
	}
	// One stale/canceled item must not prevent independent valid commits. Retry
	// individually while retaining the same fence/revision predicates.
	for _, request := range active {
		individual, beginErr := s.writer.BeginTx(request.ctx, nil)
		if beginErr == nil {
			beginErr = request.txFn(individual)
		}
		if beginErr == nil {
			beginErr = individual.Commit()
		} else if individual != nil {
			_ = individual.Rollback()
		}
		request.done <- beginErr
	}
}

func (s *State) write(ctx context.Context, control bool, fn func(*sql.Conn) error) error {
	req := writerRequest{ctx: ctx, fn: fn, done: make(chan error, 1)}
	queue := s.results
	if control {
		queue = s.control
	}
	select {
	case queue <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return errors.New("library indexer: state writer is closed")
	}
	select {
	case err := <-req.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return errors.New("library indexer: state writer stopped before acknowledgment")
	}
}

func (s *State) writeBatch(ctx context.Context, fn func(*sql.Tx) error) error {
	req := writerRequest{ctx: ctx, txFn: fn, done: make(chan error, 1)}
	select {
	case s.batches <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return errors.New("library indexer: state writer is closed")
	}
	select {
	case err := <-req.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return errors.New("library indexer: state writer stopped before batch acknowledgment")
	}
}

func (s *State) Close() error {
	s.closeOnce.Do(func() {
		close(s.stop)
		<-s.done
		s.closeErr = errors.Join(s.reader.Close(), s.writer.Close(), s.writerDB.Close(), s.releaseLock())
	})
	return s.closeErr
}

func (s *State) Reader() *sql.DB { return s.reader }
func (s *State) Path() string    { return s.path }

type Root struct {
	ID    string
	Path  string
	Alias string
}

func (s *State) EnsureRoot(ctx context.Context, path, alias string) (Root, error) {
	return s.ensureRoot(ctx, path, alias, true)
}

// EnsureAdditionalRoot adds an independently named source without allowing an
// existing logical root to be silently repointed. Repointing is a remount and
// must use EnsureRoot explicitly.
func (s *State) EnsureAdditionalRoot(ctx context.Context, path, alias string) (Root, error) {
	return s.ensureRoot(ctx, path, alias, false)
}

func (s *State) ensureRoot(ctx context.Context, path, alias string, allowPathUpdate bool) (Root, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Root{}, err
	}
	abs = filepath.Clean(abs)
	alias = strings.TrimSpace(alias)
	if alias == "" {
		alias = filepath.Base(abs)
		if !rootAliasPattern.MatchString(alias) {
			sum := sha256.Sum256([]byte("playlist-indexer-default-root-alias/v1\x00" + abs))
			alias = "root-" + hex.EncodeToString(sum[:6])
		}
	}
	if !rootAliasPattern.MatchString(alias) {
		return Root{}, fmt.Errorf("library indexer: invalid root alias %q", alias)
	}
	rootSum := sha256.Sum256([]byte("playlist-indexer-root-alias/v1\x00" + alias))
	root := Root{ID: "root:" + hex.EncodeToString(rootSum[:16]), Path: abs, Alias: alias}
	err = s.write(ctx, true, func(conn *sql.Conn) error {
		conflict := "DO NOTHING"
		if allowPathUpdate {
			conflict = "DO UPDATE SET path=excluded.path"
		}
		_, err := conn.ExecContext(ctx, `INSERT INTO roots(id,path,alias,created_at) VALUES(?,?,?,?) ON CONFLICT(alias) `+conflict, root.ID, root.Path, root.Alias, time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		requestedPath := root.Path
		if err := conn.QueryRowContext(ctx, `SELECT id,path,alias FROM roots WHERE alias=?`, root.Alias).Scan(&root.ID, &root.Path, &root.Alias); err != nil {
			return err
		}
		if !allowPathUpdate && filepath.Clean(root.Path) != requestedPath {
			return fmt.Errorf("library indexer: append root alias %q already maps to %q; use --root-alias %s=%s to update its mount path", root.Alias, root.Path, root.Alias, requestedPath)
		}
		return nil
	})
	return root, err
}

func (s *State) BeginEpoch(ctx context.Context, roots []Root) (int64, error) {
	var epoch int64
	err := s.write(ctx, true, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		res, err := tx.ExecContext(ctx, `INSERT INTO scan_epochs(started_at,status) VALUES(?, 'enumerating')`, time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		epoch, err = res.LastInsertId()
		if err != nil {
			return err
		}
		for _, root := range roots {
			if _, err := tx.ExecContext(ctx, `INSERT INTO scan_scopes(epoch_id,root_id,status,outstanding_dirs) VALUES(?,?,'enumerating',1)`, epoch, root.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO directory_frontier(epoch_id,root_id,relative_path,state) VALUES(?,?,'.','pending')`, epoch, root.ID); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
	return epoch, err
}

func (s *State) BeginOrResumeEpoch(ctx context.Context, roots []Root) (int64, bool, int64, error) {
	var epoch int64
	var resumable bool
	err := s.write(ctx, true, func(conn *sql.Conn) error {
		if err := conn.QueryRowContext(ctx, `SELECT id FROM scan_epochs WHERE status='enumerating' ORDER BY id DESC LIMIT 1`).Scan(&epoch); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if epoch == 0 {
			return nil
		}
		rows, err := conn.QueryContext(ctx, `SELECT root_id FROM scan_scopes WHERE epoch_id=? ORDER BY root_id`, epoch)
		if err != nil {
			return err
		}
		var found []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			found = append(found, id)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		expected := make([]string, len(roots))
		for i := range roots {
			expected[i] = roots[i].ID
		}
		sort.Strings(expected)
		if !slices.Equal(found, expected) {
			now := time.Now().UTC().Format(time.RFC3339Nano)
			if _, err := conn.ExecContext(ctx, `UPDATE scan_epochs
				SET status='interrupted',finished_at=?,error='scan scope changed before resume'
				WHERE id=? AND status='enumerating'`, now, epoch); err != nil {
				return err
			}
			if _, err := conn.ExecContext(ctx, `UPDATE scan_scopes SET status='interrupted' WHERE epoch_id=? AND status='enumerating'`, epoch); err != nil {
				return err
			}
			if _, err := conn.ExecContext(ctx, `UPDATE directory_frontier
				SET state='abandoned',fence='',lease_until=NULL,error='scan scope changed before resume'
				WHERE epoch_id=? AND state IN ('pending','leased')`, epoch); err != nil {
				return err
			}
			epoch = 0
			return nil
		}
		// Holding the coordinator lock proves the prior mutator is gone.
		if _, err := conn.ExecContext(ctx, `UPDATE directory_frontier SET state='pending',fence='',lease_until=NULL WHERE epoch_id=? AND state='leased'`, epoch); err != nil {
			return err
		}
		resumable = true
		return nil
	})
	if err != nil {
		return 0, false, 0, err
	}
	if resumable {
		rescanned, err := s.requeueChangedDirectories(ctx, epoch, roots)
		return epoch, true, rescanned, err
	}
	epoch, err = s.BeginEpoch(ctx, roots)
	return epoch, false, 0, err
}

type DirectoryRevision struct {
	MTimeNS int64
	Size    int64
}

type completedDirectory struct {
	rootID       string
	relativePath string
	revision     DirectoryRevision
}

// requeueChangedDirectories makes interrupted scans observe additions made to
// a directory that had already completed in the interrupted epoch. It uses
// directory stat data only as a change-detection hint: a normal scan after a
// completed epoch still walks every directory and validates each source file.
func (s *State) requeueChangedDirectories(ctx context.Context, epoch int64, roots []Root) (int64, error) {
	rootByID := make(map[string]Root, len(roots))
	for _, root := range roots {
		rootByID[root.ID] = root
	}
	rows, err := s.reader.QueryContext(ctx, `SELECT root_id,relative_path,completed_mtime_ns,completed_size
		FROM directory_frontier WHERE epoch_id=? AND state='completed' ORDER BY root_id,relative_path`, epoch)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	const batchSize = 128
	batch := make([]completedDirectory, 0, batchSize)
	var total int64
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		items := slices.Clone(batch)
		batch = batch[:0]
		return s.write(ctx, true, func(conn *sql.Conn) error {
			tx, err := conn.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback() }()
			changedByRoot := make(map[string]int64)
			for _, item := range items {
				res, err := tx.ExecContext(ctx, `UPDATE directory_frontier
					SET state='pending',fence='',lease_until=NULL,error=''
					WHERE epoch_id=? AND root_id=? AND relative_path=? AND state='completed'`, epoch, item.rootID, item.relativePath)
				if err != nil {
					return err
				}
				if count, _ := res.RowsAffected(); count == 1 {
					changedByRoot[item.rootID]++
					total++
				}
			}
			for rootID, count := range changedByRoot {
				if _, err := tx.ExecContext(ctx, `UPDATE scan_scopes
					SET outstanding_dirs=outstanding_dirs+?,status='enumerating'
					WHERE epoch_id=? AND root_id=?`, count, epoch, rootID); err != nil {
					return err
				}
			}
			return tx.Commit()
		})
	}
	for rows.Next() {
		var item completedDirectory
		if err := rows.Scan(&item.rootID, &item.relativePath, &item.revision.MTimeNS, &item.revision.Size); err != nil {
			return 0, err
		}
		root, ok := rootByID[item.rootID]
		if !ok {
			return 0, fmt.Errorf("library indexer: resumed directory references unknown root %s", item.rootID)
		}
		relative := item.relativePath
		if relative == "." {
			relative = ""
		}
		info, statErr := os.Stat(filepath.Join(root.Path, relative))
		changed := statErr != nil || !info.IsDir() || item.revision.MTimeNS < 0 || item.revision.Size < 0
		if !changed {
			changed = info.ModTime().UnixNano() != item.revision.MTimeNS || info.Size() != item.revision.Size
		}
		if !changed {
			continue
		}
		batch = append(batch, item)
		if len(batch) == batchSize {
			if err := flush(); err != nil {
				return 0, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := flush(); err != nil {
		return 0, err
	}
	return total, nil
}

type DirectoryTask struct {
	EpochID      int64
	RootID       string
	RelativePath string
	Attempt      int
	Fence        string
}

// AddDirectoryChildren durably publishes a bounded discovery batch while the
// parent remains leased. Replays are idempotent and increment outstanding work
// only for newly inserted children.
func (s *State) AddDirectoryChildren(ctx context.Context, task DirectoryTask, children []string) error {
	if len(children) == 0 {
		return nil
	}
	return s.write(ctx, true, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		var state, fence string
		if err := tx.QueryRowContext(ctx, `SELECT state,fence FROM directory_frontier WHERE epoch_id=? AND root_id=? AND relative_path=?`, task.EpochID, task.RootID, task.RelativePath).Scan(&state, &fence); err != nil {
			return err
		}
		if state != "leased" || fence != task.Fence {
			return errors.New("library indexer: stale directory fence")
		}
		added := 0
		for _, child := range children {
			child = filepath.Clean(child)
			if filepath.IsAbs(child) || child == ".." || strings.HasPrefix(child, ".."+string(filepath.Separator)) {
				return errors.New("library indexer: child directory escapes root")
			}
			res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO directory_frontier(epoch_id,root_id,relative_path,state) VALUES(?,?,?,'pending')`, task.EpochID, task.RootID, child)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			added += int(n)
		}
		if added > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE scan_scopes SET outstanding_dirs=outstanding_dirs+? WHERE epoch_id=? AND root_id=?`, added, task.EpochID, task.RootID); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
}

// ClaimDirectories leases a small durable frontier batch. Expired tasks are
// recoverable after a crash; a new fence prevents a late enumerator from
// completing or extending the superseded attempt.
func (s *State) ClaimDirectories(ctx context.Context, epoch int64, limit int, lease time.Duration) ([]DirectoryTask, error) {
	if epoch <= 0 || limit < 1 || limit > 256 || lease <= 0 {
		return nil, errors.New("library indexer: invalid directory claim bounds")
	}
	var tasks []DirectoryTask
	err := s.write(ctx, true, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		now := time.Now().UTC()
		rows, err := tx.QueryContext(ctx, `SELECT epoch_id,root_id,relative_path,attempt FROM directory_frontier
			WHERE epoch_id=? AND (state='pending' OR (state='leased' AND lease_until<?))
			ORDER BY root_id,relative_path LIMIT ?`, epoch, now.Format(time.RFC3339Nano), limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			var task DirectoryTask
			if err := rows.Scan(&task.EpochID, &task.RootID, &task.RelativePath, &task.Attempt); err != nil {
				rows.Close()
				return err
			}
			tasks = append(tasks, task)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for i := range tasks {
			tasks[i].Attempt++
			tasks[i].Fence, err = randomFence()
			if err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, `UPDATE directory_frontier SET state='leased',attempt=?,fence=?,lease_until=? WHERE epoch_id=? AND root_id=? AND relative_path=?`, tasks[i].Attempt, tasks[i].Fence, now.Add(lease).Format(time.RFC3339Nano), tasks[i].EpochID, tasks[i].RootID, tasks[i].RelativePath)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return errors.New("library indexer: directory changed during claim")
			}
		}
		return tx.Commit()
	})
	return tasks, err
}

func (s *State) RenewDirectories(ctx context.Context, tasks []DirectoryTask, lease time.Duration) error {
	if len(tasks) == 0 {
		return nil
	}
	if len(tasks) > 1024 || lease <= 0 {
		return errors.New("library indexer: invalid directory renewal bounds")
	}
	return s.write(ctx, true, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		now := time.Now().UTC()
		for _, task := range tasks {
			res, err := tx.ExecContext(ctx, `UPDATE directory_frontier SET lease_until=? WHERE epoch_id=? AND root_id=? AND relative_path=? AND state='leased' AND fence=?`, now.Add(lease).Format(time.RFC3339Nano), task.EpochID, task.RootID, task.RelativePath, task.Fence)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 1 {
				continue
			}
			var state, fence string
			if err := tx.QueryRowContext(ctx, `SELECT state,fence FROM directory_frontier WHERE epoch_id=? AND root_id=? AND relative_path=?`, task.EpochID, task.RootID, task.RelativePath).Scan(&state, &fence); err != nil {
				return err
			}
			if state == "leased" && fence != task.Fence {
				return errors.New("library indexer: lost directory lease")
			}
		}
		return tx.Commit()
	})
}

// CompleteDirectory atomically publishes discovered child directories before
// acknowledging the parent. A crash cannot lose work between those operations.
func (s *State) CompleteDirectory(ctx context.Context, task DirectoryTask, children []string, revision DirectoryRevision) error {
	return s.write(ctx, true, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		var state, fence string
		if err := tx.QueryRowContext(ctx, `SELECT state,fence FROM directory_frontier WHERE epoch_id=? AND root_id=? AND relative_path=?`, task.EpochID, task.RootID, task.RelativePath).Scan(&state, &fence); err != nil {
			return err
		}
		if state != "leased" || fence != task.Fence {
			return errors.New("library indexer: stale directory fence")
		}
		added := 0
		for _, child := range children {
			child = filepath.Clean(child)
			if filepath.IsAbs(child) || child == ".." || strings.HasPrefix(child, ".."+string(filepath.Separator)) {
				return errors.New("library indexer: child directory escapes root")
			}
			res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO directory_frontier(epoch_id,root_id,relative_path,state) VALUES(?,?,?,'pending')`, task.EpochID, task.RootID, child)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			added += int(n)
		}
		res, err := tx.ExecContext(ctx, `UPDATE directory_frontier
			SET state='completed',fence='',lease_until=NULL,completed_mtime_ns=?,completed_size=?
			WHERE epoch_id=? AND root_id=? AND relative_path=? AND fence=?`, revision.MTimeNS, revision.Size, task.EpochID, task.RootID, task.RelativePath, task.Fence)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return errors.New("library indexer: directory completion lost its fence")
		}
		_, err = tx.ExecContext(ctx, `UPDATE scan_scopes SET outstanding_dirs=outstanding_dirs-1+? WHERE epoch_id=? AND root_id=?`, added, task.EpochID, task.RootID)
		if err != nil {
			return err
		}
		return tx.Commit()
	})
}

// RetryDirectory returns a still-outstanding task to the durable frontier. It
// is used when the directory changed while it was being enumerated, so the
// scan does not commit a potentially incomplete view.
func (s *State) RetryDirectory(ctx context.Context, task DirectoryTask, detail string) error {
	if len(detail) > 4096 {
		detail = detail[:4096]
	}
	return s.write(ctx, true, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, `UPDATE directory_frontier
			SET state='pending',error=?,fence='',lease_until=NULL
			WHERE epoch_id=? AND root_id=? AND relative_path=? AND state='leased' AND fence=?`, detail, task.EpochID, task.RootID, task.RelativePath, task.Fence)
		if err != nil {
			return err
		}
		if count, _ := res.RowsAffected(); count != 1 {
			return errors.New("library indexer: stale directory retry")
		}
		return nil
	})
}

func (s *State) FailDirectory(ctx context.Context, task DirectoryTask, detail string) error {
	if len(detail) > 4096 {
		detail = detail[:4096]
	}
	return s.write(ctx, true, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		res, err := tx.ExecContext(ctx, `UPDATE directory_frontier SET state='failed',error=?,fence='',lease_until=NULL WHERE epoch_id=? AND root_id=? AND relative_path=? AND fence=?`, detail, task.EpochID, task.RootID, task.RelativePath, task.Fence)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return errors.New("library indexer: stale directory failure")
		}
		_, err = tx.ExecContext(ctx, `UPDATE scan_scopes SET outstanding_dirs=outstanding_dirs-1,permission_errors=permission_errors+1,status='partial' WHERE epoch_id=? AND root_id=?`, task.EpochID, task.RootID)
		if err != nil {
			return err
		}
		return tx.Commit()
	})
}

// FinishEpoch performs absence reconciliation only for completely enumerated
// roots. A permission error, missing mount or interrupted frontier preserves
// every previously known file for that scope.
func (s *State) FinishEpoch(ctx context.Context, epoch int64) (bool, error) {
	finished := false
	err := s.write(ctx, true, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		var outstanding int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(outstanding_dirs),0) FROM scan_scopes WHERE epoch_id=?`, epoch).Scan(&outstanding); err != nil {
			return err
		}
		if outstanding != 0 {
			return nil
		}
		rows, err := tx.QueryContext(ctx, `SELECT root_id,permission_errors FROM scan_scopes WHERE epoch_id=? ORDER BY root_id`, epoch)
		if err != nil {
			return err
		}
		type scope struct {
			root     string
			failures int
		}
		var scopes []scope
		for rows.Next() {
			var item scope
			if err := rows.Scan(&item.root, &item.failures); err != nil {
				rows.Close()
				return err
			}
			scopes = append(scopes, item)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		partial := false
		for _, item := range scopes {
			if item.failures != 0 {
				partial = true
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE files SET status='missing',tombstoned_at=? WHERE root_id=? AND last_seen_epoch<? AND tombstoned_at IS NULL`, time.Now().UTC().Format(time.RFC3339Nano), item.root, epoch); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE roots SET last_successful_epoch=?,offline=0 WHERE id=?`, epoch, item.root); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE scan_scopes SET status='completed' WHERE epoch_id=? AND root_id=?`, epoch, item.root); err != nil {
				return err
			}
		}
		status := "completed"
		if partial {
			status = "partial"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scan_epochs SET status=?,finished_at=? WHERE id=?`, status, time.Now().UTC().Format(time.RFC3339Nano), epoch); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		finished = true
		return nil
	})
	return finished, err
}

type SourceFile struct {
	ID             string
	RootID         string
	RelativePath   string
	Device         uint64
	Inode          uint64
	Size           int64
	MTimeNS        int64
	SourceRevision string
	Extension      string
}

type FileRecord struct {
	SourceFile
	RootPath string
	Status   string
}

func (s *State) File(ctx context.Context, id string) (FileRecord, error) {
	var record FileRecord
	record.ID = id
	err := s.reader.QueryRowContext(ctx, `SELECT f.root_id,r.path,f.relative_path,f.device,f.inode,f.size,f.mtime_ns,f.source_revision,f.extension,f.status
		FROM files f JOIN roots r ON r.id=f.root_id WHERE f.id=?`, id).Scan(&record.RootID, &record.RootPath, &record.RelativePath, &record.Device, &record.Inode, &record.Size, &record.MTimeNS, &record.SourceRevision, &record.Extension, &record.Status)
	return record, err
}

func (f FileRecord) AbsolutePath() (string, error) {
	rel := filepath.Clean(f.RelativePath)
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("library indexer: stored path escapes root")
	}
	return filepath.Join(f.RootPath, rel), nil
}

func sourceRevision(size, mtime int64, device, inode uint64) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d:%d:%d:%d", size, mtime, device, inode)
	return hex.EncodeToString(h.Sum(nil))
}

func stableFileID(rootID, relative string) string {
	h := sha256.Sum256([]byte("playlist-indexer-file/v1\x00" + rootID + "\x00" + filepath.ToSlash(relative)))
	return "local:" + hex.EncodeToString(h[:16])
}

// SemanticDigest compares serial and parallel analyzer results while excluding
// leases, timestamps, execution settings, physical database layout, and logs.
func (s *State) SemanticDigest(ctx context.Context) (string, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT f.id,f.source_revision,COALESCE(m.data,''),COALESCE(d.data,''),COALESCE(v.vector,'')
		FROM files f
		LEFT JOIN track_metadata m ON m.file_id=f.id AND m.source_revision=f.source_revision
		LEFT JOIN dsp_results d ON d.file_id=f.id AND d.source_revision=f.source_revision
		LEFT JOIN mert_results v ON v.file_id=f.id AND v.source_revision=f.source_revision
		WHERE f.status='present' ORDER BY f.id`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	h := sha256.New()
	for rows.Next() {
		var id, revision string
		var metadata, dsp, vector []byte
		if err := rows.Scan(&id, &revision, &metadata, &dsp, &vector); err != nil {
			return "", err
		}
		for _, value := range [][]byte{[]byte(id), []byte(revision), metadata, dsp, vector} {
			var size [8]byte
			binary.LittleEndian.PutUint64(size[:], uint64(len(value)))
			h.Write(size[:])
			h.Write(value)
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *State) ObserveFile(ctx context.Context, epoch int64, file SourceFile, semanticKeys map[string]string) (SourceFile, error) {
	file.RelativePath = filepath.Clean(file.RelativePath)
	if file.RelativePath == "." || filepath.IsAbs(file.RelativePath) || file.RelativePath == ".." || strings.HasPrefix(file.RelativePath, ".."+string(filepath.Separator)) {
		return file, errors.New("library indexer: source path escapes its root")
	}
	if file.SourceRevision == "" {
		file.SourceRevision = sourceRevision(file.Size, file.MTimeNS, file.Device, file.Inode)
	}
	if file.ID == "" {
		file.ID = stableFileID(file.RootID, file.RelativePath)
	}
	err := s.writeBatch(ctx, func(tx *sql.Tx) error {
		var err error
		// Preserve identity across same-filesystem moves within a configured root.
		var prior string
		// A zero pair means that this platform/filesystem could not provide a
		// native identity. Never let that sentinel collapse unrelated paths.
		if file.Device != 0 || file.Inode != 0 {
			_ = tx.QueryRowContext(ctx, `SELECT id FROM files WHERE root_id=? AND device=? AND inode=? AND tombstoned_at IS NULL LIMIT 1`, file.RootID, file.Device, file.Inode).Scan(&prior)
		}
		if prior != "" {
			file.ID = prior
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO files(id,root_id,relative_path,device,inode,size,mtime_ns,source_revision,extension,status,first_seen_epoch,last_seen_epoch,tombstoned_at)
			VALUES(?,?,?,?,?,?,?,?,?,'present',?,?,NULL)
			ON CONFLICT(id) DO UPDATE SET relative_path=excluded.relative_path,device=excluded.device,inode=excluded.inode,size=excluded.size,mtime_ns=excluded.mtime_ns,source_revision=excluded.source_revision,extension=excluded.extension,status='present',last_seen_epoch=excluded.last_seen_epoch,tombstoned_at=NULL`,
			file.ID, file.RootID, file.RelativePath, file.Device, file.Inode, file.Size, file.MTimeNS, file.SourceRevision, file.Extension, epoch, epoch)
		if err != nil {
			return err
		}
		for kind, key := range semanticKeys {
			if _, err = tx.ExecContext(ctx, `UPDATE jobs SET state='superseded',fence='',lease_until=NULL,updated_at=? WHERE kind=? AND file_id=? AND semantic_key<>? AND state<>'superseded'`, time.Now().UTC().Format(time.RFC3339Nano), kind, file.ID, key); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO jobs(kind,file_id,source_revision,semantic_key,state,updated_at) VALUES(?,?,?,?,'pending',?)
				ON CONFLICT(kind,file_id,semantic_key) DO UPDATE SET source_revision=excluded.source_revision,
				state=CASE WHEN jobs.source_revision=excluded.source_revision AND jobs.state='completed' THEN jobs.state ELSE 'pending' END,
				fence='',lease_until=NULL,error_code='',error_detail='',updated_at=excluded.updated_at`, kind, file.ID, file.SourceRevision, key, time.Now().UTC().Format(time.RFC3339Nano))
			if err != nil {
				return err
			}
		}
		return nil
	})
	return file, err
}

type Job struct {
	ID             int64
	Kind           string
	FileID         string
	SourceRevision string
	SemanticKey    string
	Attempt        int
	Fence          string
	RetryCount     int
}

func randomFence() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *State) ClaimJobs(ctx context.Context, kind string, limit int, lease time.Duration) ([]Job, error) {
	if limit < 1 || limit > 1024 || lease <= 0 {
		return nil, errors.New("library indexer: invalid job claim bounds")
	}
	var jobs []Job
	err := s.write(ctx, true, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		now := time.Now().UTC()
		rows, err := tx.QueryContext(ctx, `SELECT j.id,j.kind,j.file_id,j.source_revision,j.semantic_key,j.attempt,j.retry_count FROM jobs j
			WHERE j.kind=? AND (j.state='pending' OR (j.state='leased' AND j.lease_until<?))
			AND (j.kind='metadata' OR EXISTS(SELECT 1 FROM jobs prerequisite WHERE prerequisite.file_id=j.file_id AND prerequisite.kind='metadata' AND prerequisite.state='completed' AND prerequisite.source_revision=j.source_revision))
			ORDER BY j.id LIMIT ?`, kind, now.Format(time.RFC3339Nano), limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			var job Job
			if err := rows.Scan(&job.ID, &job.Kind, &job.FileID, &job.SourceRevision, &job.SemanticKey, &job.Attempt, &job.RetryCount); err != nil {
				rows.Close()
				return err
			}
			jobs = append(jobs, job)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for i := range jobs {
			jobs[i].Attempt++
			jobs[i].Fence, err = randomFence()
			if err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, `UPDATE jobs SET state='leased',attempt=?,fence=?,lease_until=?,updated_at=? WHERE id=? AND source_revision=?`, jobs[i].Attempt, jobs[i].Fence, now.Add(lease).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), jobs[i].ID, jobs[i].SourceRevision)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return errors.New("library indexer: job changed during claim")
			}
		}
		return tx.Commit()
	})
	return jobs, err
}

// RenewJobs extends leases only for the exact attempts still owned by this
// coordinator. A single pool heartbeat calls this in bounded batches; workers
// never create per-track timers.
func (s *State) RenewJobs(ctx context.Context, jobs []Job, lease time.Duration) error {
	if len(jobs) == 0 {
		return nil
	}
	if len(jobs) > 1024 || lease <= 0 {
		return errors.New("library indexer: invalid job renewal bounds")
	}
	return s.write(ctx, true, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		now := time.Now().UTC()
		for _, job := range jobs {
			res, err := tx.ExecContext(ctx, `UPDATE jobs SET lease_until=?,updated_at=? WHERE id=? AND state='leased' AND fence=? AND source_revision=?`, now.Add(lease).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), job.ID, job.Fence, job.SourceRevision)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n != 1 {
				var state, fence, source string
				if err := tx.QueryRowContext(ctx, `SELECT state,fence,source_revision FROM jobs WHERE id=?`, job.ID).Scan(&state, &fence, &source); err != nil {
					return err
				}
				// The writer may have completed, failed, or refreshed this job
				// after the heartbeat snapshot was captured.
				if state != "leased" {
					continue
				}
				if fence != job.Fence || source != job.SourceRevision {
					return fmt.Errorf("library indexer: lost lease for job %d", job.ID)
				}
			}
		}
		return tx.Commit()
	})
}

// RefreshFileRevision atomically moves every semantic job for a changed file
// onto its newly observed stat revision. Results for the old revision remain
// as history but no longer join the current inventory row.
func (s *State) RefreshFileRevision(ctx context.Context, fileID, expected string, size, mtimeNS int64, device, inode uint64) (bool, error) {
	newRevision := sourceRevision(size, mtimeNS, device, inode)
	if newRevision == expected {
		return false, nil
	}
	refreshed := false
	err := s.write(ctx, true, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT source_revision FROM files WHERE id=?`, fileID).Scan(&current); err != nil {
			return err
		}
		if current != expected {
			return nil
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `UPDATE files SET device=?,inode=?,size=?,mtime_ns=?,source_revision=? WHERE id=? AND source_revision=?`, device, inode, size, mtimeNS, newRevision, fileID, expected); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET source_revision=?,state='pending',fence='',lease_until=NULL,error_code='',error_detail='',updated_at=? WHERE file_id=? AND source_revision=?`, newRevision, now, fileID, expected); err != nil {
			return err
		}
		refreshed = true
		return tx.Commit()
	})
	return refreshed, err
}

type JobResult struct {
	Job       Job
	Contract  string
	Metadata  []byte
	DSP       []byte
	Vector    []byte
	MERTData  []byte
	Dimension int
}

// CommitPartialDSP preserves a valid independent DSP branch when MERT fails.
// It deliberately leaves the owning audio job leased so FailJob can record the
// missing MERT capability under the same fence.
func (s *State) CommitPartialDSP(ctx context.Context, job Job, contract string, data []byte) error {
	return s.writeBatch(ctx, func(tx *sql.Tx) error {
		var state, fence, source, current string
		if err := tx.QueryRowContext(ctx, `SELECT state,fence,source_revision FROM jobs WHERE id=?`, job.ID).Scan(&state, &fence, &source); err != nil {
			return err
		}
		if state != "leased" || fence != job.Fence || source != job.SourceRevision {
			return errors.New("library indexer: stale partial DSP fence")
		}
		if err := tx.QueryRowContext(ctx, `SELECT source_revision FROM files WHERE id=?`, job.FileID).Scan(&current); err != nil || current != source {
			if err != nil {
				return err
			}
			return errors.New("library indexer: source changed before partial DSP commit")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO dsp_results(file_id,source_revision,contract,data) VALUES(?,?,?,?) ON CONFLICT(file_id,contract) DO UPDATE SET source_revision=excluded.source_revision,data=excluded.data`, job.FileID, source, contract, data); err != nil {
			return err
		}
		return nil
	})
}

func (s *State) CommitJob(ctx context.Context, result JobResult) error {
	return s.writeBatch(ctx, func(tx *sql.Tx) error {
		var err error
		var source, fence, state string
		if err := tx.QueryRowContext(ctx, `SELECT source_revision,fence,state FROM jobs WHERE id=?`, result.Job.ID).Scan(&source, &fence, &state); err != nil {
			return err
		}
		if state != "leased" || fence != result.Job.Fence || source != result.Job.SourceRevision {
			return errors.New("library indexer: stale job fence or source revision")
		}
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT source_revision FROM files WHERE id=?`, result.Job.FileID).Scan(&current); err != nil || current != source {
			if err != nil {
				return err
			}
			return errors.New("library indexer: source changed before commit")
		}
		switch result.Job.Kind {
		case "metadata":
			_, err = tx.ExecContext(ctx, `INSERT INTO track_metadata(file_id,source_revision,contract,data) VALUES(?,?,?,?) ON CONFLICT(file_id,contract) DO UPDATE SET source_revision=excluded.source_revision,data=excluded.data`, result.Job.FileID, source, result.Contract, result.Metadata)
		case "dsp":
			_, err = tx.ExecContext(ctx, `INSERT INTO dsp_results(file_id,source_revision,contract,data) VALUES(?,?,?,?) ON CONFLICT(file_id,contract) DO UPDATE SET source_revision=excluded.source_revision,data=excluded.data`, result.Job.FileID, source, result.Contract, result.DSP)
		case "mert":
			_, err = tx.ExecContext(ctx, `INSERT INTO mert_results(file_id,source_revision,contract,dimension,vector,data) VALUES(?,?,?,?,?,?) ON CONFLICT(file_id,contract) DO UPDATE SET source_revision=excluded.source_revision,dimension=excluded.dimension,vector=excluded.vector,data=excluded.data`, result.Job.FileID, source, result.Contract, result.Dimension, result.Vector, result.MERTData)
		case "audio":
			if len(result.DSP) != 0 {
				_, err = tx.ExecContext(ctx, `INSERT INTO dsp_results(file_id,source_revision,contract,data) VALUES(?,?,?,?) ON CONFLICT(file_id,contract) DO UPDATE SET source_revision=excluded.source_revision,data=excluded.data`, result.Job.FileID, source, result.Contract, result.DSP)
			}
			if err == nil && len(result.Vector) != 0 {
				_, err = tx.ExecContext(ctx, `INSERT INTO mert_results(file_id,source_revision,contract,dimension,vector,data) VALUES(?,?,?,?,?,?) ON CONFLICT(file_id,contract) DO UPDATE SET source_revision=excluded.source_revision,dimension=excluded.dimension,vector=excluded.vector,data=excluded.data`, result.Job.FileID, source, result.Contract, result.Dimension, result.Vector, result.MERTData)
			}
		default:
			return fmt.Errorf("library indexer: unknown result kind %q", result.Job.Kind)
		}
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE jobs SET state='completed',lease_until=NULL,updated_at=? WHERE id=? AND fence=? AND source_revision=?`, time.Now().UTC().Format(time.RFC3339Nano), result.Job.ID, result.Job.Fence, source)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return errors.New("library indexer: commit lost its fence")
		}
		return nil
	})
}

func (s *State) FailJob(ctx context.Context, job Job, code, detail string, retry bool) error {
	if len(detail) > 4096 {
		detail = detail[:4096]
	}
	return s.writeBatch(ctx, func(tx *sql.Tx) error {
		next := "failed"
		retryIncrement := 0
		if retry {
			next = "pending"
			retryIncrement = 1
		}
		res, err := tx.ExecContext(ctx, `UPDATE jobs SET state=?,retry_count=retry_count+?,error_code=?,error_detail=?,fence='',lease_until=NULL,updated_at=? WHERE id=? AND fence=? AND source_revision=?`, next, retryIncrement, code, detail, time.Now().UTC().Format(time.RFC3339Nano), job.ID, job.Fence, job.SourceRevision)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return errors.New("library indexer: stale failure result")
		}
		return nil
	})
}

func (s *State) RetryFailed(ctx context.Context) (int64, error) {
	var changed int64
	err := s.write(ctx, true, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, `UPDATE jobs SET state='pending',fence='',lease_until=NULL,error_code='',error_detail='',updated_at=?
			WHERE state='failed' AND error_code NOT IN ('unsupported','corrupt_media','metadata_unavailable') AND retry_count < 5`, time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		changed, err = res.RowsAffected()
		return err
	})
	return changed, err
}

type Status struct {
	Files         int64                       `json:"files"`
	Present       int64                       `json:"present"`
	Tombstoned    int64                       `json:"tombstoned"`
	Metadata      int64                       `json:"metadata"`
	DSP           int64                       `json:"dsp"`
	MERT          int64                       `json:"mert"`
	JobsByState   map[string]int64            `json:"jobsByState"`
	JobsByStage   map[string]map[string]int64 `json:"jobsByStage"`
	ExpiredLeases int64                       `json:"expiredLeases"`
	RetryCount    int64                       `json:"retryCount"`
}

// ProgressSnapshot is a bounded view of the current semantic jobs used by
// interactive clients. Counts exclude superseded contracts so an unchanged
// rerun starts from its compatible durable results rather than historical work.
type ProgressSnapshot struct {
	Files    int64
	Total    int64
	Finished int64
	Queued   int64
	Leased   int64
	Failed   int64
	Retries  int64
}

func (s *State) Progress(ctx context.Context, semanticJobs map[string]string) (ProgressSnapshot, error) {
	var snapshot ProgressSnapshot
	if err := s.reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE status='present'`).Scan(&snapshot.Files); err != nil {
		return snapshot, err
	}
	kinds := make([]string, 0, len(semanticJobs))
	for kind := range semanticJobs {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		var total, finished, queued, leased, failed, retries int64
		if err := s.reader.QueryRowContext(ctx, `SELECT COUNT(*),
			COALESCE(SUM(state IN ('completed','failed')),0),
			COALESCE(SUM(state='pending'),0),COALESCE(SUM(state='leased'),0),
			COALESCE(SUM(state='failed'),0),COALESCE(SUM(retry_count),0)
			FROM jobs WHERE kind=? AND semantic_key=? AND state<>'superseded'`, kind, semanticJobs[kind]).Scan(&total, &finished, &queued, &leased, &failed, &retries); err != nil {
			return snapshot, err
		}
		snapshot.Total += total
		snapshot.Finished += finished
		snapshot.Queued += queued
		snapshot.Leased += leased
		snapshot.Failed += failed
		snapshot.Retries += retries
	}
	return snapshot, nil
}

func (s *State) Status(ctx context.Context) (Status, error) {
	return queryStatus(ctx, s.reader)
}

func ReadStatus(ctx context.Context, dir string) (Status, error) {
	path, err := filepath.Abs(filepath.Join(dir, "library-index.sqlite"))
	if err != nil {
		return Status{}, err
	}
	dsn, err := sqliteuri.ReadOnly(path, false)
	if err != nil {
		return Status{}, err
	}
	db, err := sql.Open("sqlite", dsn+"&_pragma="+url.QueryEscape("busy_timeout(5000)"))
	if err != nil {
		return Status{}, err
	}
	defer db.Close()
	return queryStatus(ctx, db)
}

func queryStatus(ctx context.Context, db *sql.DB) (Status, error) {
	status := Status{JobsByState: map[string]int64{}, JobsByStage: map[string]map[string]int64{}}
	err := db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(status='present'),0),COALESCE(SUM(tombstoned_at IS NOT NULL),0),
		(SELECT COUNT(*) FROM track_metadata),(SELECT COUNT(*) FROM dsp_results),(SELECT COUNT(*) FROM mert_results) FROM files`).Scan(&status.Files, &status.Present, &status.Tombstoned, &status.Metadata, &status.DSP, &status.MERT)
	if err != nil {
		return status, err
	}
	rows, err := db.QueryContext(ctx, `SELECT kind,state,COUNT(*),COALESCE(SUM(retry_count),0),COALESCE(SUM(state='leased' AND lease_until < ?),0) FROM jobs GROUP BY kind,state`, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return status, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, state string
		var count, retries, expired int64
		if err := rows.Scan(&kind, &state, &count, &retries, &expired); err != nil {
			return status, err
		}
		status.JobsByState[state] += count
		if status.JobsByStage[kind] == nil {
			status.JobsByStage[kind] = map[string]int64{}
		}
		status.JobsByStage[kind][state] = count
		status.RetryCount += retries
		status.ExpiredLeases += expired
	}
	return status, rows.Err()
}

func (s *State) JobCounts(ctx context.Context, kind string) (pending, leased int64, err error) {
	err = s.reader.QueryRowContext(ctx, `SELECT COALESCE(SUM(state='pending'),0),COALESCE(SUM(state='leased'),0) FROM jobs WHERE kind=?`, kind).Scan(&pending, &leased)
	return
}

func (s *State) Metadata(ctx context.Context, fileID string) ([]byte, string, error) {
	var data []byte
	var revision string
	err := s.reader.QueryRowContext(ctx, `SELECT data,source_revision FROM track_metadata WHERE file_id=? ORDER BY rowid DESC LIMIT 1`, fileID).Scan(&data, &revision)
	return data, revision, err
}

func (s *State) FinalizeBlockedAudio(ctx context.Context) error {
	return s.write(ctx, true, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `UPDATE jobs AS audio SET state='failed',error_code='metadata_unavailable',
			error_detail='audio analysis prerequisite metadata did not complete',updated_at=?
			WHERE audio.kind='audio' AND audio.state='pending' AND NOT EXISTS(
				SELECT 1 FROM jobs metadata WHERE metadata.file_id=audio.file_id AND metadata.kind='metadata'
				AND metadata.state='completed' AND metadata.source_revision=audio.source_revision)`, time.Now().UTC().Format(time.RFC3339Nano))
		return err
	})
}
