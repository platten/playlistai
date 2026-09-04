package ports

import "github.com/platten/playlistai/internal/core"

// Vectors holds one track's two embedding sub-vectors, each L2-normalized.
//
//   - Audio: the audio-content space (Deej-AI spotifytovec.p / MP3ToVec).
//   - Track: the Spotify-playlist co-occurrence space (Deej-AI tracktovec.p).
//
// Both have length Catalog.Dim().
type Vectors struct {
	Audio []float32
	Track []float32
}

// Catalog is the read-only shipped dataset: track metadata, the two embedding
// spaces, and a text search over "Artist - Title".
//
// Tracks have a stable row order (0..Len()-1) so a SimilarityEngine can build a
// dense matrix over Row(i); Row(i) corresponds to ID(i).
type Catalog interface {
	// Len is the number of tracks.
	Len() int
	// Dim is the per-space embedding dimension (100 for Deej-AI).
	Dim() int

	// ID returns the track id at a row, or "" if out of range.
	ID(row int) string
	// RowOf returns the row index for an id.
	RowOf(id string) (row int, ok bool)

	// Meta returns catalog metadata for an id.
	Meta(id string) (core.TrackMeta, bool)
	// VectorsByRow returns the embedding vectors at a row. The returned slices
	// may share the catalog's backing memory and must be treated as read-only.
	VectorsByRow(row int) (Vectors, bool)
	// Vectors returns the embedding vectors for an id.
	Vectors(id string) (Vectors, bool)

	// RawRow returns the row's two quantized (int8, scale 1/127) sub-vectors as
	// views into the catalog's backing memory. The views are valid for the
	// catalog's lifetime and must not be mutated. Used by the similarity engine
	// to avoid materializing a float32 copy of the whole catalog. Returns
	// nil, nil, false for an out-of-range row.
	RawRow(row int) (audio, track []int8, ok bool)

	// Resolve runs a token-substring search over "Artist - Title" (the same
	// normalization as deej-ai.online-app's /search) and returns up to max
	// matches, best-first.
	Resolve(query string, max int) []core.TrackRef
}
