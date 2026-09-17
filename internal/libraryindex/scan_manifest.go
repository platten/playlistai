package libraryindex

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const ScanManifestVersion = 1

type ScanManifestReport struct {
	Version        int               `json:"version"`
	Epoch          int64             `json:"epoch"`
	CreatedAt      time.Time         `json:"createdAt"`
	InventoryPath  string            `json:"inventoryPath"`
	InventoryCount int64             `json:"inventoryCount"`
	InventoryHash  string            `json:"inventorySha256"`
	DiffPath       string            `json:"diffPath"`
	DiffCount      int64             `json:"diffCount"`
	DiffHash       string            `json:"diffSha256"`
	SemanticJobs   map[string]string `json:"semanticJobs"`
}

type scanManifestFile struct {
	FileID         string `json:"fileId"`
	RootAlias      string `json:"rootAlias"`
	Path           string `json:"path"`
	Size           int64  `json:"size"`
	SourceRevision string `json:"sourceRevision"`
	Extension      string `json:"extension"`
}

type scanDiffFile struct {
	scanManifestFile
	JobKind     string `json:"jobKind"`
	SemanticKey string `json:"semanticKey"`
}

type hashedJSONL struct {
	file   *os.File
	buffer *bufio.Writer
	hash   hash.Hash
	count  int64
}

func newHashedJSONL(path string) (*hashedJSONL, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	buffer := bufio.NewWriterSize(io.MultiWriter(file, h), 64<<10)
	return &hashedJSONL{file: file, buffer: buffer, hash: h}, nil
}

func (w *hashedJSONL) write(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := w.buffer.Write(append(raw, '\n')); err != nil {
		return err
	}
	w.count++
	return nil
}

func (w *hashedJSONL) close() (string, error) {
	if w == nil || w.file == nil {
		return "", nil
	}
	err := w.buffer.Flush()
	if err == nil {
		err = w.file.Sync()
	}
	err = errors.Join(err, w.file.Close())
	w.file = nil
	return hex.EncodeToString(w.hash.Sum(nil)), err
}

