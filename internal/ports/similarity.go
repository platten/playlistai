package ports

import "context"

// Match is a scored catalog track.
type Match struct {
	ID    string
	Row   int
	Score float32 // blended cosine similarity to the query
}

// SimilarityQuery is a point in the blended two-space embedding: the (already
// noise-perturbed, un-normalized) sums of the seed sub-vectors plus the blend
// weights. Randomness lives in the RecommendationEngine, which owns the seeded
// RNG; by the time a query reaches the SimilarityEngine it is fully determined,
// so Search is a pure function.
type SimilarityQuery struct {
	AudioSum []float32           // Σ seed vectors in the audio space, plus any noise
	TrackSum []float32           // Σ seed vectors in the co-occurrence space, plus any noise
	Weights  [2]float32          // {creativity, 1 - creativity}
	K        int                 // number of matches to return
	Exclude  map[string]struct{} // track ids to omit from results
}

// SimilarityEngine ranks catalog tracks by blended cosine similarity to a
// query. The production implementation is brute force over the whole catalog,
// matching upstream deej-ai.online-app (no ANN index).
type SimilarityEngine interface {
	Search(ctx context.Context, q SimilarityQuery) ([]Match, error)
	// Len is the number of indexed tracks.
	Len() int
}
