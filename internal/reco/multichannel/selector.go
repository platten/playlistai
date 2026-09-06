package multichannel

import (
	"context"
	"fmt"
	"math"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// MMRSelector applies a transparent maximal-marginal-relevance objective.
// ArtistDiversity moves lambda from 1 (pure relevance) toward
// MMRMinimumLambda. Redundancy is the maximum positive, intent-weighted cosine
// similarity to already fixed/selected tracks. Artist and reliable-album
// concentration are occurrence shares in that same context.
type MMRSelector struct {
	cat ports.Catalog
	cfg Config
}

func NewSelector(cat ports.Catalog, cfg Config) *MMRSelector {
	return &MMRSelector{cat: cat, cfg: cfg.normalized()}
}

func (s *MMRSelector) Select(ctx context.Context, candidates []core.Candidate, request ports.SelectionRequest) (ports.SelectionResult, error) {
	if request.Count <= 0 {
		return ports.SelectionResult{Candidates: []core.Candidate{}, Notices: []core.PlaylistNotice{}}, nil
	}
	if err := ctx.Err(); err != nil {
		return ports.SelectionResult{}, err
	}
	best := math.Inf(-1)
	for _, candidate := range candidates {
		best = math.Max(best, candidate.Scores.Total)
	}
	floor := math.Max(s.cfg.SelectionMinimumRelevance, best-s.cfg.SelectionRelevanceWindow)
	pool := make([]core.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Scores.Total >= floor {
			pool = append(pool, candidate)
		}
	}
	result := ports.SelectionResult{Candidates: make([]core.Candidate, 0, request.Count), Notices: []core.PlaylistNotice{}}
	if len(pool) == 0 {
		result.Notices = append(result.Notices, selectionFloorNotice(request.Count, 0, floor))
		return result, nil
	}

	contextTracks := append([]core.TrackRef(nil), request.Required...)
	contextTracks = append(contextTracks, request.Waypoints...)
	contextTracks = append(contextTracks, tailTracks(request.RecentSelections, maxContinuationAnchors)...)
	lambda := 1 - (1-s.cfg.MMRMinimumLambda)*clamp(request.Intent.Controls.ArtistDiversity, 0, 1)
	for len(result.Candidates) < request.Count && len(pool) > 0 {
		if err := ctx.Err(); err != nil {
			return ports.SelectionResult{}, err
		}
		chosen := -1
		for index := range pool {
			candidate := &pool[index]
			candidate.Scores.SelectionRelevance = normalizedRelevance(candidate.Scores.Total, floor, best)
			candidate.Available.SelectionRelevance = true
			candidate.Scores.EmbeddingRedundancy, candidate.Available.EmbeddingRedundancy =
				s.maxRedundancy(candidate.Track, contextTracks, request.Intent)
			candidate.Scores.ArtistConcentration, candidate.Available.ArtistConcentration =
				artistConcentration(candidate.Track, contextTracks)
			candidate.Scores.AlbumConcentration, candidate.Available.AlbumConcentration =
				s.albumConcentration(candidate.Track, contextTracks)
			penalty := s.diversityPenalty(*candidate)
			candidate.Scores.MMR = lambda*candidate.Scores.SelectionRelevance - (1-lambda)*penalty
			candidate.Available.MMR = true
			if chosen < 0 || betterMMR(*candidate, pool[chosen]) {
				chosen = index
			}
		}
		selected := pool[chosen]
		result.Candidates = append(result.Candidates, selected)
		contextTracks = append(contextTracks, selected.Track)
		pool = append(pool[:chosen], pool[chosen+1:]...)
	}
	if len(result.Candidates) < request.Count {
		result.Notices = append(result.Notices, selectionFloorNotice(request.Count, len(result.Candidates), floor))
	}
	return result, nil
}

func normalizedRelevance(score, floor, best float64) float64 {
	if best <= floor {
		return 1
	}
	return clamp((score-floor)/(best-floor), 0, 1)
}

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

func reliableAlbum(cat ports.Catalog, trackID string) (string, bool) {
	meta, ok := cat.Meta(trackID)
	if !ok || !meta.AlbumReliable {
		return "", false
	}
	album := core.NormalizeIdentityPart(meta.Album)
	return album, album != ""
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

func betterMMR(left, right core.Candidate) bool {
	if left.Scores.MMR != right.Scores.MMR {
		return left.Scores.MMR > right.Scores.MMR
	}
	if left.Scores.Total != right.Scores.Total {
		return left.Scores.Total > right.Scores.Total
	}
	return left.Track.ID < right.Track.ID
}

func selectionFloorNotice(requested, actual int, floor float64) core.PlaylistNotice {
	return core.PlaylistNotice{
		Code:      "selection_relevance_floor_exhausted",
		Detail:    fmt.Sprintf("not enough eligible candidates met the selection relevance floor %.3f", floor),
		Requested: requested, Actual: actual,
	}
}

var _ ports.CandidateSelector = (*MMRSelector)(nil)
