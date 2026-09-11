package sqliteuri_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/history"
	"github.com/platten/playlistai/internal/sqliteuri"
	"github.com/platten/playlistai/internal/taste"
)

func TestStoresUseExactReservedCharacterPaths(t *testing.T) {
	names := []string{"data#100%"}
	if runtime.GOOS != "windows" {
		names = append(names, "data?reserved")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, name)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			h, err := history.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := h.Close(); err != nil {
				t.Fatal(err)
			}
			s, err := taste.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			for _, file := range []string{catalog.DBFile, catalog.VectorsFile} {
				raw, err := os.ReadFile(filepath.Join("..", "catalog", "testdata", file))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, file), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			c, err := catalog.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if c.Len() != 256 {
				t.Fatal("opened wrong catalog")
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 1 {
				t.Fatalf("unexpected sibling files: %v %v", entries, err)
			}
			for _, file := range []string{history.FileName, taste.FileName} {
				path := filepath.Join(dir, file)
				dsn, err := sqliteuri.Writable(path)
				if err != nil {
					t.Fatal(err)
				}
				db, err := sql.Open("sqlite", dsn)
				if err != nil {
					t.Fatal(err)
				}
				var mode string
				var timeout int
				if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
					t.Fatal(err)
				}
				_ = db.Close()
				if mode != "wal" || timeout != 5000 {
					t.Fatalf("connection options lost: %s %d", mode, timeout)
				}
			}
		})
	}
}

func TestReadOnlyDoesNotCreateOrPermitWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db#100%.sqlite")
	dsn, err := sqliteuri.ReadOnly(path, true)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if db.Ping() == nil {
		t.Fatal("opened absent read-only database")
	}
	_ = db.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("created absent database: %v", err)
	}
	writable, err := sqliteuri.Writable(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", writable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE example(id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	db, err = sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT INTO example VALUES(1)"); err == nil {
		t.Fatal("read-only database permitted mutation")
	}
}