// WriteScanManifest materializes the completed enumeration and its pending-job
// diff before workers can claim any analysis. Paths are logical alias-relative
// paths; absolute source mount locations are deliberately omitted.
func (s *State) WriteScanManifest(ctx context.Context, epoch int64, semanticJobs map[string]string) (ScanManifestReport, error) {
	var report ScanManifestReport
	if epoch <= 0 || len(semanticJobs) == 0 {
		return report, errors.New("library indexer: scan epoch and semantic jobs are required for a manifest")
	}
	if err := s.write(ctx, true, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, `DELETE FROM scan_diff_jobs WHERE epoch_id=?`, epoch); err != nil {
			return err
		}
		keys := make([]string, 0, len(semanticJobs))
		for kind := range semanticJobs {
			keys = append(keys, kind)
		}
		sort.Strings(keys)
		for _, kind := range keys {
			if _, err := tx.ExecContext(ctx, `INSERT INTO scan_diff_jobs(epoch_id,job_id,source_revision,kind,semantic_key)
				SELECT ?,j.id,j.source_revision,j.kind,j.semantic_key
				FROM jobs j JOIN files f ON f.id=j.file_id
				WHERE f.status='present' AND f.last_seen_epoch=? AND j.state='pending' AND j.kind=? AND j.semantic_key=?
				ORDER BY j.id`, epoch, epoch, kind, semanticJobs[kind]); err != nil {
				return err
			}
		}
		return tx.Commit()
	}); err != nil {
		return report, err
	}
	base := filepath.Join(s.dir, "manifests")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return report, err
	}
	name := fmt.Sprintf("scan-%012d", epoch)
	stage, err := os.MkdirTemp(base, "."+name+"-stage-")
	if err != nil {
		return report, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stage)
		}
	}()

	inventory, err := newHashedJSONL(filepath.Join(stage, "inventory.jsonl"))
	if err != nil {
		return report, err
	}
	rows, err := s.reader.QueryContext(ctx, `SELECT f.id,r.alias,f.relative_path,f.size,f.source_revision,f.extension
		FROM files f JOIN roots r ON r.id=f.root_id
		WHERE f.status='present' AND f.last_seen_epoch=? ORDER BY r.alias,f.relative_path,f.id`, epoch)
	if err != nil {
		_, _ = inventory.close()
		return report, err
	}
	for rows.Next() {
		var item scanManifestFile
		if err := rows.Scan(&item.FileID, &item.RootAlias, &item.Path, &item.Size, &item.SourceRevision, &item.Extension); err != nil {
			rows.Close()
			_, _ = inventory.close()
			return report, err
		}
		item.Path = filepath.ToSlash(item.Path)
		if err := inventory.write(item); err != nil {
			rows.Close()
			_, _ = inventory.close()
			return report, err
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		_, _ = inventory.close()
		return report, err
	}
	inventoryHash, err := inventory.close()
	if err != nil {
		return report, err
	}

	diff, err := newHashedJSONL(filepath.Join(stage, "diff.jsonl"))
	if err != nil {
		return report, err
	}
	rows, err = s.reader.QueryContext(ctx, `SELECT f.id,r.alias,f.relative_path,f.size,d.source_revision,f.extension,d.kind,d.semantic_key
		FROM scan_diff_jobs d JOIN jobs j ON j.id=d.job_id JOIN files f ON f.id=j.file_id JOIN roots r ON r.id=f.root_id
		WHERE d.epoch_id=? ORDER BY r.alias,f.relative_path,f.id,d.kind`, epoch)
	if err != nil {
		_, _ = diff.close()
		return report, err
	}
	for rows.Next() {
		var item scanDiffFile
		if err := rows.Scan(&item.FileID, &item.RootAlias, &item.Path, &item.Size, &item.SourceRevision, &item.Extension, &item.JobKind, &item.SemanticKey); err != nil {
			rows.Close()
			_, _ = diff.close()
			return report, err
		}
		item.Path = filepath.ToSlash(item.Path)
		if err := diff.write(item); err != nil {
			rows.Close()
			_, _ = diff.close()
			return report, err
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		_, _ = diff.close()
		return report, err
	}
	diffHash, err := diff.close()
	if err != nil {
		return report, err
	}

	report = ScanManifestReport{
		Version: ScanManifestVersion, Epoch: epoch, CreatedAt: time.Now().UTC(),
		InventoryPath: filepath.ToSlash(filepath.Join("manifests", name, "inventory.jsonl")), InventoryCount: inventory.count, InventoryHash: inventoryHash,
		DiffPath: filepath.ToSlash(filepath.Join("manifests", name, "diff.jsonl")), DiffCount: diff.count, DiffHash: diffHash,
		SemanticJobs: make(map[string]string, len(semanticJobs)),
	}
	keys := make([]string, 0, len(semanticJobs))
	for key := range semanticJobs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		report.SemanticJobs[key] = semanticJobs[key]
	}
	manifestRaw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return report, err
	}
	manifestRaw = append(manifestRaw, '\n')
	manifestPath := filepath.Join(stage, "manifest.json")
	manifestFile, err := os.OpenFile(manifestPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return report, err
	}
	_, writeErr := manifestFile.Write(manifestRaw)
	if writeErr == nil {
		writeErr = manifestFile.Sync()
	}
	if err := errors.Join(writeErr, manifestFile.Close()); err != nil {
		return report, err
	}
	if err := syncScanManifestDirectory(stage); err != nil {
		return report, err
	}
	destination := filepath.Join(base, name)
	if err := os.Rename(stage, destination); err != nil {
		return report, err
	}
	published = true
	if err := syncScanManifestDirectory(base); err != nil {
		return report, err
	}
	return report, nil
}

func syncScanManifestDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
