package multichannel

import (
	"context"
	"fmt"
	"sort"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const categoryBeamWidth = 32

type categoryPath struct {
	tail         int // index in the request-local predecessor arena; -1 is empty
	length       int
	stage        int
	nextRequired int
	lastWaypoint int
	score        float64
	capacity     int // upper bound on reachable total length without reversing stages
}

type categoryNode struct {
	parent int
	item   int
}

// categoryJourney jointly enforces direction, required-track order and artist
// adjacency. A bounded beam keeps alternatives per stage, using the existing
// transition objective. It returns a safe partial path when selected candidates
// cannot all be placed; it never fills a gap by reversing musical direction.
func (s *GreedySequencer) categoryJourney(ctx context.Context, request ports.SequenceRequest) ([]sequenceItem, bool, error) {
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
	stages := len(request.CategoryStages)
	recordings, artists := map[string]int{}, map[string]int{"": 0}
	recordingIDs, artistIDs := make([]int, len(pool)), make([]int, len(pool))
	requiredIndices, waypointIndices := make([]int, len(pool)), make([]int, len(pool))
	membership := make([]bool, stages*len(pool))
	var lastRecordingStage []int
	for index, item := range pool {
		key := core.ProvisionalRecordingKey(item.track)
		recording, exists := recordings[key]
		if !exists {
			recording = len(recordings)
			recordings[key] = recording
			lastRecordingStage = append(lastRecordingStage, -1)
		}
		recordingIDs[index] = recording
		artist := core.NormalizeIdentityPart(item.track.Artist)
		if _, exists := artists[artist]; !exists {
			artists[artist] = len(artists)
		}
		artistIDs[index] = artists[artist]
		requiredIndices[index] = requiredOrder[item.track.ID]
		waypointIndices[index] = -1
		if waypoint, exists := waypointOrder[item.track.ID]; exists {
			waypointIndices[index] = waypoint
		}
		for stage, tracks := range request.CategoryStages {
			if tracks[item.track.ID] {
				membership[stage*len(pool)+index] = true
				lastRecordingStage[recording] = max(lastRecordingStage[recording], stage)
			}
		}
	}
	start := s.startAnchor(request, nil)
	startArtist := artists[core.NormalizeIdentityPart(start.Artist)]
	nodes := make([]categoryNode, 0, min(request.Intent.Count, len(pool))*categoryBeamWidth*stages)
	used := make([]bool, len(recordings))
	tailArtists := make([]bool, len(artists))
	capacity := make([]int, stages)
	frontier := []categoryPath{{tail: -1, stage: -1, lastWaypoint: -1}}
	var complete, partial *categoryPath
	gap := s.softArtistGap(request.Intent)
	for depth := 0; depth < min(request.Intent.Count, len(pool)) && len(frontier) > 0; depth++ {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		next := make([][]categoryPath, stages)
		for _, path := range frontier {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			// A named destination is an endpoint even when the starting
			// category has no required reference track. Never extend past it.
			if request.Intent.Destination != nil && len(request.Required) > 0 && path.nextRequired == len(request.Required) {
				continue
			}
			previous, previousArtist := start, startArtist
			if path.tail >= 0 {
				item := nodes[path.tail].item
				previous, previousArtist = pool[item].track, artistIDs[item]
			}
			if path.length == 0 && len(request.RecentSelections) == 0 {
				previousArtist = 0 // reference anchors are not previously played output
			}
			clear(used)
			clear(tailArtists)
			for node, distance := path.tail, 0; node >= 0; node, distance = nodes[node].parent, distance+1 {
				item := nodes[node].item
				used[recordingIDs[item]] = true
				if distance < gap {
					tailArtists[artistIDs[item]] = true
				}
			}
			for stage := max(0, path.stage); stage <= min(path.stage+1, stages-1); stage++ {
				remaining := 0
				for recording, lastStage := range lastRecordingStage {
					if !used[recording] && lastStage >= stage {
						remaining++
					}
				}
				capacity[stage] = min(request.Intent.Count, path.length+remaining)
			}
			for index, item := range pool {
				if used[recordingIDs[index]] || request.Intent.Constraints.NoRepeatArtistBackToBack && previous.ID != "" && previousArtist != 0 && previousArtist == artistIDs[index] {
					continue
				}
				if item.required && requiredIndices[index] != path.nextRequired {
					continue
				}
				waypoint := waypointIndices[index]
				isWaypoint := waypoint >= 0
				if isWaypoint && waypoint < path.lastWaypoint {
					continue
				}
				candidate := core.Candidate{Track: item.track}
				if item.candidate != nil {
					candidate = *item.candidate
				}
				score := path.score + s.orderingScore(candidate, previous, request, depth)
				if gap > 0 && artistIDs[index] != 0 && tailArtists[artistIDs[index]] {
					score-- // soft preference only; any relaxation remains visible
				}
				for stage := max(0, path.stage); stage <= min(path.stage+1, stages-1); stage++ {
					if !membership[stage*len(pool)+index] {
						continue
					}
					extended := categoryPath{length: path.length + 1, stage: stage, nextRequired: path.nextRequired, lastWaypoint: path.lastWaypoint, score: score, capacity: capacity[stage]}
					if item.required {
						extended.nextRequired++
						if request.Intent.Destination != nil && extended.nextRequired == len(request.Required) {
							extended.capacity = extended.length
						}
					}
					if isWaypoint {
						extended.lastWaypoint = waypoint
					}
					// Preserve stable insertion and track-ID ties, but retain only
					// one predecessor node rather than copying every path prefix.
					bucket := next[stage]
					position := sort.Search(len(bucket), func(i int) bool { return betterCategoryBeam(extended, bucket[i]) })
					if position >= categoryBeamWidth {
						continue
					}
					extended.tail = len(nodes)
					nodes = append(nodes, categoryNode{parent: path.tail, item: index})
					if len(bucket) < categoryBeamWidth {
						bucket = append(bucket, categoryPath{})
					}
					copy(bucket[position+1:], bucket[position:len(bucket)-1])
					bucket[position] = extended
					next[stage] = bucket
					if extended.nextRequired == len(request.Required) {
						if betterCategoryResult(extended, partial) {
							copyPath := extended
							partial = &copyPath
						}
						if stage == stages-1 && betterCategoryResult(extended, complete) {
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
	items := make([]sequenceItem, best.length)
	for node, index := best.tail, best.length-1; node >= 0; node, index = nodes[node].parent, index-1 {
		items[index] = pool[nodes[node].item]
	}
	return items, complete == nil || best.length < len(pool), nil
}

func betterCategoryBeam(left, right categoryPath) bool {
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

func betterCategoryResult(path categoryPath, best *categoryPath) bool {
	if best == nil || path.stage != best.stage {
		return best == nil || path.stage > best.stage
	}
	if path.length != best.length {
		return path.length > best.length
	}
	return path.score > best.score
}
