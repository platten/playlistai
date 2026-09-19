package libraryindex

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
)

const (
	scanInventoryLimit      = 4096
	scanInventoryBytes      = 16 << 20
	scanInventoryFixedBytes = 1 << 20
)

type scanIdentityKey struct {
	rootID string
	device uint64
	inode  uint64
}

type scanInventoryFile struct {
	id       string
	revision string
	present  bool
}

// scanInventory is an in-memory snapshot of the scanned roots' durable
// inventory. It lets a rescan confirm an unchanged file with one last-seen
// update instead of the identity lookup, upsert, supersede and job upserts
// that ObserveFiles performs. Only the scan committer uses it.
type scanInventory struct {
	disabled      bool
	retainedBytes int64
	byPath        map[string]scanInventoryFile
	ids           map[string]struct{}
	identities    map[scanIdentityKey]string
	// ambiguous identities have several live rows; ObserveFiles resolves those
	// with an unordered LIMIT 1, so they always take the full path.
	ambiguous map[scanIdentityKey]struct{}
	// jobs holds the source revision of each file's settled current-key job
	// per kind.
	jobs map[string]map[string]string
	// staleJobs marks files that still have an unsuperseded job for another
	// semantic key, which ObserveFiles would supersede.
	staleJobs map[string]struct{}
	// Rows written through the full path during this scan no longer match the
	// snapshot, so files touching them must be re-resolved in SQLite.
	dirtyIDs        map[string]struct{}
	dirtyIdentities map[scanIdentityKey]struct{}
	semanticKeys    map[string]string
}

func scanPathKey(rootID, relativePath string) string { return rootID + "\x00" + relativePath }

