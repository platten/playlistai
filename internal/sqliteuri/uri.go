// Package sqliteuri constructs escaped filesystem SQLite connection URIs.
package sqliteuri

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Path encodes an absolute path, including Windows drive and UNC paths, without
// allowing filename characters to become URI options or a fragment.
func Path(path string, options url.Values) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: options.Encode()}).String()
}

// ReadOnly opens an existing database. Immutable is appropriate only for
// shipped databases that will not change while the connection is open.
func ReadOnly(path string, immutable bool) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	options := url.Values{"mode": {"ro"}}
	if immutable {
		options.Set("immutable", "1")
	}
	return Path(absolute, options), nil
}

// Writable preserves the application's busy timeout and WAL journal settings.
func Writable(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := checkLegacyPath(absolute); err != nil {
		return "", err
	}
	return Path(absolute, url.Values{"_pragma": {"busy_timeout(5000)", "journal_mode(WAL)"}}), nil
}

// Older connection strings interpreted reserved filename characters as URI
// syntax. If that left a database at another path, do not silently create an
// empty replacement. Detection is read-only: ownership/schema and recovery of
// that database (including any WAL) need explicit review, not an automatic move.
func checkLegacyPath(absolute string) error {
	if _, err := os.Stat(absolute); !os.IsNotExist(err) {
		return nil
	}
	// SQLite discards literal fragments/queries before percent decoding and
	// tolerates a '%' that is not an escape. net/url.Parse is stricter and
	// would miss, for example, a real legacy database from "data#100%/db".
	raw := filepath.ToSlash(absolute)
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	var decoded strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] == '%' && i+2 < len(raw) {
			if b, err := strconv.ParseUint(raw[i+1:i+3], 16, 8); err == nil {
				if b == 0 { // SQLite's URI filename ends at an encoded NUL.
					break
				}
				decoded.WriteByte(byte(b))
				i += 2
				continue
			}
		}
		decoded.WriteByte(raw[i])
	}
	previous := filepath.FromSlash(decoded.String())
	if previous == absolute {
		return nil
	}
	f, err := os.Open(previous)
	if err != nil {
		return nil
	}
	defer f.Close()
	var header [16]byte
	if _, err := io.ReadFull(f, header[:]); err != nil || string(header[:]) != "SQLite format 3\x00" {
		return nil
	}
	return fmt.Errorf("possible legacy database at %q; refusing to create empty %q: close Playlist AI, back up the database and any -wal/-shm files, and verify its schema before recovering it to the intended path", previous, absolute)
}
