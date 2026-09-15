package multichannel

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const journeySearchLimit = 20000

// A bounded search failure is not evidence that the user's request conflicts.
var errJourneySearchExhausted = errors.New("bounded journey ordering search exhausted")

// repairRequiredJourney allows gap lengths to vary when the musical greedy
// ordering stalls. Candidates of the same artist are interchangeable for hard
// adjacency, so each state tries only the best-scoring unused one per artist.
// Required tracks remain in order, with the first and last as fixed endpoints.
// A safe partial result must still contain every required track.
func (s *GreedySequencer) repairRequiredJourney(ctx context.Context, request ports.SequenceRequest, greedy []sequenceItem, limit int) ([]sequenceItem, bool, error) {
	var best []sequenceItem
	if s.hardSpacingValid(greedy, request) {
		best = append([]sequenceItem(nil), greedy...)
	}
	groups := map[string]int{}
	var counts []int
	groupIDs := make([]int, len(request.Candidates))
	for i, candidate := range request.Candidates {
		artist := core.NormalizeIdentityPart(candidate.Track.Artist)
		group, exists := groups[artist]
		if !exists {
			group = len(counts)
			groups[artist] = group
			counts = append(counts, 0)
		}
		groupIDs[i] = group
		counts[group]++
	}
	used := make([]bool, len(request.Candidates))
	path := []sequenceItem{{track: request.Required[0], required: true, fixed: true}}
	if !s.hardSpacingValid(path, request) {
		return nil, true, fmt.Errorf("%w: the required start repeats the previous artist", core.ErrRequiredTrackConflict)
	}
	total := len(request.Required) + len(request.Candidates)
	seen := map[string]bool{}
	visited, exhausted := 0, false
	var search func(int) bool
	search = func(nextRequired int) bool {
		if ctx.Err() != nil {
			return false
		}
		if nextRequired == len(request.Required) {
			if len(path) > len(best) {
				best = append(best[:0], path...)
			}
			return len(path) == total
		}
		previous := path[len(path)-1].track
		key := strconv.AppendInt(nil, int64(nextRequired), 10)
		for _, count := range counts {
			key = append(key, ',')
			key = strconv.AppendInt(key, int64(count), 10)
		}
		key = append(key, ':')
		key = append(key, core.NormalizeIdentityPart(previous.Artist)...)
		if seen[string(key)] {
			return false
		}
		if visited >= limit {
			exhausted = true
			return false
		}
		visited++
		seen[string(key)] = true
		// Identity ties are stable; the order affects musical preference, not
		// which hard-spacing states can be reached by an artist group.
		choices := make([]int, len(counts))
		for i := range choices {
			choices[i] = -1
		}
		scores := make([]float64, len(request.Candidates))
		for i, candidate := range request.Candidates {
			if used[i] || sameArtist(previous, candidate.Track) {
				continue
			}
			scores[i] = s.orderingScore(candidate, previous, request, len(path))
			group := groupIDs[i]
			old := choices[group]
			if old < 0 || scores[i] > scores[old] || scores[i] == scores[old] && candidate.Track.ID < request.Candidates[old].Track.ID {
				choices[group] = i
			}
		}
		ordered := make([]int, 0, len(choices))
		for _, index := range choices {
			if index >= 0 {
				ordered = append(ordered, index)
			}
		}
		sort.Slice(ordered, func(i, j int) bool {
			left, right := ordered[i], ordered[j]
			if scores[left] != scores[right] {
				return scores[left] > scores[right]
			}
			return request.Candidates[left].Track.ID < request.Candidates[right].Track.ID
		})
		for _, index := range ordered {
			candidate := &request.Candidates[index]
			used[index] = true
			counts[groupIDs[index]]--
			path = append(path, sequenceItem{track: candidate.Track, candidate: candidate})
			complete := search(nextRequired)
			path = path[:len(path)-1]
			counts[groupIDs[index]]++
			used[index] = false
			if complete {
				return true
			}
			if exhausted || ctx.Err() != nil {
				return false
			}
		}
		track := request.Required[nextRequired]
		if !sameArtist(previous, track) {
			path = append(path, sequenceItem{track: track, required: true, fixed: true})
			complete := search(nextRequired + 1)
			path = path[:len(path)-1]
			return complete
		}
		return false
	}
	complete := search(1)
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if len(best) > 0 {
		return best, !complete, nil
	}
	if exhausted {
		return nil, true, errJourneySearchExhausted
	}
	return nil, true, fmt.Errorf("%w: the selected tracks cannot separate the required waypoints", core.ErrRequiredTrackConflict)
}
