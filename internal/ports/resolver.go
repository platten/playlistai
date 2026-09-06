package ports

import "github.com/platten/playlistai/internal/core"

// ReferenceResolver maps a typed user reference to real catalog entities. It
// deliberately returns ambiguity and failed matches as data rather than
// guessing, so callers can either ask the user or report a useful error.
type ReferenceResolver interface {
	ResolveReference(core.IntentReference) core.ReferenceResolution
	CatalogVersion() string
}
