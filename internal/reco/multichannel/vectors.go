package multichannel

import (
	"math"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type weightedVectors struct {
	id     string
	weight float64
	v      ports.Vectors
}

type referenceVectors struct {
	id   string
	reps []weightedVectors
}

func positiveReferenceVectors(cat ports.Catalog, intent core.MusicIntent) []referenceVectors {
	return intentReferenceVectors(cat, intent, core.InfluencePositive)
}

func negativeReferenceVectors(cat ports.Catalog, intent core.MusicIntent) []referenceVectors {
	return intentReferenceVectors(cat, intent, core.InfluenceNegative)
}

func intentReferenceVectors(cat ports.Catalog, intent core.MusicIntent, influence core.Influence) []referenceVectors {
	references := append(append([]core.IntentReference(nil), intent.References...), intent.Journey.Waypoints...)
	seen := map[string]struct{}{}
	result := make([]referenceVectors, 0, len(references))
	for index, reference := range references {
		if reference.Influence != influence {
			continue
		}
		key := referenceKey(reference)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		reps := referenceRepresentatives(cat, reference)
		if len(reps) > 0 {
			result = append(result, referenceVectors{id: referenceID(reference, index), reps: reps})
		}
	}
	return result
}

func referenceRepresentatives(cat ports.Catalog, reference core.IntentReference) []weightedVectors {
	var tracks []core.WeightedTrack
	if reference.Resolution != nil && reference.Resolution.Selected != nil {
		tracks = reference.Resolution.Selected.Representatives
	}
	if len(tracks) == 0 && reference.TrackID != "" {
		tracks = []core.WeightedTrack{{TrackID: reference.TrackID, Weight: 1}}
	}
	out := make([]weightedVectors, 0, len(tracks))
	for _, track := range tracks {
		vectors, ok := cat.Vectors(track.TrackID)
		if !ok {
			continue
		}
		weight := track.Weight
		if weight <= 0 {
			weight = 1
		}
		out = append(out, weightedVectors{id: track.TrackID, weight: weight, v: normalizedVectors(vectors)})
	}
	return out
}

func referenceKey(reference core.IntentReference) string {
	if reference.Resolution != nil && reference.Resolution.Selected != nil {
		return string(reference.Kind) + "\x00" + reference.Resolution.Selected.EntityID
	}
	return string(reference.Kind) + "\x00" + reference.TrackID + "\x00" + reference.Query
}

func referenceID(reference core.IntentReference, index int) string {
	if reference.Resolution != nil && reference.Resolution.Selected != nil {
		return reference.Resolution.Selected.EntityID
	}
	if reference.TrackID != "" {
		return reference.TrackID
	}
	return string(reference.Kind) + ":" + reference.Query + ":" + itoa(index)
}

func normalizedVectors(v ports.Vectors) ports.Vectors {
	return ports.Vectors{Audio: normalizeVector(v.Audio), Track: normalizeVector(v.Track)}
}

func normalizeVector(values []float32) []float32 {
	out := append([]float32(nil), values...)
	var sum float64
	for _, value := range out {
		sum += float64(value) * float64(value)
	}
	if sum == 0 {
		return out
	}
	norm := float32(math.Sqrt(sum))
	for index := range out {
		out[index] /= norm
	}
	return out
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, aa, bb float64
	for index := range a {
		dot += float64(a[index]) * float64(b[index])
		aa += float64(a[index]) * float64(a[index])
		bb += float64(b[index]) * float64(b[index])
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / math.Sqrt(aa*bb)
}

func weightedSpaceSimilarity(candidate []float32, refs []referenceVectors, audio bool) (float64, bool) {
	if len(refs) == 0 || !vectorAvailable(candidate) {
		return 0, false
	}
	best, available := -2.0, false
	for _, reference := range refs {
		var score, weight float64
		for _, rep := range reference.reps {
			query := rep.v.Track
			if audio {
				query = rep.v.Audio
			}
			if len(query) != len(candidate) || !vectorAvailable(query) {
				continue
			}
			score += rep.weight * cosine(candidate, query)
			weight += rep.weight
		}
		if weight > 0 && score/weight > best {
			best = score / weight
			available = true
		}
	}
	return best, available
}

func affinitySimilarity(affinity core.EmbeddingAffinity, candidate ports.Vectors, audioWeight, trackWeight float64) (float64, bool) {
	var score, weights float64
	if len(affinity.Audio) == len(candidate.Audio) && len(candidate.Audio) > 0 && audioWeight > 0 && vectorAvailable(affinity.Audio) {
		score += audioWeight * cosine(affinity.Audio, candidate.Audio)
		weights += audioWeight
	}
	if len(affinity.Cooccurrence) == len(candidate.Track) && len(candidate.Track) > 0 && trackWeight > 0 && vectorAvailable(affinity.Cooccurrence) {
		score += trackWeight * cosine(affinity.Cooccurrence, candidate.Track)
		weights += trackWeight
	}
	if weights == 0 {
		return 0, false
	}
	return score / weights, true
}

func weightedVectorSimilarity(left, right ports.Vectors, audioWeight, trackWeight float64) (float64, bool) {
	var score, weights float64
	if audioWeight > 0 && len(left.Audio) == len(right.Audio) && vectorAvailable(left.Audio) && vectorAvailable(right.Audio) {
		score += audioWeight * cosine(left.Audio, right.Audio)
		weights += audioWeight
	}
	if trackWeight > 0 && len(left.Track) == len(right.Track) && vectorAvailable(left.Track) && vectorAvailable(right.Track) {
		score += trackWeight * cosine(left.Track, right.Track)
		weights += trackWeight
	}
	if weights == 0 {
		return 0, false
	}
	return score / weights, true
}

func vectorAvailable(values []float32) bool {
	for _, value := range values {
		if value != 0 {
			return true
		}
	}
	return false
}

func clamp(value, low, high float64) float64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
