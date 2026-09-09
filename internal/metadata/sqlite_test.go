package metadata

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReadOnlyURI(t *testing.T) {
	for _, tc := range []struct{ name, path, want string }{
		{"windows reported path", "C:/Users/pawel/AppData/Roaming/playlist-ai/metadata/.metadata-unpack-233181463.sqlite", "file:///C:/Users/pawel/AppData/Roaming/playlist-ai/metadata/.metadata-unpack-233181463.sqlite?mode=ro"},
		{"windows spaces and unicode", "D:/Users/Paweł Nowak/Music #100%/metadata.sqlite", "file:///D:/Users/Pawe%C5%82%20Nowak/Music%20%23100%25/metadata.sqlite?mode=ro"},
		{"unix", "/home/paul/metadata.sqlite", "file:///home/paul/metadata.sqlite?mode=ro"},
		{"query characters", "/tmp/metadata?mode=rw#%.sqlite", "file:///tmp/metadata%3Fmode=rw%23%25.sqlite?mode=ro"},
		{"unc no authority", "//server/share/metadata.sqlite", "file:////server/share/metadata.sqlite?mode=ro"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Native separators exercise backslashes on Windows CI; drive-letter
			// URI structure is also checked on Linux/macOS.
			got := readOnlyURI(filepath.FromSlash(tc.path))
			if got != tc.want {
				t.Fatalf("URI = %q, want %q", got, tc.want)
			}
			u, err := url.Parse(got)
			if err != nil || u.Host != "" || u.Fragment != "" || u.RawQuery != "mode=ro" {
				t.Fatalf("unexpected URI components: %+v, %v", u, err)
			}
		})
	}
}

func TestMetadataReadOnlyPaths(t *testing.T) {
	source, _, _ := compactFixture(t)
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"music metadata", "Paweł #100%"}
	if runtime.GOOS != "windows" {
		names = append(names, "literal\\backslash", "query?mode=rw")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), name)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "metadata.sqlite")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if !s.HasGenre(context.Background(), "Electronic") {
				t.Fatal("opened wrong database")
			}
			if _, err := s.db.Exec("DELETE FROM info"); err == nil {
				t.Fatal("read-only store permitted writes")
			}
			// Compact uses a second read-only URI through ATTACH DATABASE.
			if _, err := Compact(context.Background(), path, filepath.Join(dir, "runtime.sqlite")); err != nil {
				t.Fatal(err)
			}
			attached, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer attached.Close()
			attached.SetMaxOpenConns(1)
			if _, err := attached.Exec("ATTACH DATABASE ? AS source", readOnlyURI(path)); err != nil {
				t.Fatal(err)
			}
			if _, err := attached.Exec("DELETE FROM source.info"); err == nil {
				t.Fatal("read-only attachment permitted writes")
			}
		})
	}
}

func TestMetadataOpenDoesNotCreateMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing #100%.sqlite")
	if s, err := Open(path); err == nil {
		_ = s.Close()
		t.Fatal("opened missing database")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("missing database was created: %v", err)
	}
}
