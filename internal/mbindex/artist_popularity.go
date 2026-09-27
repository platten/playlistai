package mbindex

import (
	"context"
	"errors"
	"sort"

	"github.com/platten/playlistai/internal/core"
)

// ArtistPopularityReader reads prepared/cached public counts, never the network.
// One immutable reader is pinned for recognition and the following generation.
type ArtistPopularityReader interface {
	SnapshotIdentity() string
	LookupArtistPopularity(context.Context, []string) (map[string]core.ArtistPopularity, error)
}

const maxArtistPopularityMatches = 4096

var ErrArtistPopularityLookupTooBroad = errors.New("artist popularity lookup exceeds 4096 identity matches")

// WithArtistPopularity enables reversible defaults on a request-local store
// view. Closing either view closes the shared database; callers retain the
// original store's lifetime. A nil reader preserves contextual/name defaults.
func (s *Store) WithArtistPopularity(reader ArtistPopularityReader) *Store {
	out := *s
	out.artistPopularity, out.automaticArtists = reader, true
	return &out
}

func (s *Store) AutomaticArtistResolution() bool { return s.automaticArtists }

func artistLookupBound(automatic bool) string {
	if automatic {
		return " LIMIT 4097"
	}
	return ""
}

func (s *Store) rankArtistPopularity(ctx context.Context, byKey map[string][]ArtistIdentity) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	observations := map[string]core.ArtistPopularity{}
	if s.artistPopularity != nil {
		seen := map[string]bool{}
		var ids []string
		for _, candidates := range byKey {
			for _, c := range candidates {
				if !seen[c.MBID] {
					seen[c.MBID] = true
					ids = append(ids, c.MBID)
				}
			}
		}
		sort.Strings(ids)
		snapshot := s.artistPopularity.SnapshotIdentity()
		for at := 0; at < len(ids); at += 1000 {
			values, err := s.artistPopularity.LookupArtistPopularity(ctx, ids[at:min(at+1000, len(ids))])
			if err != nil {
				return err
			}
			for id, value := range values {
				if !seen[id] || snapshot == "" || value.Snapshot != snapshot || !value.Valid() {
					return errors.New("invalid artist popularity snapshot observation")
				}
				observations[id] = value
			}
		}
	}
	for key, candidates := range byKey {
		if err := ctx.Err(); err != nil {
			return err
		}
		for i := range candidates {
			if value, ok := observations[candidates[i].MBID]; ok {
				candidates[i].Popularity = value.Clone()
			}
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			eligibleI, eligibleJ := candidates[i].MatchType != ArtistMatchCredit, candidates[j].MatchType != ArtistMatchCredit
			if eligibleI != eligibleJ {
				return eligibleI
			}
			if compared := core.CompareArtistPopularity(candidates[i].Popularity, candidates[j].Popularity); compared != 0 {
				return compared > 0
			}
			// Unknown tie-breakers remain unknown for the decision, but display
			// ordering needs a total order independent of provider row order.
			a, b := candidates[i].Popularity, candidates[j].Popularity
			if a.Valid() && b.Valid() && a.UniqueListeners != nil && b.UniqueListeners != nil && (a.Listens != nil) != (b.Listens != nil) {
				return a.Listens != nil
			}
			if candidates[i].MatchType != candidates[j].MatchType {
				return artistMatchPriority(candidates[i].MatchType) < artistMatchPriority(candidates[j].MatchType)
			}
			return candidates[i].MBID < candidates[j].MBID
		})
		byKey[key] = candidates
	}
	return ctx.Err()
}

func artistMatchPriority(kind ArtistMatchType) int {
	switch kind {
	case ArtistMatchCanonical:
		return 0
	case ArtistMatchAlias:
		return 1
	default:
		return 2
	}
}
