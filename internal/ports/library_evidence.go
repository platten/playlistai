package ports

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

// LibraryAudioCatalog is optional and independent of the Deej-AI Catalog
// vectors. Callers hold the catalog's generation pin throughout a request.
type LibraryAudioCatalog interface {
	LibraryVector(context.Context, string) (core.LibraryVector, bool, error)
	LibraryDSPPreference(context.Context, string, core.MusicIntent) (float64, bool)
}
