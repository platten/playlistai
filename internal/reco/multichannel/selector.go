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

// contextEntry is one already-fixed or already-selected track, resolved once so
// the selection loop never re-reads the catalog for it.
type contextEntry struct {
	library    core.LibraryVector
	hasLibrary bool
	vectors    ports.Vectors
	hasVectors bool
	artistKey  string
	performers []string
	album      string
	hasAlbum   bool
}

// poolEntry carries a candidate's per-round diversity terms. Each term folds in
// one newly selected track at a time, so selection costs one update per
// remaining candidate per round instead of a rescan of the whole context.
type poolEntry struct {
	library       core.LibraryVector
	hasLibrary    bool
	candidate     core.Candidate
	vectors       ports.Vectors
	hasVectors    bool
	artistKey     string
	performers    []string
	outsideNamed  bool
	album         string
	hasAlbum      bool
	relevance     float64
	redundancy    float64
	redundancyOK  bool
	artistMatches int
	albumMatches  int
	albumKnown    int
	contextSize   int
}

func (s *MMRSelector) contextEntry(ctx context.Context, track core.TrackRef, useLibrary bool) (contextEntry, error) {
	entry := contextEntry{artistKey: core.NormalizeIdentityPart(track.Artist)}
	if s.cfg.LibraryEvidenceEnabled && useLibrary {
		var err error
		entry.library, entry.hasLibrary, err = lookupLibraryVector(ctx, s.cat, track.ID)
		if err != nil {
			return contextEntry{}, err
		}
	}
	entry.vectors, entry.hasVectors = s.cat.Vectors(track.ID)
	entry.album, entry.hasAlbum = reliableAlbumContext(ctx, s.cat, track.ID)
	return entry, ctx.Err()
}

// fold accumulates one context track into the running diversity terms.
func (p *poolEntry) fold(entry contextEntry, intent core.MusicIntent) {
	p.contextSize++
	if intent.Controls.RecommendationMode == core.EnhancedHybrid && p.hasLibrary && entry.hasLibrary {
		if similarity, measured := libraryCosine(p.library, entry.library); measured {
			p.redundancy = math.Max(p.redundancy, math.Max(0, similarity))
			p.redundancyOK = true
		}
	}
	if p.hasVectors && entry.hasVectors {
		if similarity, measured := weightedVectorSimilarity(p.vectors, entry.vectors,
			intent.Controls.AudioWeight, intent.Controls.CooccurrenceWeight); measured {
			p.redundancy = math.Max(p.redundancy, math.Max(0, similarity))
			p.redundancyOK = true
		}
	}
	if performersOverlap(p.performers, entry.performers) {
		p.artistMatches++
	}
	if entry.hasAlbum {
		p.albumKnown++
		if p.hasAlbum && entry.album == p.album {
			p.albumMatches++
		}
	}
}

// score writes the current diversity terms onto the candidate and returns its
// MMR value, matching the component semantics of the standalone helpers.
func (p *poolEntry) score(cfg Config, lambda float64) float64 {
	c := &p.candidate
	c.Scores.SelectionRelevance, c.Available.SelectionRelevance = p.relevance, true
	c.Scores.EmbeddingRedundancy, c.Available.EmbeddingRedundancy = p.redundancy, (p.hasVectors || p.hasLibrary) && p.redundancyOK
	c.Scores.ArtistConcentration, c.Available.ArtistConcentration =
		float64(p.artistMatches)/float64(maxInt(1, p.contextSize)), p.artistKey != ""
	c.Scores.AlbumConcentration, c.Available.AlbumConcentration =
		float64(p.albumMatches)/float64(maxInt(1, p.albumKnown)), p.hasAlbum
	var weighted, weights float64
	add := func(score, weight float64, available bool) {
		if available && weight > 0 {
			weighted += weight * score
			weights += weight
		}
	}
	add(c.Scores.EmbeddingRedundancy, cfg.EmbeddingRedundancyWeight, c.Available.EmbeddingRedundancy)
	add(c.Scores.ArtistConcentration, cfg.ArtistConcentrationWeight, c.Available.ArtistConcentration)
	add(c.Scores.AlbumConcentration, cfg.AlbumConcentrationWeight, c.Available.AlbumConcentration)
	penalty := 0.0
	if weights > 0 {
		penalty = weighted / weights
	}
	c.Scores.MMR, c.Available.MMR = lambda*p.relevance-(1-lambda)*penalty, true
	return c.Scores.MMR
}

func (s *MMRSelector) Select(ctx context.Context, candidates []core.Candidate, request ports.SelectionRequest) (ports.SelectionResult, error) {
	return s.selectCandidates(ctx, candidates, request, true)
}

