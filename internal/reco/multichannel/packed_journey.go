package multichannel

import (
	"math"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// A separate trajectory per representation avoids projecting CLAP or MERT
// into Deej space. Missing endpoints cannot fabricate a measured trajectory.
func (s *GreedySequencer) packedJourneySimilarity(track core.TrackRef, request ports.SequenceRequest, position float64) (float64, bool) {
	if !s.cfg.LibraryEvidenceEnabled || request.Intent.Controls.RecommendationMode != core.EnhancedHybrid || request.Intent.Mode != core.ModeJourney || len(request.Waypoints) < 2 {
		return 0, false
	}
	scaled := clamp(position, 0, 1) * float64(len(request.Waypoints)-1)
	segment := min(int(math.Floor(scaled)), len(request.Waypoints)-2)
	t := scaled - float64(segment)
	var score float64
	denominator, known := 1.0, false
	for _, family := range []struct {
		vectors map[string]core.LibraryVector
		weight  float64
	}{
		{s.libraryVectors, enhancedWeight(s.cfg.EnhancedMERTWeight)},
		{s.libraryCLAPVectors, enhancedWeight(s.cfg.EnhancedTransitionWeight)},
	} {
		if len(family.vectors) < 2 || family.weight == 0 {
			continue
		}
		denominator += family.weight
		a, aOK := family.vectors[request.Waypoints[segment].ID]
		b, bOK := family.vectors[request.Waypoints[segment+1].ID]
		candidate, cOK := family.vectors[track.ID]
		if !aOK || !bOK || !cOK {
			continue
		}
		if _, compatible := libraryCosine(a, b); !compatible {
			continue
		}
		target := core.LibraryVector{Source: a.Source, Values: make([]float32, len(a.Values))}
		for i := range target.Values {
			target.Values[i] = float32((1-t)*float64(a.Values[i]) + t*float64(b.Values[i]))
		}
		if similarity, ok := libraryCosine(candidate, target); ok {
			score += family.weight * similarity
			known = true
		}
	}
	return score / denominator, known
}
