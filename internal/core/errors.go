package core

import "errors"

// Sentinel errors shared across ports. Wrap with fmt.Errorf("%w", ...) for
// context; callers match with errors.Is.
var (
	// ErrNotFound is returned for an unknown track id / catalog key.
	ErrNotFound = errors.New("playlistai: not found")
	// ErrNotImplemented marks an interface method with no backend wired yet.
	ErrNotImplemented = errors.New("playlistai: not implemented")
	// ErrNoSeeds means an intent produced no resolvable seed tracks.
	ErrNoSeeds = errors.New("playlistai: no seeds resolved")
	// ErrCatalogEmpty means the catalog has not been downloaded/loaded.
	ErrCatalogEmpty = errors.New("playlistai: catalog is empty")
	// ErrUnavailable marks an optional backend that is not configured
	// (e.g. the Soundiiz API exporter with no token).
	ErrUnavailable = errors.New("playlistai: backend unavailable")
)
