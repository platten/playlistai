package libraryindex

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	sqlite "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/sqliteuri"
)

type CorpusSnapshot struct {
	Generation string `json:"generation"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Bytes      int64  `json:"bytes"`
}

type sqliteBackuper interface {
	NewBackup(string) (*sqlite.Backup, error)
}

// CreateSnapshot materializes a consistent SQLite generation after analysis
// commits drain. The online-backup handle is finished before readers open the
// immutable file; no live transaction is retained during fitting.
func (s *State) CreateSnapshot(ctx context.Context) (CorpusSnapshot, error) {
	var snapshot CorpusSnapshot
	root := filepath.Join(s.dir, "generations", "corpus")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return snapshot, err
	}
	stage, err := os.CreateTemp(root, ".snapshot-*.sqlite")
	if err != nil {
		return snapshot, err
	}
	stagePath := stage.Name()
	if err := stage.Close(); err != nil {
		_ = os.Remove(stagePath)
		return snapshot, err
	}
	_ = os.Remove(stagePath) // sqlite backup creates the destination itself
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(stagePath)
		}
	}()
	err = s.write(ctx, true, func(conn *sql.Conn) error {
		return conn.Raw(func(raw any) error {
			backuper, ok := raw.(sqliteBackuper)
			if !ok {
				return fmt.Errorf("library indexer: SQLite driver does not expose online backup (%T)", raw)
			}
			backup, err := backuper.NewBackup(stagePath)
			if err != nil {
				return err
			}
			for {
				more, stepErr := backup.Step(256)
				if stepErr != nil {
					return errors.Join(stepErr, backup.Finish())
				}
				if !more {
					break
				}
				if err := ctx.Err(); err != nil {
					return errors.Join(err, backup.Finish())
				}
			}
			return backup.Finish()
		})
	})
	if err != nil {
		return snapshot, err
	}
	file, err := os.Open(stagePath)
	if err != nil {
		return snapshot, err
	}
	hash := sha256.New()
	bytes, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return snapshot, err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	semanticDigest, err := snapshotSemanticDigest(ctx, stagePath)
	if err != nil {
		return snapshot, err
	}
	generation := "corpus-" + semanticDigest[:24]
	target := filepath.Join(root, generation+".sqlite")
	if _, statErr := os.Stat(target); statErr == nil {
		if err := os.Remove(stagePath); err != nil {
			return snapshot, err
		}
		digest, bytes, err = hashSnapshotFile(target)
		if err != nil {
			return snapshot, err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return snapshot, statErr
	} else if err := os.Rename(stagePath, target); err != nil {
		return snapshot, err
	}
	keep = true
	if err := syncFileAndDirectory(target, root); err != nil {
		return snapshot, err
	}
	return CorpusSnapshot{Generation: generation, Path: target, SHA256: digest, Bytes: bytes}, nil
}

func hashSnapshotFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	bytes, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), bytes, nil
}

// snapshotSemanticDigest excludes scan epochs, leases, timestamps, and SQLite
// page layout while retaining every current input/result contract and payload.
func snapshotSemanticDigest(ctx context.Context, path string) (string, error) {
	dsn, err := sqliteuri.ReadOnly(path, true)
	if err != nil {
		return "", err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT f.id,f.source_revision,j.kind,j.semantic_key,
		CASE j.kind WHEN 'metadata' THEN COALESCE(m.data,'') WHEN 'audio' THEN COALESCE(d.data,'') || COALESCE(v.vector,'') ELSE '' END
		FROM files f JOIN jobs j ON j.file_id=f.id AND j.source_revision=f.source_revision AND j.state='completed'
		LEFT JOIN track_metadata m ON j.kind='metadata' AND m.file_id=f.id AND m.source_revision=f.source_revision AND m.contract=j.semantic_key
		LEFT JOIN dsp_results d ON j.kind='audio' AND d.file_id=f.id AND d.source_revision=f.source_revision AND d.contract=j.semantic_key
		LEFT JOIN mert_results v ON j.kind='audio' AND v.file_id=f.id AND v.source_revision=f.source_revision AND v.contract=j.semantic_key
		WHERE f.status='present' ORDER BY f.id,j.kind,j.semantic_key`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	h := sha256.New()
	for rows.Next() {
		var values [5][]byte
		if err := rows.Scan(&values[0], &values[1], &values[2], &values[3], &values[4]); err != nil {
			return "", err
		}
		for _, value := range values {
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

func syncFileAndDirectory(path, directory string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	fileErr := errors.Join(file.Sync(), file.Close())
	dir, err := os.Open(directory)
	if err != nil {
		return errors.Join(fileErr, err)
	}
	return errors.Join(fileErr, dir.Sync(), dir.Close())
}
