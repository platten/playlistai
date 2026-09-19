package libraryindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"

	"github.com/platten/playlistai/internal/sqliteuri"
)

// A disk-backed B-tree provides whole-directory ordering with bounded cache
// memory. Native inode identity is order-sensitive for hard links, so sorting
// only ReadDir chunks would change the serial scanner's durable identity.
func newScanSpill(ctx context.Context, dir string) (*sql.DB, *sql.Tx, string, error) {
	file, err := os.CreateTemp(filepath.Join(dir, "scan-staging"), "directory-*")
	if err != nil {
		return nil, nil, "", err
	}
	name := file.Name()
	_ = file.Close()
	db, err := sql.Open("sqlite", sqliteuri.Path(name, nil))
	if err != nil {
		_ = os.Remove(name)
		return nil, nil, "", err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, `PRAGMA journal_mode=OFF; PRAGMA synchronous=OFF; PRAGMA cache_size=-1024; PRAGMA temp_store=FILE; CREATE TABLE entries(path TEXT PRIMARY KEY, file BLOB) WITHOUT ROWID`); err != nil {
		db.Close()
		os.Remove(name)
		return nil, nil, "", err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		db.Close()
		os.Remove(name)
		return nil, nil, "", err
	}
	return db, tx, name, nil
}

func writeScanSpill(ctx context.Context, tx *sql.Tx, chunk scanDirectoryChunk) error {
	insert, err := tx.PrepareContext(ctx, `INSERT INTO entries(path,file) VALUES(?,?)`)
	if err != nil {
		return err
	}
	defer insert.Close()
	for _, file := range chunk.Files {
		data, err := json.Marshal(file)
		if err != nil {
			return err
		}
		if _, err := insert.ExecContext(ctx, file.RelativePath, data); err != nil {
			return err
		}
	}
	for _, child := range chunk.Children {
		if _, err := insert.ExecContext(ctx, child, nil); err != nil {
			return err
		}
	}
	return nil
}

func readScanSpill(ctx context.Context, path string, consume func(scanDirectoryChunk) error) error {
	db, err := sql.Open("sqlite", sqliteuri.Path(path, url.Values{"mode": {"ro"}}))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA cache_size=-1024`); err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT path,file FROM entries ORDER BY path`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var buffer scanChunkBuffer
	for rows.Next() {
		var path string
		var data []byte
		if err := rows.Scan(&path, &data); err != nil {
			return err
		}
		if data == nil {
			if err := buffer.addChild(path, consume); err != nil {
				return err
			}
		} else {
			var file SourceFile
			if err := json.Unmarshal(data, &file); err != nil {
				return err
			}
			// JSON replaces invalid UTF-8, but Unix filenames may contain those
			// bytes. The SQLite key preserves the exact filesystem identity.
			file.RelativePath = path
			if err := buffer.addFile(file, consume); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return buffer.flush(consume)
}
