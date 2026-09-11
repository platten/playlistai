package metadata

import (
	"net/url"

	"github.com/platten/playlistai/internal/sqliteuri"
)

// readOnlyURI encodes an absolute filesystem path for SQLite Open and ATTACH.
func readOnlyURI(absPath string) string {
	// A Windows drive must be in /C:/..., not the authority of file://C:/...
	// ToSlash is platform-aware: a literal backslash in a Unix filename stays
	// intact. UNC paths keep their leading // in the path, with no URI host.
	// See https://www.sqlite.org/uri.html#the_uri_path.
	return sqliteuri.Path(absPath, url.Values{"mode": {"ro"}})
}
