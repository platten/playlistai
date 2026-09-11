package sqliteuri

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestLegacyDatabaseRequiresExplicitRecovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("old Windows drive URI is ambiguous; do not infer a recovery path")
	}
	for _, suffix := range []string{"#fragment/history.sqlite", "?query/history.sqlite", "%23encoded"} {
		prefix := filepath.Join(t.TempDir(), "data")
		t.Run(suffix, func(t *testing.T) {
			intended := prefix + suffix
			legacy := prefix
			if strings.HasPrefix(suffix, "%") {
				legacy = prefix + "#encoded"
			}
			data := []byte("SQLite format 3\x00do not modify")
			if err := os.WriteFile(legacy, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Writable(intended); err == nil || !strings.Contains(err.Error(), "possible legacy database") {
				t.Fatalf("missing recovery warning: %v", err)
			}
			got, err := os.ReadFile(legacy)
			if err != nil || string(got) != string(data) {
				t.Fatalf("legacy data changed: %q %v", got, err)
			}
			if _, err := os.Stat(intended); !os.IsNotExist(err) {
				t.Fatalf("replacement was created: %v", err)
			}
		})
	}
}

func TestActualLegacySQLiteURIWithPercentFragment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("old unescaped drive URI is not a portable fixture")
	}
	parent := t.TempDir()
	for _, name := range []string{"data#100%", "data?query", "data%25#fragment", "data%invalid#fragment"} {
		intended := filepath.Join(parent, name, "history.sqlite")
		db, err := sql.Open("sqlite", "file:"+intended+"?_pragma=busy_timeout(5000)")
		if err != nil {
			t.Fatal(err)
		}
		_, createErr := db.Exec("CREATE TABLE IF NOT EXISTS legacy_evidence(id INTEGER)")
		closeErr := db.Close()
		if createErr != nil || closeErr != nil {
			t.Fatalf("old DSN reproduction failed for %q: %v %v", name, createErr, closeErr)
		}
		if _, err := Writable(intended); err == nil || !strings.Contains(err.Error(), "possible legacy database") {
			t.Fatalf("actual old database missed for %q: %v", name, err)
		}
	}
}

func TestLegacyDetectionDoesNotGuessOwnership(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"absent.sqlite", "invalid%uri", "regular#file", "existing#db"} {
		if err := os.WriteFile(filepath.Join(dir, "regular"), []byte("not a database"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "existing#db"), []byte("existing target"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Writable(filepath.Join(dir, name)); err != nil {
			t.Fatalf("spurious warning for %q: %v", name, err)
		}
	}
}
