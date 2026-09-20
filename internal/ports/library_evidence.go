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

// LibrarySemanticCatalog binds text queries to a request-owned, pinned catalog.
// Implementations reject incompatible spaces and never acquire audio remotely.
type LibrarySemanticCatalog interface {
	BindLibraryQueries(core.AudioModelIdentity, []core.AudioClauseVector)
	LibraryAssessment(context.Context, string) (core.AudioAssessment, bool, error)
	LibraryCLAPVector(context.Context, string) (core.LibraryVector, bool, error)
}

type LibraryMetadataCatalog interface {
	LibraryRecordingMetadata(context.Context, string) (core.EnrichedTrack, bool, error)
	LibraryTrackFeatures(context.Context, string) (core.LibraryTrackFeatures, bool)
	LibraryPreferenceScore(context.Context, string, core.MusicIntent, string) (float64, bool)
}
