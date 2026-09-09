package metadata

import (
	"net/url"
	"path/filepath"
	"strings"
)

// readOnlyURI encodes an absolute filesystem path for SQLite Open and ATTACH.
func readOnlyURI(absPath string) string {
	path := filepath.ToSlash(absPath)
	// A Windows drive must be in /C:/..., not the authority of file://C:/...
	// ToSlash is platform-aware: a literal backslash in a Unix filename stays
	// intact. UNC paths keep their leading // in the path, with no URI host.
	// See https://www.sqlite.org/uri.html#the_uri_path.
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	return u.String()
}
