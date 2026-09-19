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

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/installlock"
	"github.com/platten/playlistai/internal/sqliteuri"
)

const stateSchemaVersion = 5

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
	pageCacheBytes int64
	dir            string
	path           string
	writerDB       *sql.DB
	writer         *sql.Conn
	reader         *sql.DB
	control        chan writerRequest
	results        chan writerRequest
	batches        chan writerRequest
	stop           chan struct{}
	done           chan struct{}
	releaseLock    func() error
	closeOnce      sync.Once
	closeErr       error
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
	if err := rebuildEligibleWorkset(ctx, conn); err != nil {
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
	// Bound reader cache residency independently of library size and spill sort
	// temporaries to disk. Eight readers plus the writer use at most 96 MiB of
	// page caches, within the planner's native-process safety headroom.
	reader, err := sql.Open("sqlite", roDSN+"&_pragma="+url.QueryEscape("busy_timeout(5000)")+"&_pragma="+url.QueryEscape("temp_store(1)")+"&_pragma="+url.QueryEscape("cache_size(-8192)"))
	if err != nil {
		_ = conn.Close()
		_ = db.Close()
		return nil, err
	}
	readConnections = min(8, max(1, readConnections))
	reader.SetMaxOpenConns(readConnections)
	reader.SetMaxIdleConns(readConnections)
	s := &State{pageCacheBytes: int64(32+8*readConnections) << 20, dir: abs, path: path, writerDB: db, writer: conn, reader: reader,
		control: make(chan writerRequest, 32), results: make(chan writerRequest, 64), batches: make(chan writerRequest, 256), stop: make(chan struct{}), done: make(chan struct{}), releaseLock: release}
	go s.writerLoop()
	locked = false
	return s, nil
}

func migrateState(ctx context.Context, conn *sql.Conn) error {
	var version string
	// Bound page-cache memory; large temporary sorts spill to local disk. Scan
	// transactions are chunked, so they no longer need a library-sized cache.
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA synchronous=FULL; PRAGMA wal_autocheckpoint=1000;
PRAGMA cache_size=-32768; PRAGMA temp_store=FILE;
CREATE TABLE IF NOT EXISTS state_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);`); err != nil {
		return err
	}
	_ = conn.QueryRowContext(ctx, `SELECT value FROM state_meta WHERE key='schema_version'`).Scan(&version)
	if version != "" && version != "1" && version != "2" && version != "3" && version != "4" && version != strconv.Itoa(stateSchemaVersion) {
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
 status TEXT NOT NULL, error TEXT NOT NULL DEFAULT '', follow_directory_symlinks INTEGER NOT NULL DEFAULT 0
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
CREATE INDEX IF NOT EXISTS directory_frontier_claim ON directory_frontier(epoch_id,state,root_id,relative_path);
CREATE TABLE IF NOT EXISTS files (
 id TEXT PRIMARY KEY, root_id TEXT NOT NULL REFERENCES roots(id), relative_path TEXT NOT NULL,
 device INTEGER NOT NULL, inode INTEGER NOT NULL, size INTEGER NOT NULL, mtime_ns INTEGER NOT NULL,
 source_revision TEXT NOT NULL, extension TEXT NOT NULL, status TEXT NOT NULL,
 first_seen_epoch INTEGER NOT NULL, last_seen_epoch INTEGER NOT NULL, tombstoned_at TEXT,
 UNIQUE(root_id,relative_path)
);
CREATE INDEX IF NOT EXISTS files_identity ON files(root_id,device,inode);
CREATE INDEX IF NOT EXISTS files_scan_epoch ON files(last_seen_epoch,status,id);
CREATE TABLE IF NOT EXISTS jobs (
 id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, file_id TEXT NOT NULL REFERENCES files(id),
 source_revision TEXT NOT NULL, semantic_key TEXT NOT NULL, state TEXT NOT NULL,
 attempt INTEGER NOT NULL DEFAULT 0, fence TEXT NOT NULL DEFAULT '', lease_until TEXT,
 retry_count INTEGER NOT NULL DEFAULT 0, error_code TEXT NOT NULL DEFAULT '', error_detail TEXT NOT NULL DEFAULT '',
 updated_at TEXT NOT NULL, UNIQUE(kind,file_id,semantic_key)
);
CREATE INDEX IF NOT EXISTS jobs_claim ON jobs(kind,state,updated_at,id);
CREATE INDEX IF NOT EXISTS jobs_claim_order ON jobs(kind,state);
CREATE INDEX IF NOT EXISTS jobs_file_active ON jobs(file_id,kind,semantic_key,state);
CREATE TABLE IF NOT EXISTS scan_diff_jobs (
 epoch_id INTEGER NOT NULL REFERENCES scan_epochs(id), job_id INTEGER NOT NULL REFERENCES jobs(id),
 source_revision TEXT NOT NULL, kind TEXT NOT NULL, semantic_key TEXT NOT NULL,
 PRIMARY KEY(epoch_id,job_id)
);
DROP INDEX IF EXISTS scan_diff_claim;
CREATE TABLE IF NOT EXISTS track_metadata (
 file_id TEXT NOT NULL, source_revision TEXT NOT NULL, contract TEXT NOT NULL,
 data BLOB NOT NULL, recording_key TEXT NOT NULL DEFAULT '', PRIMARY KEY(file_id,contract)
);
CREATE TABLE IF NOT EXISTS dsp_results (
 file_id TEXT NOT NULL, source_revision TEXT NOT NULL, contract TEXT NOT NULL,
 data BLOB NOT NULL, PRIMARY KEY(file_id,contract)
);
CREATE TABLE IF NOT EXISTS dsp_stage_cache (
 file_id TEXT NOT NULL, source_revision TEXT NOT NULL, contract TEXT NOT NULL,
 data BLOB NOT NULL, PRIMARY KEY(file_id,contract)
);
CREATE TABLE IF NOT EXISTS mert_results (
 file_id TEXT NOT NULL, source_revision TEXT NOT NULL, contract TEXT NOT NULL,
 dimension INTEGER NOT NULL, vector BLOB NOT NULL, data BLOB NOT NULL,
 PRIMARY KEY(file_id,contract)
);
CREATE INDEX IF NOT EXISTS mert_results_contract ON mert_results(contract,file_id);
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
		table      string
		name       string
		definition string
	}{
		{table: "directory_frontier", name: "completed_mtime_ns", definition: "INTEGER NOT NULL DEFAULT -1"},
		{table: "directory_frontier", name: "completed_size", definition: "INTEGER NOT NULL DEFAULT -1"},
		{table: "scan_epochs", name: "follow_directory_symlinks", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "track_metadata", name: "recording_key", definition: "TEXT NOT NULL DEFAULT ''"},
	} {
		exists, err := sqliteColumnExists(ctx, conn, column.table, column.name)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := conn.ExecContext(ctx, `ALTER TABLE `+column.table+` ADD COLUMN `+column.name+` `+column.definition); err != nil {
				return err
			}
		}
	}
	if _, err := conn.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS track_metadata_recording ON track_metadata(recording_key,file_id) WHERE recording_key<>''`); err != nil {
		return err
	}
	if version != strconv.Itoa(stateSchemaVersion) {
		if err := backfillRecordingKeys(ctx, conn); err != nil {
			return err
		}
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO state_meta(key,value) VALUES('schema_version',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.Itoa(stateSchemaVersion))
	return err
}

