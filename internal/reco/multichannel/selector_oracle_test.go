package multichannel

import (
	"math"

	"github.com/platten/playlistai/internal/core"
)

// Historical non-incremental selection helpers are test-only oracles. Production
// selection maintains incremental terms in poolEntry instead of rescanning.
func (s *MMRSelector) maxRedundancy(candidate core.TrackRef, selected []core.TrackRef, intent core.MusicIntent) (float64, bool) {
	candidateVectors, ok := s.cat.Vectors(candidate.ID)
	if !ok {
		return 0, false
	}
	best, available := 0.0, false
	for _, track := range selected {
		vectors, ok := s.cat.Vectors(track.ID)
		if !ok {
			continue
		}
		similarity, measured := weightedVectorSimilarity(candidateVectors, vectors, intent.Controls.AudioWeight, intent.Controls.CooccurrenceWeight)
		if measured {
			best, available = math.Max(best, math.Max(0, similarity)), true
		}
	}
	return best, available
}

func artistConcentration(candidate core.TrackRef, selected []core.TrackRef) (float64, bool) {
	artist := core.NormalizeIdentityPart(candidate.Artist)
	if artist == "" {
		return 0, false
	}
	count := 0
	for _, track := range selected {
		if core.NormalizeIdentityPart(track.Artist) == artist {
			count++
		}
	}
	return float64(count) / float64(maxInt(1, len(selected))), true
}

func (s *MMRSelector) albumConcentration(candidate core.TrackRef, selected []core.TrackRef) (float64, bool) {
	album, ok := reliableAlbum(s.cat, candidate.ID)
	if !ok {
		return 0, false
	}
	known, matches := 0, 0
	for _, track := range selected {
		other, reliable := reliableAlbum(s.cat, track.ID)
		if !reliable {
			continue
		}
		known++
		if other == album {
			matches++
		}
	}
	return float64(matches) / float64(maxInt(1, known)), true
}

func (s *MMRSelector) diversityPenalty(candidate core.Candidate) float64 {
	var weighted, weights float64
	add := func(score, weight float64, available bool) {
		if available && weight > 0 {
			weighted += weight * score
			weights += weight
		}
	}
	add(candidate.Scores.EmbeddingRedundancy, s.cfg.EmbeddingRedundancyWeight, candidate.Available.EmbeddingRedundancy)
	add(candidate.Scores.ArtistConcentration, s.cfg.ArtistConcentrationWeight, candidate.Available.ArtistConcentration)
	add(candidate.Scores.AlbumConcentration, s.cfg.AlbumConcentrationWeight, candidate.Available.AlbumConcentration)
	if weights == 0 {
		return 0
	}
	return weighted / weights
}
