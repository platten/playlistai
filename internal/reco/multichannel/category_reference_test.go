// Frozen pre-optimization search used only as a deterministic behavior oracle.
package multichannel

import (
	"context"
	"fmt"
	"sort"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type referenceCategoryPath struct {
	items        []sequenceItem
	stage        int
	nextRequired int
	lastWaypoint int
	score        float64
	capacity     int // upper bound on reachable total length without reversing stages
}

// referenceCategoryJourney jointly enforces direction, required-track order and artist
// adjacency. A bounded beam keeps alternatives per stage, using the existing
// transition objective. It returns a safe partial path when selected candidates
// cannot all be placed; it never fills a gap by reversing musical direction.
func (s *GreedySequencer) referenceCategoryJourney(ctx context.Context, request ports.SequenceRequest) ([]sequenceItem, bool, error) {
	pool := make([]sequenceItem, 0, len(request.Required)+len(request.Candidates))
	requiredOrder, waypointOrder := map[string]int{}, map[string]int{}
	for index, track := range request.Required {
		requiredOrder[track.ID] = index
		pool = append(pool, sequenceItem{track: track, required: true, fixed: true})
	}
	for index := range request.Candidates {
		candidate := &request.Candidates[index]
		pool = append(pool, sequenceItem{track: candidate.Track, candidate: candidate})
	}
	for index, track := range request.Waypoints {
		waypointOrder[track.ID] = index
	}
	sort.SliceStable(pool, func(i, j int) bool { return pool[i].track.ID < pool[j].track.ID })
	recordingKeys := make(map[string]string, len(pool))
	artistKeys := make(map[string]string, len(pool))
	for _, item := range pool {
		recordingKeys[item.track.ID] = core.ProvisionalRecordingKey(item.track)
		artistKeys[item.track.ID] = core.NormalizeIdentityPart(item.track.Artist)
	}
	stages := len(request.CategoryStages)
	frontier := []referenceCategoryPath{{stage: -1, lastWaypoint: -1}}
	var complete, partial *referenceCategoryPath
	gap := s.softArtistGap(request.Intent)
	for depth := 0; depth < min(request.Intent.Count, len(pool)) && len(frontier) > 0; depth++ {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		next := make([][]referenceCategoryPath, stages)
		for _, path := range frontier {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			// A named destination is an endpoint even when the starting
			// category has no required reference track. Never extend past it.
			if request.Intent.Destination != nil && len(request.Required) > 0 && path.nextRequired == len(request.Required) {
				continue
			}
			previous := s.startAnchor(request, path.items)
			previousArtist := artistKeys[previous.ID]
			if previous.ID != "" && previousArtist == "" {
				previousArtist = core.NormalizeIdentityPart(previous.Artist)
			}
			if len(path.items) == 0 && len(request.RecentSelections) == 0 {
				previousArtist = "" // reference anchors are not previously played output
			}
			used := map[string]bool{}
			for _, item := range path.items {
				used[recordingKeys[item.track.ID]] = true
			}
			capacity := make([]int, stages)
			for stage := max(0, path.stage); stage <= min(path.stage+1, stages-1); stage++ {
				remaining := map[string]bool{}
				for _, item := range pool {
					key := recordingKeys[item.track.ID]
					if used[key] {
						continue
					}
					for future := stage; future < stages; future++ {
						if request.CategoryStages[future][item.track.ID] {
							remaining[key] = true
							break
						}
					}
				}
				capacity[stage] = min(request.Intent.Count, len(path.items)+len(remaining))
			}
			for _, item := range pool {
				if used[recordingKeys[item.track.ID]] || request.Intent.Constraints.NoRepeatArtistBackToBack && previous.ID != "" && previousArtist != "" && previousArtist == artistKeys[item.track.ID] {
					continue
				}
				if item.required && requiredOrder[item.track.ID] != path.nextRequired {
					continue
				}
				waypoint, isWaypoint := waypointOrder[item.track.ID]
				if isWaypoint && waypoint < path.lastWaypoint {
					continue
				}
				candidate := core.Candidate{Track: item.track}
				if item.candidate != nil {
					candidate = *item.candidate
				}
				score := path.score + s.orderingScore(candidate, previous, request, depth)
				if gap > 0 && artistInTail(path.items, item.track.Artist, gap) {
					score-- // soft preference only; any relaxation remains visible
				}
				for stage := max(0, path.stage); stage <= min(path.stage+1, stages-1); stage++ {
					if !request.CategoryStages[stage][item.track.ID] {
						continue
					}
					extended := referenceCategoryPath{stage: stage, nextRequired: path.nextRequired, lastWaypoint: path.lastWaypoint, score: score, capacity: capacity[stage]}
					if item.required {
						extended.nextRequired++
						if request.Intent.Destination != nil && extended.nextRequired == len(request.Required) {
							extended.capacity = len(path.items) + 1
						}
					}
					if isWaypoint {
						extended.lastWaypoint = waypoint
					}
					// Bound memory before copying the path. Stable insertion plus
					// track-ID iteration gives deterministic tie breaking.
					bucket := next[stage]
					position := sort.Search(len(bucket), func(i int) bool { return referenceCategoryBeam(extended, bucket[i]) })
					if position >= categoryBeamWidth {
						continue
					}
					extended.items = append(append([]sequenceItem(nil), path.items...), item)
					if len(bucket) < categoryBeamWidth {
						bucket = append(bucket, referenceCategoryPath{})
					}
					copy(bucket[position+1:], bucket[position:len(bucket)-1])
					bucket[position] = extended
					next[stage] = bucket
					if extended.nextRequired == len(request.Required) {
						if referenceCategoryResult(extended, partial) {
							copyPath := extended
							partial = &copyPath
						}
						if stage == stages-1 && referenceCategoryResult(extended, complete) {
							copyPath := extended
							complete = &copyPath
						}
					}
				}
			}
		}
		frontier = nil
		for _, bucket := range next {
			frontier = append(frontier, bucket...)
		}
	}
	best := complete
	if best == nil {
		best = partial
	}
	if best == nil {
		if len(request.Required) > 0 {
			return nil, true, fmt.Errorf("%w: no evidence-backed category ordering could include every required track in order while respecting hard artist spacing", core.ErrRequiredTrackConflict)
		}
		return nil, true, nil
	}
	return best.items, complete == nil || len(best.items) < len(pool), nil
}

func referenceCategoryBeam(left, right referenceCategoryPath) bool {
	if left.nextRequired != right.nextRequired {
		return left.nextRequired > right.nextRequired
	}
	// Do not let a better immediate transition crowd out paths that can still
	// use the requested number of tracks. This is a bound, not weaker eligibility.
	if left.capacity != right.capacity {
		return left.capacity > right.capacity
	}
	return left.score > right.score
}

func referenceCategoryResult(path referenceCategoryPath, best *referenceCategoryPath) bool {
	if best == nil || path.stage != best.stage {
		return best == nil || path.stage > best.stage
	}
	if len(path.items) != len(best.items) {
		return len(path.items) > len(best.items)
	}
	return path.score > best.score
}
