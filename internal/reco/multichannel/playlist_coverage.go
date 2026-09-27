package multichannel

import (
	"context"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

// Ordinary Group members are alternatives for one coverage obligation. The
// separate CoverageGroup still keeps independently requested sets distinct.
func playlistCoverageUnits(criteria []core.MusicalCriterion) [][]core.MusicalCriterion {
	var units [][]core.MusicalCriterion
	positions := map[string]int{}
	for _, c := range criteria {
		if c.CoverageGroup == "" {
			continue
		}
		key := c.CoverageGroup + "\x00" + c.Scope + "\x00group:" + c.Group
		if c.Group == "" {
			key = c.CoverageGroup + "\x00criterion:" + criterionKey(c)
		}
		if index, ok := positions[key]; ok {
			units[index] = append(units[index], c)
		} else {
			positions[key] = len(units)
			units = append(units, []core.MusicalCriterion{c})
		}
	}
	return units
}

func playlistCoverageReasons(criteria []core.MusicalCriterion, matched map[string]int) []core.OutcomeReason {
	var reasons []core.OutcomeReason
	for _, unit := range playlistCoverageUnits(criteria) {
		covered := false
		var values []string
		for _, c := range unit {
			covered = covered || matched[criterionKey(c)] > 0
			values = appendUniqueString(values, c.Value)
		}
		if !covered {
			reasons = append(reasons, core.OutcomeReason{Code: "playlist_genre_missing", Criterion: strings.Join(values, " or "), Detail: "The selected playlist has no independently supported track for this requested genre coverage.", Action: "Add a fitting reference or continue with the covered genres."})
		}
	}
	return reasons
}

// Reserve coverage from the same eligible ranked pool as ordinary selection.
// A recording may cover several genres. This bounded greedy reservation does
// not impose journey order or claim that an uncovered combination is impossible.
func (o *Orchestrator) reservePlaylistCoverage(ctx context.Context, ranked []core.Candidate, fixed []core.TrackRef, intent core.MusicIntent) ([]core.Candidate, []core.Candidate, error) {
	units := playlistCoverageUnits(intent.EssentialCriteria)
	if !o.enhanced || len(units) == 0 {
		return nil, ranked, nil
	}
	covered := make([]bool, len(units))
	used := map[string]bool{}
	for _, track := range fixed {
		used[core.ProvisionalRecordingKey(track)] = true
		for i, unit := range units {
			covered[i] = covered[i] || o.criterionGroupState(ctx, track.ID, unit) == core.EvidenceMatch
		}
	}
	membership := make([][]bool, len(ranked))
	for i, candidate := range ranked {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		membership[i] = make([]bool, len(units))
		for j, unit := range units {
			membership[i][j] = o.criterionGroupState(ctx, candidate.Track.ID, unit) == core.EvidenceMatch
		}
	}
	limit := intent.Count
	if intent.DurationSeconds > 0 && !intent.HasExplicitTrackCount() {
		limit = core.MaxCount
	}
	var reserved []core.Candidate
	for len(fixed)+len(reserved) < limit {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		best, gain := -1, 0
		for i, candidate := range ranked {
			if used[core.ProvisionalRecordingKey(candidate.Track)] {
				continue
			}
			next := 0
			for j, match := range membership[i] {
				if match && !covered[j] {
					next++
				}
			}
			if next > gain {
				best, gain = i, next
			}
		}
		if best < 0 {
			break
		}
		used[core.ProvisionalRecordingKey(ranked[best].Track)] = true
		reserved = append(reserved, ranked[best])
		for i, match := range membership[best] {
			covered[i] = covered[i] || match
		}
	}
	remaining := make([]core.Candidate, 0, len(ranked)-len(reserved))
	for _, candidate := range ranked {
		if !used[core.ProvisionalRecordingKey(candidate.Track)] {
			remaining = append(remaining, candidate)
		}
	}
	return reserved, remaining, ctx.Err()
}