func backfillRecordingKeys(ctx context.Context, conn *sql.Conn) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	statement, err := tx.PrepareContext(ctx, `UPDATE track_metadata SET recording_key=? WHERE rowid=?`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	type update struct {
		rowID int64
		key   string
	}
	var cursor int64
	for {
		rows, queryErr := tx.QueryContext(ctx, `SELECT rowid,data FROM track_metadata WHERE recording_key='' AND rowid>? ORDER BY rowid LIMIT 1024`, cursor)
		if queryErr != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			return queryErr
		}
		updates := make([]update, 0, 1024)
		scanned := 0
		for rows.Next() {
			var rowID int64
			var raw []byte
			if err := rows.Scan(&rowID, &raw); err != nil {
				_ = rows.Close()
				_ = statement.Close()
				_ = tx.Rollback()
				return err
			}
			scanned++
			cursor = rowID
			var record MetadataRecord
			if json.Unmarshal(raw, &record) == nil {
				if key := metadataRecordingIdentity(record); key != "" {
					updates = append(updates, update{rowID: rowID, key: key})
				}
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			return err
		}
		for _, item := range updates {
			if _, err := statement.ExecContext(ctx, item.key, item.rowID); err != nil {
				_ = statement.Close()
				_ = tx.Rollback()
				return err
			}
		}
		if scanned < 1024 {
			break
		}
	}
	return errors.Join(statement.Close(), tx.Commit())
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
	return s.beginEpoch(ctx, roots, false)
}

