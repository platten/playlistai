package taste

import (
	"math"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type RequestAffinity struct {
	Positive    core.EmbeddingAffinity
	Negative    core.EmbeddingAffinity
	HasEvidence bool
}

type CandidateAffinity struct {
	Historical float64 `json:"historical"`
	Request    float64 `json:"request"`
	Effective  float64 `json:"effective"`
}

// AffinityFromIntent projects resolved current-request references without
// changing recommendation ranking. These explicit signals take precedence in
// ScoreCandidate and are ready for the next milestone's candidate objective.
func AffinityFromIntent(catalog ports.Catalog, intent core.MusicIntent) RequestAffinity {
	if catalog == nil {
		return RequestAffinity{}
	}
	var positive, negative []evidencePoint
	appendReference := func(reference core.IntentReference) {
		representatives := referenceTracks(reference)
		for _, representative := range representatives {
			vectors, ok := catalog.Vectors(representative.TrackID)
			if !ok {
				continue
			}
			weight := representative.Weight
			if weight <= 0 {
				weight = 1
			}
			point := evidencePoint{trackID: representative.TrackID, weight: weight, vectors: vectors}
			if reference.Influence == core.InfluenceNegative {
				negative = append(negative, point)
			} else {
				positive = append(positive, point)
			}
		}
	}
	intent = intent.Normalized()
	for _, reference := range intent.References {
		appendReference(reference)
	}
	for _, reference := range intent.Journey.Waypoints {
		appendReference(reference)
	}
	for _, reference := range intent.RequiredTracks {
		appendReference(reference)
	}
	return RequestAffinity{
		Positive: centroid(positive, catalog.Dim()), Negative: centroid(negative, catalog.Dim()),
		HasEvidence: len(positive)+len(negative) > 0,
	}
}

func referenceTracks(reference core.IntentReference) []core.WeightedTrack {
	if reference.Resolution != nil && reference.Resolution.Selected != nil {
		weighted := reference.Resolution.Selected.Representatives
		if len(weighted) > 0 {
			return weighted
		}
	}
	if reference.TrackID != "" {
		return []core.WeightedTrack{{TrackID: reference.TrackID, Weight: 1}}
	}
	return nil
}

// ScoreCandidate reports affinity without integrating it into ranking.
// Explicit request references dominate request-scoped feedback, which in turn
// dominates durable history when they conflict.
func ScoreCandidate(profile core.TasteProfile, candidate ports.Vectors, explicit RequestAffinity) CandidateAffinity {
	historical := historicalScore(profile, candidate)
	request := affinityScore(profile.RequestPositive, profile.RequestNegative, candidate)
	hasRequest := profile.RequestEvidence > 0
	if explicit.HasEvidence {
		explicitScore := affinityScore(explicit.Positive, explicit.Negative, candidate)
		if hasRequest {
			request = clamp(explicitScore + 0.25*request)
		} else {
			request = explicitScore
		}
		hasRequest = true
	}
	effective := historical
	if hasRequest {
		effective = clamp(request + 0.25*historical)
	}
	return CandidateAffinity{Historical: historical, Request: request, Effective: effective}
}

func historicalScore(profile core.TasteProfile, candidate ports.Vectors) float64 {
	positive := affinitySimilarity(profile.Positive, candidate)
	for _, cluster := range profile.Clusters {
		if score := affinitySimilarity(cluster.Affinity, candidate); score > positive {
			positive = score
		}
	}
	negative := math.Max(0, affinitySimilarity(profile.Negative, candidate))
	return clamp(positive - negative)
}

func affinityScore(positive, negative core.EmbeddingAffinity, candidate ports.Vectors) float64 {
	return clamp(affinitySimilarity(positive, candidate) - math.Max(0, affinitySimilarity(negative, candidate)))
}

func affinitySimilarity(affinity core.EmbeddingAffinity, candidate ports.Vectors) float64 {
	return (cosine(affinity.Audio, candidate.Audio) + cosine(affinity.Cooccurrence, candidate.Track)) / 2
}

func clamp(value float64) float64 {
	return math.Max(-1, math.Min(1, value))
}
