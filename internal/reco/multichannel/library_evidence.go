package multichannel

import (
	"context"
	"math"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func libraryCosine(a, b core.LibraryVector) (float64, bool) {
	if a.Source.SpaceID == "" || a.Source.SpaceID != b.Source.SpaceID {
		return 0, false
	}
	return enhancedCosine(a.Values, b.Values, len(a.Values))
}

func (r *TransparentRanker) libraryScores(ctx context.Context, candidates []core.Candidate, request ports.RankRequest) error {
	if !r.cfg.LibraryEvidenceEnabled || request.Intent.Controls.RecommendationMode != core.EnhancedHybrid {
		return nil
	}
	catalog, ok := r.cat.(ports.LibraryAudioCatalog)
	if !ok {
		return nil
	}
	cache := map[string]core.LibraryVector{}
	get := func(id string) (core.LibraryVector, error) {
		if value, found := cache[id]; found {
			return value, nil
		}
		value, found, err := catalog.LibraryVector(ctx, id)
		if !found {
			value = core.LibraryVector{}
		}
		if err == nil {
			cache[id] = value
		}
		return value, err
	}
	refs := func(groups []referenceTracks, candidate core.LibraryVector) (float64, bool, error) {
		best, found := -2.0, false
		for _, group := range groups {
			var total, weight float64
			matched := false
			for _, rep := range group.reps {
				if rep.Weight <= 0 {
					continue
				}
				weight += rep.Weight
				vector, err := get(rep.TrackID)
				if err != nil {
					return 0, false, err
				}
				if score, ok := libraryCosine(candidate, vector); ok {
					total += score * rep.Weight
					matched = true
				}
			}
			if weight > 0 && matched && (!found || total/weight > best) {
				best, found = total/weight, true
			}
		}
		if !found {
			return 0, false, nil
		}
		return best, true, nil
	}
	positive := intentReferenceTracksContext(ctx, r.cat, request.Intent, core.InfluencePositive, false)
	negative := intentReferenceTracksContext(ctx, r.cat, request.Intent, core.InfluenceNegative, false)
	if err := ctx.Err(); err != nil {
		return err
	}
	for i := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &candidates[i]
		c.Scores.LibraryMERT, c.Available.LibraryMERT = 0, false
		c.Scores.LibraryDSP, c.Available.LibraryDSP = catalog.LibraryDSPPreference(ctx, c.Track.ID, request.Intent)
		vector, err := get(c.Track.ID)
		if err != nil {
			return err
		}
		if vector.Source.SpaceID != "" {
			p, pok, err := refs(positive, vector)
			if err != nil {
				return err
			}
			n, nok, err := refs(negative, vector)
			if err != nil {
				return err
			}
			c.Scores.LibraryMERT, c.Available.LibraryMERT = clamp(p-math.Max(0, n), -1, 1), pok || nok
		}
	}
	return nil
}

func lookupLibraryVector(ctx context.Context, cat ports.Catalog, id string) (core.LibraryVector, bool, error) {
	if err := ctx.Err(); err != nil {
		return core.LibraryVector{}, false, err
	}
	if provider, ok := cat.(ports.LibraryAudioCatalog); ok {
		vector, available, err := provider.LibraryVector(ctx, id)
		if contextErr := ctx.Err(); contextErr != nil {
			return core.LibraryVector{}, false, contextErr
		}
		return vector, available && err == nil, err
	}
	return core.LibraryVector{}, false, nil
}