func (s *State) beginEpoch(ctx context.Context, roots []Root, followDirectorySymlinks bool) (int64, error) {
	var epoch int64
	err := s.write(ctx, true, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		res, err := tx.ExecContext(ctx, `INSERT INTO scan_epochs(started_at,status,follow_directory_symlinks) VALUES(?,'enumerating',?)`, time.Now().UTC().Format(time.RFC3339Nano), followDirectorySymlinks)
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

func (s *State) BeginOrResumeEpoch(ctx context.Context, roots []Root, followDirectorySymlinks bool) (int64, bool, int64, error) {
	var epoch int64
	var storedFollowDirectorySymlinks bool
	var resumable bool
	err := s.write(ctx, true, func(conn *sql.Conn) error {
		if err := conn.QueryRowContext(ctx, `SELECT id,follow_directory_symlinks FROM scan_epochs WHERE status='enumerating' ORDER BY id DESC LIMIT 1`).Scan(&epoch, &storedFollowDirectorySymlinks); err != nil && !errors.Is(err, sql.ErrNoRows) {
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
		if !slices.Equal(found, expected) || storedFollowDirectorySymlinks != followDirectorySymlinks {
			detail := "scan scope changed before resume"
			if slices.Equal(found, expected) {
				detail = "directory symlink policy changed before resume"
			}
			now := time.Now().UTC().Format(time.RFC3339Nano)
			if _, err := conn.ExecContext(ctx, `UPDATE scan_epochs
				SET status='interrupted',finished_at=?,error=?
				WHERE id=? AND status='enumerating'`, now, detail, epoch); err != nil {
				return err
			}
			if _, err := conn.ExecContext(ctx, `UPDATE scan_scopes SET status='interrupted' WHERE epoch_id=? AND status='enumerating'`, epoch); err != nil {
				return err
			}
			if _, err := conn.ExecContext(ctx, `UPDATE directory_frontier
				SET state='abandoned',fence='',lease_until=NULL,error=?
				WHERE epoch_id=? AND state IN ('pending','leased')`, detail, epoch); err != nil {
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
	epoch, err = s.beginEpoch(ctx, roots, followDirectorySymlinks)
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

	var afterRoot, afterPath string
	for {
		rows, err := s.reader.QueryContext(ctx, `SELECT root_id,relative_path,completed_mtime_ns,completed_size
   FROM directory_frontier WHERE epoch_id=? AND state='completed' AND (root_id,relative_path)>(?,?)
   ORDER BY root_id,relative_path LIMIT ?`, epoch, afterRoot, afterPath, batchSize)
		if err != nil {
			return 0, err
		}
		page := make([]completedDirectory, 0, batchSize)
		for rows.Next() {
			var item completedDirectory
			if err := rows.Scan(&item.rootID, &item.relativePath, &item.revision.MTimeNS, &item.revision.Size); err != nil {
				rows.Close()
				return 0, err
			}
			page = append(page, item)
		}
		if err := closeRows(rows); err != nil {
			return 0, err
		}
		if len(page) == 0 {
			break
		}
		afterRoot = page[len(page)-1].rootID
		afterPath = page[len(page)-1].relativePath
		// Close each read snapshot before statting and writing, so resume cannot
		// pin a growing WAL for the duration of a large library walk.
		for _, item := range page {
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
		if err := flush(); err != nil {
			return 0, err
		}
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
		// Separate state ranges keep completed directories out of every batch
		// lookup. Merge their bounded ordered prefixes to preserve frontier order.
		for _, selection := range []struct{ state, expiry string }{
			{state: "pending"}, {state: "leased", expiry: " AND lease_until<?"},
		} {
			args := []any{epoch, selection.state}
			if selection.expiry != "" {
				args = append(args, now.Format(time.RFC3339Nano))
			}
			args = append(args, limit)
			rows, err := tx.QueryContext(ctx, `SELECT epoch_id,root_id,relative_path,attempt
				FROM directory_frontier INDEXED BY directory_frontier_claim
				WHERE epoch_id=? AND state=?`+selection.expiry+`
				ORDER BY root_id,relative_path LIMIT ?`, args...)
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
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				return err
			}
		}
		sort.Slice(tasks, func(i, j int) bool {
			if tasks[i].RootID != tasks[j].RootID {
				return tasks[i].RootID < tasks[j].RootID
			}
			return tasks[i].RelativePath < tasks[j].RelativePath
		})
		if len(tasks) > limit {
			tasks = tasks[:limit]
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
	ID              string
	RootID          string
	RelativePath    string
	Device          uint64
	Inode           uint64
	Size            int64
	MTimeNS         int64
	SourceRevision  string
	Extension       string
	needsProcessing bool
}

type FileRecord struct {
	SourceFile
	RootPath  string
	RootAlias string
	Status    string
}

func (s *State) File(ctx context.Context, id string) (FileRecord, error) {
	var record FileRecord
	record.ID = id
	err := s.reader.QueryRowContext(ctx, `SELECT f.root_id,r.path,r.alias,f.relative_path,f.device,f.inode,f.size,f.mtime_ns,f.source_revision,f.extension,f.status
		FROM files f JOIN roots r ON r.id=f.root_id WHERE f.id=?`, id).Scan(&record.RootID, &record.RootPath, &record.RootAlias, &record.RelativePath, &record.Device, &record.Inode, &record.Size, &record.MTimeNS, &record.SourceRevision, &record.Extension, &record.Status)
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
	observed, err := s.ObserveFiles(ctx, epoch, []SourceFile{file}, semanticKeys)
	if len(observed) == 1 {
		return observed[0], err
	}
	return file, err
}

// ObserveFiles records one bounded filesystem discovery chunk in a single
// transaction. Callers retain per-file results, while directories with many
// entries avoid one durable round trip and commit per source.
func (s *State) ObserveFiles(ctx context.Context, epoch int64, files []SourceFile, semanticKeys map[string]string) ([]SourceFile, error) {
	if len(files) == 0 {
		return []SourceFile{}, nil
	}
	if len(files) > 1024 {
		return nil, errors.New("library indexer: file observation batch exceeds limit")
	}
	observed := slices.Clone(files)
	for index := range observed {
		if err := normalizeSourceFile(&observed[index]); err != nil {
			return observed, err
		}
	}
	updatedAt := time.Now().UTC().Format(time.RFC3339Nano)
	err := s.writeBatch(ctx, func(tx *sql.Tx) error {
		statements, err := prepareObservation(ctx, tx)
		if err != nil {
			return err
		}
		defer statements.close()
		for index := range observed {
			if err := statements.observe(ctx, epoch, &observed[index], semanticKeys, updatedAt); err != nil {
				return err
			}
		}
		return nil
	})
	return observed, err
}

// normalizeSourceFile validates the root-relative path and derives the stat
// revision and default path identity.
func normalizeSourceFile(file *SourceFile) error {
	file.RelativePath = filepath.Clean(file.RelativePath)
	if file.RelativePath == "." || filepath.IsAbs(file.RelativePath) || file.RelativePath == ".." || strings.HasPrefix(file.RelativePath, ".."+string(filepath.Separator)) {
		return errors.New("library indexer: source path escapes its root")
	}
	if file.SourceRevision == "" {
		file.SourceRevision = sourceRevision(file.Size, file.MTimeNS, file.Device, file.Inode)
	}
	if file.ID == "" {
		file.ID = stableFileID(file.RootID, file.RelativePath)
	}
	file.needsProcessing = false
	return nil
}

type observationStatements struct {
	identity  *sql.Stmt
	file      *sql.Stmt
	supersede *sql.Stmt
	job       *sql.Stmt
}

type preparer interface {
	PrepareContext(context.Context, string) (*sql.Stmt, error)
}

func prepareObservation(ctx context.Context, tx preparer) (*observationStatements, error) {
	statements := &observationStatements{}
	var err error
	if statements.identity, err = tx.PrepareContext(ctx, `SELECT id FROM files WHERE root_id=? AND device=? AND inode=? AND tombstoned_at IS NULL LIMIT 1`); err != nil {
		return nil, err
	}
	if statements.file, err = tx.PrepareContext(ctx, `INSERT INTO files(id,root_id,relative_path,device,inode,size,mtime_ns,source_revision,extension,status,first_seen_epoch,last_seen_epoch,tombstoned_at)
		VALUES(?,?,?,?,?,?,?,?,?,'present',?,?,NULL)
		ON CONFLICT(id) DO UPDATE SET relative_path=excluded.relative_path,device=excluded.device,inode=excluded.inode,size=excluded.size,mtime_ns=excluded.mtime_ns,source_revision=excluded.source_revision,extension=excluded.extension,status='present',last_seen_epoch=excluded.last_seen_epoch,tombstoned_at=NULL`); err != nil {
		statements.close()
		return nil, err
	}
	if statements.supersede, err = tx.PrepareContext(ctx, `UPDATE jobs SET state='superseded',fence='',lease_until=NULL,updated_at=? WHERE kind=? AND file_id=? AND semantic_key<>? AND state<>'superseded'`); err != nil {
		statements.close()
		return nil, err
	}
	if statements.job, err = tx.PrepareContext(ctx, `INSERT INTO jobs(kind,file_id,source_revision,semantic_key,state,updated_at) VALUES(?,?,?,?,'pending',?)
		ON CONFLICT(kind,file_id,semantic_key) DO UPDATE SET source_revision=excluded.source_revision,
		state=CASE WHEN jobs.source_revision=excluded.source_revision AND jobs.state IN ('completed','failed') THEN jobs.state ELSE 'pending' END,
		fence='',lease_until=NULL,
		error_code=CASE WHEN jobs.source_revision=excluded.source_revision AND jobs.state='failed' THEN jobs.error_code ELSE '' END,
		error_detail=CASE WHEN jobs.source_revision=excluded.source_revision AND jobs.state='failed' THEN jobs.error_detail ELSE '' END,
		updated_at=excluded.updated_at RETURNING state`); err != nil {
		statements.close()
		return nil, err
	}
	return statements, nil
}

func (o *observationStatements) close() {
	for _, statement := range []*sql.Stmt{o.identity, o.file, o.supersede, o.job} {
		if statement != nil {
			_ = statement.Close()
		}
	}
}

// observe records one normalized file and its semantic jobs, resolving the
// durable identity first so same-filesystem moves keep their file ID. A caller
// that has proven the file matches no durable row passes known=false to skip
// the identity lookup and supersede statements, which would be no-ops.
func (o *observationStatements) observe(ctx context.Context, epoch int64, file *SourceFile, semanticKeys map[string]string, updatedAt string) error {
	return o.observeKnown(ctx, epoch, file, semanticKeys, updatedAt, true)
}

func (o *observationStatements) observeKnown(ctx context.Context, epoch int64, file *SourceFile, semanticKeys map[string]string, updatedAt string, known bool) error {
	var prior string
	// A zero pair means that this platform/filesystem could not provide a
	// native identity. Never let that sentinel collapse unrelated paths.
	if known && (file.Device != 0 || file.Inode != 0) {
		if err := o.identity.QueryRowContext(ctx, file.RootID, file.Device, file.Inode).Scan(&prior); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if prior != "" {
		file.ID = prior
	}
	if _, err := o.file.ExecContext(ctx, file.ID, file.RootID, file.RelativePath, file.Device, file.Inode, file.Size, file.MTimeNS, file.SourceRevision, file.Extension, epoch, epoch); err != nil {
		return err
	}
	for kind, key := range semanticKeys {
		if known {
			if _, err := o.supersede.ExecContext(ctx, updatedAt, kind, file.ID, key); err != nil {
				return err
			}
		}
		var jobState string
		if err := o.job.QueryRowContext(ctx, kind, file.ID, file.SourceRevision, key, updatedAt).Scan(&jobState); err != nil {
			return err
		}
		if jobState == "pending" {
			file.needsProcessing = true
		}
	}
	return nil
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
	claimEpoch     int64
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
	return s.claimJobs(ctx, kind, 0, limit, lease)
}

// ClaimScanDiffJobs leases only work frozen into one scan manifest. A source
// revision changed after that barrier cannot enter this run's diff.
func (s *State) ClaimScanDiffJobs(ctx context.Context, epoch int64, kind string, limit int, lease time.Duration) ([]Job, error) {
	if epoch <= 0 || limit < 1 || limit > 1024 || lease <= 0 {
		return nil, errors.New("library indexer: invalid scan diff job claim bounds")
	}
	return s.claimJobs(ctx, kind, epoch, limit, lease)
}

// claimJobs selects candidates on a read connection and holds the single
// writer only for the guarded lease updates. Candidate selection walks an
// epoch-scoped eligible workset, so unrelated, blocked and completed work does
// not affect claim cost. A candidate that changed
// after the read snapshot fails its guard and is skipped, not leased.
func (s *State) claimJobs(ctx context.Context, kind string, epoch int64, limit int, lease time.Duration) ([]Job, error) {
	candidates, err := s.claimCandidates(ctx, kind, epoch, time.Now().UTC(), limit)
	if err != nil || len(candidates) == 0 {
		return nil, err
	}
	return s.leaseCandidates(ctx, candidates, lease)
}

// leaseCandidates leases each candidate only if it still matches the snapshot
// it was selected from; changed candidates are omitted from the result.
func (s *State) leaseCandidates(ctx context.Context, candidates []Job, lease time.Duration) ([]Job, error) {
	var jobs []Job
	err := s.write(ctx, true, func(conn *sql.Conn) error {
		jobs = jobs[:0]
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		now := time.Now().UTC()
		for _, job := range candidates {
			fence, err := randomFence()
			if err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, `UPDATE jobs SET state='leased',attempt=?,fence=?,lease_until=?,updated_at=?
				WHERE id=? AND kind=? AND source_revision=? AND semantic_key=? AND attempt=?
				AND (state='pending' OR (state='leased' AND lease_until<?))
				AND EXISTS(SELECT 1 FROM eligible_jobs e WHERE e.epoch_id=? AND e.kind=jobs.kind AND e.ready=1 AND e.job_id=jobs.id)`, job.Attempt+1, fence, now.Add(lease).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
				job.ID, job.Kind, job.SourceRevision, job.SemanticKey, job.Attempt, now.Format(time.RFC3339Nano), job.claimEpoch)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n != 1 {
				continue
			}
			job.Attempt++
			job.Fence = fence
			jobs = append(jobs, job)
		}
		return tx.Commit()
	})
	if err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *State) claimCandidates(ctx context.Context, kind string, epoch int64, now time.Time, limit int) ([]Job, error) {
	// Pending prefixes are already ordered by job ID. Expired leases use their
	// own expiry range, then sort the bounded active leases into the same order.
	var candidates []Job
	for _, selection := range []struct {
		state  string
		expiry string
	}{{state: "pending"}, {state: "leased", expiry: ` AND e.lease_until<?`}} {
		args := []any{epoch, kind, selection.state}
		if selection.expiry != "" {
			args = append(args, now.Format(time.RFC3339Nano))
		}
		args = append(args, limit)
		rows, err := s.reader.QueryContext(ctx, `SELECT j.id,j.kind,j.file_id,j.source_revision,j.semantic_key,j.attempt,j.retry_count
			FROM eligible_jobs e CROSS JOIN jobs j ON j.id=e.job_id
			WHERE e.epoch_id=? AND e.kind=? AND e.ready=1 AND e.state=?`+selection.expiry+` ORDER BY e.job_id LIMIT ?`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var job Job
			if err := rows.Scan(&job.ID, &job.Kind, &job.FileID, &job.SourceRevision, &job.SemanticKey, &job.Attempt, &job.RetryCount); err != nil {
				rows.Close()
				return nil, err
			}
			job.claimEpoch = epoch
			candidates = append(candidates, job)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
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
	Job              Job
	Contract         string
	Metadata         []byte
	DSP              []byte
	DSPCacheContract string
	Vector           []byte
	MERTData         []byte
	Dimension        int
}

// CommitPartialDSP preserves a valid independent DSP branch when MERT fails.
// It deliberately leaves the owning audio job leased so FailJob can record the
// missing MERT capability under the same fence.
func (s *State) CommitPartialDSP(ctx context.Context, job Job, contract, cacheContract string, data []byte) error {
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
		if cacheContract != "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO dsp_stage_cache(file_id,source_revision,contract,data) VALUES(?,?,?,?) ON CONFLICT(file_id,contract) DO UPDATE SET source_revision=excluded.source_revision,data=excluded.data`, job.FileID, source, cacheContract, data); err != nil {
				return err
			}
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
			var record MetadataRecord
			_ = json.Unmarshal(result.Metadata, &record)
			recordingKey := metadataRecordingIdentity(record)
			_, err = tx.ExecContext(ctx, `INSERT INTO track_metadata(file_id,source_revision,contract,data,recording_key) VALUES(?,?,?,?,?) ON CONFLICT(file_id,contract) DO UPDATE SET source_revision=excluded.source_revision,data=excluded.data,recording_key=excluded.recording_key`, result.Job.FileID, source, result.Contract, result.Metadata, recordingKey)
		case "dsp":
			_, err = tx.ExecContext(ctx, `INSERT INTO dsp_results(file_id,source_revision,contract,data) VALUES(?,?,?,?) ON CONFLICT(file_id,contract) DO UPDATE SET source_revision=excluded.source_revision,data=excluded.data`, result.Job.FileID, source, result.Contract, result.DSP)
		case "mert":
			_, err = tx.ExecContext(ctx, `INSERT INTO mert_results(file_id,source_revision,contract,dimension,vector,data) VALUES(?,?,?,?,?,?) ON CONFLICT(file_id,contract) DO UPDATE SET source_revision=excluded.source_revision,dimension=excluded.dimension,vector=excluded.vector,data=excluded.data`, result.Job.FileID, source, result.Contract, result.Dimension, result.Vector, result.MERTData)
		case "audio":
			if len(result.DSP) != 0 {
				_, err = tx.ExecContext(ctx, `INSERT INTO dsp_results(file_id,source_revision,contract,data) VALUES(?,?,?,?) ON CONFLICT(file_id,contract) DO UPDATE SET source_revision=excluded.source_revision,data=excluded.data`, result.Job.FileID, source, result.Contract, result.DSP)
			}
			if err == nil && len(result.DSP) != 0 && result.DSPCacheContract != "" {
				_, err = tx.ExecContext(ctx, `INSERT INTO dsp_stage_cache(file_id,source_revision,contract,data) VALUES(?,?,?,?) ON CONFLICT(file_id,contract) DO UPDATE SET source_revision=excluded.source_revision,data=excluded.data`, result.Job.FileID, source, result.DSPCacheContract, result.DSP)
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

type ReusableMERT struct {
	Vector    []byte
	Dimension int
}

func (s *State) CachedDSP(ctx context.Context, fileID, sourceRevision, contract string) ([]byte, bool, error) {
	var data []byte
	err := s.reader.QueryRowContext(ctx, `SELECT data FROM dsp_stage_cache WHERE file_id=? AND source_revision=? AND contract=?`, fileID, sourceRevision, contract).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return data, err == nil, err
}

// LoadReusableMERT reads the exact-contract recording index once before audio
// workers start. Subsequent deduplication is served by the analyzer's
// thread-safe in-memory cache rather than one SQLite query per file.
func (s *State) LoadReusableMERT(ctx context.Context, contract string) (map[string]ReusableMERT, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT m.recording_key,v.vector,v.dimension
		FROM track_metadata m
		JOIN files f ON f.id=m.file_id AND f.source_revision=m.source_revision AND f.status='present'
		JOIN mert_results v ON v.file_id=m.file_id AND v.source_revision=m.source_revision AND v.contract=?
		WHERE m.recording_key<>'' AND v.dimension=? AND length(v.vector)=?
		ORDER BY m.recording_key,m.file_id`, contract, audio.MERTDimension, audio.MERTDimension*4)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]ReusableMERT)
	for rows.Next() {
		var recordingKey string
		var value ReusableMERT
		if err := rows.Scan(&recordingKey, &value.Vector, &value.Dimension); err != nil {
			return nil, err
		}
		key := mertReuseKey(recordingKey, contract)
		if _, exists := result[key]; !exists {
			result[key] = value
		}
	}
	return result, rows.Err()
}

// ReleaseJob returns a leased job to the queue without charging a retry. It is
// used when shared infrastructure, not the track, prevented completion.
func (s *State) ReleaseJob(ctx context.Context, job Job) error {
	return s.writeBatch(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE jobs SET state='pending',fence='',lease_until=NULL,updated_at=? WHERE id=? AND state='leased' AND fence=? AND source_revision=?`, time.Now().UTC().Format(time.RFC3339Nano), job.ID, job.Fence, job.SourceRevision)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return errors.New("library indexer: stale job release")
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
	QueuedFiles   int64                       `json:"queuedFiles"`
	ActiveFiles   int64                       `json:"activeFiles"`
	FailedFiles   int64                       `json:"failedFiles"`
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
	kinds := make([]string, 0, len(semanticJobs))
	for kind := range semanticJobs {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	if len(kinds) == 0 {
		return snapshot, nil
	}
	clauses := make([]string, 0, len(kinds))
	args := make([]any, 0, len(kinds)*2)
	for _, kind := range kinds {
		clauses = append(clauses, `(j.kind=? AND j.semantic_key=?)`)
		args = append(args, kind, semanticJobs[kind])
	}
	query := `WITH per_file AS (
		SELECT j.file_id,
			MIN(j.state IN ('completed','failed')) AS finished,
			MAX(j.state='pending') AS pending,
			MAX(j.state='leased') AS leased,
			MAX(j.state='failed') AS failed,
			SUM(j.retry_count) AS retries
		FROM jobs j JOIN files f ON f.id=j.file_id
		WHERE f.status='present' AND j.state<>'superseded' AND (` + strings.Join(clauses, ` OR `) + `)
		GROUP BY j.file_id
	) SELECT COUNT(*),COUNT(*),COALESCE(SUM(finished),0),
		COALESCE(SUM(pending AND NOT leased),0),COALESCE(SUM(leased),0),
		COALESCE(SUM(failed),0),COALESCE(SUM(retries),0) FROM per_file`
	err := s.reader.QueryRowContext(ctx, query, args...).Scan(&snapshot.Files, &snapshot.Total, &snapshot.Finished, &snapshot.Queued, &snapshot.Leased, &snapshot.Failed, &snapshot.Retries)
	return snapshot, err
}

// ScanCandidateProgress reports both the unique supported audio files observed
// in this enumeration and the subset that still has compatible pending work.
// Directory-frontier tasks and unsupported filesystem entries are never
// processing units.
func (s *State) ScanCandidateProgress(ctx context.Context, epoch int64, semanticJobs map[string]string) (ProgressSnapshot, error) {
	var snapshot ProgressSnapshot
	kinds := make([]string, 0, len(semanticJobs))
	for kind := range semanticJobs {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	if epoch <= 0 || len(kinds) == 0 {
		return snapshot, nil
	}
	clauses := make([]string, 0, len(kinds))
	args := make([]any, 0, 1+len(kinds)*2)
	args = append(args, epoch)
	for _, kind := range kinds {
		clauses = append(clauses, `(j.kind=? AND j.semantic_key=?)`)
		args = append(args, kind, semanticJobs[kind])
	}
	query := `SELECT
		(SELECT COUNT(*) FROM files f WHERE f.status='present' AND f.last_seen_epoch=?),
		COUNT(DISTINCT j.file_id)
		FROM jobs j JOIN files f ON f.id=j.file_id
		WHERE f.status='present' AND f.last_seen_epoch=? AND j.state='pending' AND (` + strings.Join(clauses, ` OR `) + `)`
	args = append([]any{epoch}, args...)
	err := s.reader.QueryRowContext(ctx, query, args...).Scan(&snapshot.Files, &snapshot.Total)
	snapshot.Queued = snapshot.Total
	return snapshot, err
}

func (s *State) ScanDiffProgress(ctx context.Context, epoch int64) (ProgressSnapshot, error) {
	var snapshot ProgressSnapshot
	err := s.reader.QueryRowContext(ctx, `WITH per_file AS (
		SELECT j.file_id,
			MIN(j.state IN ('completed','failed')) AS finished,
			MAX(j.state='pending') AS pending,
			MAX(j.state='leased') AS leased,
			MAX(j.state='failed') AS failed,
			SUM(j.retry_count) AS retries
		FROM scan_diff_jobs d
		JOIN jobs j ON j.id=d.job_id AND j.source_revision=d.source_revision AND j.semantic_key=d.semantic_key
		WHERE d.epoch_id=? GROUP BY j.file_id
	) SELECT COUNT(*),COUNT(*),COALESCE(SUM(finished),0),
		COALESCE(SUM(pending AND NOT leased),0),COALESCE(SUM(leased),0),
		COALESCE(SUM(failed),0),COALESCE(SUM(retries),0) FROM per_file`, epoch).Scan(&snapshot.Files, &snapshot.Total, &snapshot.Finished, &snapshot.Queued, &snapshot.Leased, &snapshot.Failed, &snapshot.Retries)
	return snapshot, err
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
	if err := db.QueryRowContext(ctx, `WITH per_file AS (
		SELECT file_id,MAX(state='pending') AS pending,MAX(state='leased') AS leased,MAX(state='failed') AS failed
		FROM jobs j JOIN files f ON f.id=j.file_id
		WHERE f.status='present' AND j.state<>'superseded' GROUP BY file_id
	) SELECT COALESCE(SUM(pending AND NOT leased),0),COALESCE(SUM(leased),0),COALESCE(SUM(failed),0) FROM per_file`).Scan(&status.QueuedFiles, &status.ActiveFiles, &status.FailedFiles); err != nil {
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
	err = s.reader.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM jobs INDEXED BY jobs_claim_order WHERE kind=?1 AND state='pending'),
		(SELECT COUNT(*) FROM jobs INDEXED BY jobs_claim_order WHERE kind=?1 AND state='leased')`, kind).Scan(&pending, &leased)
	return
}

func (s *State) ScanDiffJobCounts(ctx context.Context, epoch int64, kind string) (pending, leased int64, err error) {
	// Include blocked work, but never unrelated epochs or completed jobs.
	err = s.reader.QueryRowContext(ctx, `SELECT COALESCE(SUM(state='pending'),0),COALESCE(SUM(state='leased'),0)
		FROM eligible_jobs WHERE epoch_id=? AND kind=?`, epoch, kind).Scan(&pending, &leased)
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
			WHERE audio.id IN (SELECT pending.id FROM jobs pending INDEXED BY jobs_claim_order WHERE pending.kind='audio' AND pending.state='pending')
			AND NOT EXISTS(
				SELECT 1 FROM jobs metadata WHERE metadata.file_id=audio.file_id AND metadata.kind='metadata'
				AND metadata.state='completed' AND metadata.source_revision=audio.source_revision)`, time.Now().UTC().Format(time.RFC3339Nano))
		return err
	})
}

func (s *State) FinalizeBlockedScanDiffAudio(ctx context.Context, epoch int64) error {
	if epoch <= 0 {
		return errors.New("library indexer: invalid scan diff epoch")
	}
	return s.write(ctx, true, func(conn *sql.Conn) error {
		// The projection contains only exact manifest revisions and semantics.
		// Its blocked-pending range excludes unrelated, completed and ready work.
		_, err := conn.ExecContext(ctx, `UPDATE jobs SET state='failed',error_code='metadata_unavailable',
			error_detail='audio analysis prerequisite metadata did not complete',updated_at=?
			WHERE id IN (SELECT job_id FROM eligible_jobs
				WHERE epoch_id=? AND kind='audio' AND ready=0 AND state='pending')`, time.Now().UTC().Format(time.RFC3339Nano), epoch)
		return err
	})
}