func (s *State) loadScanInventory(ctx context.Context, roots []Root, semanticKeys map[string]string) (*scanInventory, error) {
	inventory := &scanInventory{
		retainedBytes: scanInventoryFixedBytes,
		byPath:        make(map[string]scanInventoryFile), ids: make(map[string]struct{}), identities: make(map[scanIdentityKey]string),
		ambiguous: make(map[scanIdentityKey]struct{}), jobs: make(map[string]map[string]string),
		staleJobs: make(map[string]struct{}), dirtyIDs: make(map[string]struct{}),
		dirtyIdentities: make(map[scanIdentityKey]struct{}), semanticKeys: semanticKeys,
	}
	if len(roots) == 0 {
		return inventory, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(roots)), ",")
	args := make([]any, len(roots))
	for i, root := range roots {
		args[i] = root.ID
	}
	rows, err := s.reader.QueryContext(ctx, `SELECT id,root_id,relative_path,device,inode,source_revision,status='present',tombstoned_at IS NULL
		FROM files WHERE root_id IN (`+placeholders+`) LIMIT 4097`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, rootID, relativePath, revision string
		var device, inode uint64
		var present, live bool
		if err := rows.Scan(&id, &rootID, &relativePath, &device, &inode, &revision, &present, &live); err != nil {
			rows.Close()
			return nil, err
		}
		if len(inventory.ids) >= scanInventoryLimit {
			inventory.disable()
			break
		}
		// Charge map buckets/spare capacity and conservatively duplicated string
		// storage before retaining any part of this row.
		if !inventory.reserveBytes(1024 + 2*int64(len(relativePath)) + 4*int64(len(rootID)+len(id)) + 2*int64(len(revision))) {
			break
		}
		inventory.byPath[scanPathKey(rootID, relativePath)] = scanInventoryFile{id: id, revision: revision, present: present && live}
		inventory.ids[id] = struct{}{}
		// Mirror ObserveFiles' identity lookup, which filters only tombstones.
		if !live || (device == 0 && inode == 0) {
			continue
		}
		key := scanIdentityKey{rootID: rootID, device: device, inode: inode}
		if _, exists := inventory.identities[key]; exists {
			inventory.ambiguous[key] = struct{}{}
		}
		inventory.identities[key] = id
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	if inventory.disabled {
		return inventory, nil
	}
	// A settled job is one the ObserveFiles upsert would leave unchanged for the
	// same revision: completed/failed, or pending with no lease or error left
	// over from a retry.
	rows, err = s.reader.QueryContext(ctx, `SELECT j.file_id,j.kind,j.semantic_key,j.source_revision,
			j.state IN ('completed','failed') OR (j.state='pending' AND j.fence='' AND j.lease_until IS NULL AND j.error_code='' AND j.error_detail='')
		FROM jobs j JOIN files f ON f.id=j.file_id
		WHERE f.root_id IN (`+placeholders+`) AND j.state<>'superseded'`, args...)
	if err != nil {
		return nil, err
	}
	jobRows := 0
	for rows.Next() {
		jobRows++
		if jobRows > scanInventoryLimit*max(1, len(semanticKeys)) {
			inventory.disable()
			break
		}
		var fileID, kind, semanticKey, revision string
		var settled bool
		if err := rows.Scan(&fileID, &kind, &semanticKey, &revision, &settled); err != nil {
			rows.Close()
			return nil, err
		}
		current, tracked := semanticKeys[kind]
		if !tracked {
			continue
		}
		if !inventory.reserveBytes(512 + 2*int64(len(fileID)+len(kind)+len(semanticKey)+len(revision))) {
			break
		}
		if semanticKey != current {
			inventory.staleJobs[fileID] = struct{}{}
			continue
		}
		if !settled {
			continue
		}
		if inventory.jobs[fileID] == nil {
			inventory.jobs[fileID] = make(map[string]string, len(semanticKeys))
		}
		inventory.jobs[fileID][kind] = revision
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	return inventory, nil
}

func closeRows(rows *sql.Rows) error {
	err := rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	return err
}

// unchanged reports whether ObserveFiles would leave every durable column of
// this normalized file and its jobs as they are, apart from last_seen_epoch
// and job timestamps. On true it sets file.ID to the durable identity.
func (inventory *scanInventory) unchanged(file *SourceFile) bool {
	if inventory.disabled {
		return false
	}
	record, ok := inventory.byPath[scanPathKey(file.RootID, file.RelativePath)]
	if !ok || !record.present || record.revision != file.SourceRevision {
		return false
	}
	if _, dirty := inventory.dirtyIDs[record.id]; dirty {
		return false
	}
	// normalizeSourceFile already set the default path identity.
	expected := file.ID
	if file.Device != 0 || file.Inode != 0 {
		key := scanIdentityKey{rootID: file.RootID, device: file.Device, inode: file.Inode}
		if _, dirty := inventory.dirtyIdentities[key]; dirty {
			return false
		}
		if _, ambiguous := inventory.ambiguous[key]; ambiguous {
			return false
		}
		prior, found := inventory.identities[key]
		if !found {
			return false
		}
		expected = prior
	}
	if expected != record.id {
		return false
	}
	if _, stale := inventory.staleJobs[record.id]; stale {
		return false
	}
	jobs := inventory.jobs[record.id]
	for kind := range inventory.semanticKeys {
		if revision, ok := jobs[kind]; !ok || revision != file.SourceRevision {
			return false
		}
	}
	file.ID = record.id
	return true
}

// unseen reports whether no durable row can match this normalized file: its
// path, default ID and native identity are all absent from the snapshot and
// untouched by this scan. For such a file the identity lookup and supersede
// statements of ObserveFiles are no-ops, and foreign keys guarantee that no
// jobs reference its new ID.
func (inventory *scanInventory) unseen(file SourceFile) bool {
	if inventory.disabled {
		return false
	}
	if _, known := inventory.byPath[scanPathKey(file.RootID, file.RelativePath)]; known {
		return false
	}
	if _, known := inventory.ids[file.ID]; known {
		return false
	}
	if _, dirty := inventory.dirtyIDs[file.ID]; dirty {
		return false
	}
	if file.Device == 0 && file.Inode == 0 {
		return true
	}
	key := scanIdentityKey{rootID: file.RootID, device: file.Device, inode: file.Inode}
	if _, known := inventory.identities[key]; known {
		return false
	}
	_, dirty := inventory.dirtyIdentities[key]
	return !dirty
}

// touched records a full-path observation so later files that share its row
// or identity are re-resolved in SQLite rather than trusted from the snapshot.
func (inventory *scanInventory) touched(file SourceFile) {
	if inventory.disabled {
		return
	}
	if len(inventory.dirtyIDs) >= scanInventoryLimit {
		inventory.disable()
		return
	}
	if !inventory.reserveBytes(1024 + 2*int64(len(file.ID)+len(file.RootID))) {
		return
	}
	inventory.dirtyIDs[file.ID] = struct{}{}
	if file.Device != 0 || file.Inode != 0 {
		inventory.dirtyIdentities[scanIdentityKey{rootID: file.RootID, device: file.Device, inode: file.Inode}] = struct{}{}
	}
}

// reserveBytes includes retained map/string storage; the fixed 1 MiB covers
// row decoding and map headers. Saturating the budget drops the snapshot and
// uses the indexed transactional guard instead of keeping a partial cache.
func (inventory *scanInventory) reserveBytes(bytes int64) bool {
	if inventory.disabled {
		return false
	}
	if bytes > scanInventoryBytes-inventory.retainedBytes {
		inventory.disable()
		return false
	}
	inventory.retainedBytes += bytes
	return true
}

// A partial snapshot cannot prove absence or unique identity. Above the cap,
// use ObserveFiles' indexed identity/path/job SQL instead of retaining a
// library-sized map or guessing from missing cache entries.
func (inventory *scanInventory) disable() {
	inventory.disabled = true
	inventory.retainedBytes = 0
	inventory.byPath = nil
	inventory.ids = nil
	inventory.identities = nil
	inventory.ambiguous = nil
	inventory.jobs = nil
	inventory.staleJobs = nil
	inventory.dirtyIDs = nil
	inventory.dirtyIdentities = nil
}

// scanUnchangedLookup retains no library-sized state. Its indexed, prepared
// read proves the same fast-path invariants as the small inventory snapshot
// against the current transaction, including observations in earlier chunks.
type scanUnchangedLookup struct {
	statement    *sql.Stmt
	semanticArgs []any
}

func prepareScanUnchanged(ctx context.Context, tx *sql.Tx, semanticKeys map[string]string) (*scanUnchangedLookup, error) {
	query := `SELECT f.id FROM files f WHERE f.root_id=? AND f.relative_path=? AND f.source_revision=?
 AND f.status='present' AND f.tombstoned_at IS NULL
 AND ((?=0 AND ?=0 AND f.id=?) OR ((?<>0 OR ?<>0) AND f.device=? AND f.inode=?
 AND NOT EXISTS(SELECT 1 FROM files identity WHERE identity.root_id=f.root_id AND identity.device=f.device AND identity.inode=f.inode AND identity.tombstoned_at IS NULL AND identity.id<>f.id)))`
	kinds := make([]string, 0, len(semanticKeys))
	for kind := range semanticKeys {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	var args []any
	for _, kind := range kinds {
		query += ` AND EXISTS(SELECT 1 FROM jobs j WHERE j.file_id=f.id AND j.kind=? AND j.semantic_key=? AND j.source_revision=f.source_revision
   AND (j.state IN ('completed','failed') OR (j.state='pending' AND j.fence='' AND j.lease_until IS NULL AND j.error_code='' AND j.error_detail='')))
   AND NOT EXISTS(SELECT 1 FROM jobs stale WHERE stale.file_id=f.id AND stale.kind=? AND stale.semantic_key<>? AND stale.state<>'superseded')`
		args = append(args, kind, semanticKeys[kind], kind, semanticKeys[kind])
	}
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &scanUnchangedLookup{statement: stmt, semanticArgs: args}, nil
}

func (lookup *scanUnchangedLookup) unchanged(ctx context.Context, file *SourceFile) (bool, error) {
	args := []any{file.RootID, file.RelativePath, file.SourceRevision, file.Device, file.Inode, file.ID, file.Device, file.Inode, file.Device, file.Inode}
	args = append(args, lookup.semanticArgs...)
	var id string
	err := lookup.statement.QueryRowContext(ctx, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	file.ID = id
	return true, nil
}