// shortlist orders provisional candidates for analysis. Missing preview scores
// cannot yet justify rejecting a candidate at the final relevance floor. MMR
// still uses that floor to favor relevance, and final Select enforces it.
func (s *MMRSelector) shortlist(ctx context.Context, candidates []core.Candidate, request ports.SelectionRequest) (ports.SelectionResult, error) {
	return s.selectCandidates(ctx, candidates, request, false)
}

func (s *MMRSelector) selectCandidates(ctx context.Context, candidates []core.Candidate, request ports.SelectionRequest, enforceFloor bool) (ports.SelectionResult, error) {
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
	enhanced := request.Intent.Controls.RecommendationMode == core.EnhancedHybrid
	requestFloor := s.cfg.SelectionMinimumRelevance
	requestRelevance := enhancedRequestRelevances(candidates, request.Intent)
	pool := make([]poolEntry, 0, len(candidates))
	contextTracks := append([]core.TrackRef(nil), request.Required...)
	contextTracks = append(contextTracks, request.Waypoints...)
	contextTracks = append(contextTracks, tailTracks(request.RecentSelections, maxContinuationAnchors)...)
	named := namedReferenceArtistsContext(ctx, s.cat, request.Intent)
	allTracks := append(append([]core.TrackRef(nil), contextTracks...), named...)
	for _, candidate := range candidates {
		allTracks = append(allTracks, candidate.Track)
	}
	performers := newPerformerKeysContext(ctx, request.Intent, allTracks, s.cat)
	if err := ctx.Err(); err != nil {
		return ports.SelectionResult{}, err
	}
	for index, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return ports.SelectionResult{}, err
		}
		passesEnhancedRequestFloor := false
		if enforceFloor && request.Intent.Controls.RecommendationMode == core.EnhancedHybrid && candidate.FitTier == fitClose && candidate.MusicalFit != core.EvidenceMatch {
			relevance, ok := requestRelevance[index]
			if candidate.FitAssessment != nil && candidate.FitAssessment.PolicyVersion == core.AutomaticFitPolicyVersion {
				// The Automatic pipeline has already reconciled and frozen its
				// musical assessment. Legacy source fields are not its fit ledger.
				relevance.value, relevance.ok = candidateRequestFit(candidate, request.Intent)
				ok = true
			}
			if !ok || !relevance.ok || relevance.value < requestFloor {
				continue
			}
			passesEnhancedRequestFloor = true
		}
		if enforceFloor && !enhanced && !passesEnhancedRequestFloor && candidate.Scores.Total < floor && (request.Intent.VerificationPolicy != core.BestAvailable || candidate.MusicalFit != core.EvidenceMatch) {
			continue
		}
		entry := poolEntry{
			candidate:  candidate,
			artistKey:  core.NormalizeIdentityPart(candidate.Track.Artist),
			performers: performers.trackKeys(candidate.Track),
			relevance:  normalizedRelevance(candidate.Scores.Total, floor, best),
		}
		if enhanced {
			entry.relevance, _ = candidateRequestFit(candidate, request.Intent)
		}
		if core.RequiresOtherArtists(request.Intent) {
			entry.outsideNamed = outsideNamedArtists(performers, candidate.Track, named)
		}
		entry.vectors, entry.hasVectors = s.cat.Vectors(candidate.Track.ID)
		if s.cfg.LibraryEvidenceEnabled && request.Intent.Controls.RecommendationMode == core.EnhancedHybrid {
			var err error
			entry.library, entry.hasLibrary, err = lookupLibraryVector(ctx, s.cat, candidate.Track.ID)
			if err != nil {
				return ports.SelectionResult{}, err
			}
		}
		entry.album, entry.hasAlbum = reliableAlbumContext(ctx, s.cat, candidate.Track.ID)
		if err := ctx.Err(); err != nil {
			return ports.SelectionResult{}, err
		}
		pool = append(pool, entry)
	}
	result := ports.SelectionResult{Candidates: make([]core.Candidate, 0, request.Count), Notices: []core.PlaylistNotice{}}
	if len(pool) == 0 {
		result.Notices = append(result.Notices, selectionFloorNotice(request.Count, 0, floor))
		return result, nil
	}

	for _, track := range contextTracks {
		entry, err := s.contextEntry(ctx, track, request.Intent.Controls.RecommendationMode == core.EnhancedHybrid)
		if err != nil {
			return ports.SelectionResult{}, err
		}
		entry.performers = performers.trackKeys(track)
		for index := range pool {
			pool[index].fold(entry, request.Intent)
		}
	}

	lambda := 1 - (1-s.cfg.MMRMinimumLambda)*clamp(request.Intent.Controls.ArtistDiversity, 0, 1)
	artistDiversity := genreArtistDiversity(request.Intent)
	artistUses := map[string]int{}
	seenRequired := map[string]bool{}
	needsOther := core.RequiresOtherArtists(request.Intent) && !includesOtherArtistsContext(ctx, s.cat, request.Intent, request.Required)
	for _, track := range request.Required {
		if !seenRequired[track.ID] {
			artistUses[core.NormalizeIdentityPart(track.Artist)]++
			seenRequired[track.ID] = true
		}
	}
	for len(result.Candidates) < request.Count && len(pool) > 0 {
		if err := ctx.Err(); err != nil {
			return ports.SelectionResult{}, err
		}
		// Anchor the tolerance at the best fit in the best permitted tier. A
		// pairwise tolerance comparator would drift through chains of near ties.
		bestFit := math.Inf(-1)
		preferOther, preferStrong := false, false
		if enhanced {
			for _, entry := range pool {
				preferOther = preferOther || needsOther && entry.outsideNamed
			}
			for _, entry := range pool {
				if !preferOther || entry.outsideNamed {
					preferStrong = preferStrong || entry.candidate.FitTier == fitStrong
				}
			}
			for _, entry := range pool {
				if (!preferOther || entry.outsideNamed) && (!preferStrong || entry.candidate.FitTier == fitStrong) {
					bestFit = max(bestFit, entry.relevance)
				}
			}
		}
		chosen := -1
		for index := range pool {
			if enhanced && (preferOther && !pool[index].outsideNamed || preferStrong && pool[index].candidate.FitTier != fitStrong || pool[index].relevance < bestFit-s.cfg.EnhancedRelevanceTolerance) {
				continue
			}
			pool[index].score(s.cfg, lambda)
			better := chosen < 0
			if chosen >= 0 {
				left, right := pool[index].candidate, pool[chosen].candidate
				leftOther := pool[index].outsideNamed
				rightOther := pool[chosen].outsideNamed
				if needsOther && leftOther != rightOther {
					// Reserve the explicit requirement before first-N analysis can
					// stop. Candidates have already passed the applicable floor.
					better = leftOther
				} else if request.Intent.Controls.RecommendationMode == core.EnhancedHybrid && (left.FitTier == fitStrong) != (right.FitTier == fitStrong) {
					better = left.FitTier == fitStrong
				} else if request.Intent.Controls.RecommendationMode != core.EnhancedHybrid && request.Intent.VerificationPolicy == core.BestAvailable && (left.MusicalFit == core.EvidenceMatch) != (right.MusicalFit == core.EvidenceMatch) {
					better = left.MusicalFit == core.EvidenceMatch
				} else if enhanced && s.cfg.EnhancedRelevanceTolerance == 0 && pool[index].relevance != pool[chosen].relevance {
					better = pool[index].relevance > pool[chosen].relevance
				} else if artistDiversity && pool[index].artistKey != "" && pool[chosen].artistKey != "" && artistUses[pool[index].artistKey] != artistUses[pool[chosen].artistKey] {
					better = artistUses[pool[index].artistKey] < artistUses[pool[chosen].artistKey]
				} else {
					better = betterMMR(left, right)
				}
			}
			if better {
				chosen = index
			}
		}
		selected := pool[chosen]
		needsOther = needsOther && !selected.outsideNamed
		artistUses[selected.artistKey]++
		result.Candidates = append(result.Candidates, selected.candidate)
		pool = append(pool[:chosen], pool[chosen+1:]...)
		added := contextEntry{
			library: selected.library, hasLibrary: selected.hasLibrary,
			vectors: selected.vectors, hasVectors: selected.hasVectors,
			artistKey: selected.artistKey, album: selected.album, hasAlbum: selected.hasAlbum,
			performers: selected.performers,
		}
		for index := range pool {
			pool[index].fold(added, request.Intent)
		}
	}
	if len(result.Candidates) < request.Count {
		result.Notices = append(result.Notices, selectionFloorNotice(request.Count, len(result.Candidates), floor))
	}
	if err := ctx.Err(); err != nil {
		return ports.SelectionResult{}, err
	}
	return result, nil
}

func normalizedRelevance(score, floor, best float64) float64 {
	if best <= floor {
		return 1
	}
	return clamp((score-floor)/(best-floor), 0, 1)
}

func reliableAlbumContext(ctx context.Context, cat ports.Catalog, trackID string) (string, bool) {
	meta, ok := ports.CatalogMeta(ctx, cat, trackID)
	if !ok || !meta.AlbumReliable {
		return "", false
	}
	album := core.NormalizeIdentityPart(meta.Album)
	return album, album != ""
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

func reliableAlbum(cat ports.Catalog, trackID string) (string, bool) {
	return reliableAlbumContext(context.Background(), cat, trackID)
}
