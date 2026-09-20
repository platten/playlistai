package multichannel

import (
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func TestPackedJourneyRetainsDirectionWithoutDeej(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LibraryEvidenceEnabled = true
	s := NewSequencer(testCatalog(), cfg)
	v := func(x, y float32) core.LibraryVector {
		return core.LibraryVector{Source: core.LibraryEvidenceSource{SpaceID: "mert"}, Values: []float32{x, y}}
	}
	s.libraryVectors = map[string]core.LibraryVector{"start": v(1, 0), "end": v(0, 1), "candidate": v(1, 0)}
	request := ports.SequenceRequest{Intent: core.MusicIntent{Mode: core.ModeJourney}, Waypoints: []core.TrackRef{{ID: "start"}, {ID: "end"}}}
	request.Intent.Controls.RecommendationMode = core.EnhancedHybrid
	a, aOK := s.packedJourneySimilarity(core.TrackRef{ID: "candidate"}, request, 0)
	b, bOK := s.packedJourneySimilarity(core.TrackRef{ID: "candidate"}, request, 1)
	if !aOK || !bOK || a <= b {
		t.Fatalf("direction lost: %v %v", a, b)
	}
	request.Waypoints[0], request.Waypoints[1] = request.Waypoints[1], request.Waypoints[0]
	reversed, ok := s.packedJourneySimilarity(core.TrackRef{ID: "candidate"}, request, 1)
	if !ok || reversed != a {
		t.Fatal("reverse journey ignored")
	}
	bad := s.libraryVectors["end"]
	bad.Source.SpaceID = "other"
	s.libraryVectors["end"] = bad
	if _, ok := s.packedJourneySimilarity(core.TrackRef{ID: "candidate"}, request, .5); ok {
		t.Fatal("incompatible endpoints interpolated")
	}
}
