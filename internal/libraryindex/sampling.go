package libraryindex

import (
	"errors"
	"math"
	"sort"
)

type SamplingProfile string

const (
	ProfileFast     SamplingProfile = "fast"
	ProfileBalanced SamplingProfile = "balanced"
	ProfileDeep     SamplingProfile = "deep"
	SamplingVersion                 = "centered-5s-fast3-balanced6-deep12-unique-observed/v1"
)

type SampleWindow struct {
	Index    int     `json:"index"`
	Start    float64 `json:"startSeconds"`
	Duration float64 `json:"durationSeconds"`
}

func SamplingWindows(duration float64, profile SamplingProfile) ([]SampleWindow, error) {
	if math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 {
		return nil, errors.New("library indexer: exact positive duration required for centered sampling")
	}
	var centers []float64
	switch profile {
	case ProfileFast:
		centers = []float64{0.18, 0.50, 0.82}
	case "", ProfileBalanced:
		centers = []float64{0.10, 0.26, 0.42, 0.58, 0.74, 0.90}
	case ProfileDeep:
		centers = make([]float64, 12)
		for i := range centers {
			centers[i] = 0.05 + 0.90*float64(i)/11
		}
	default:
		return nil, errors.New("library indexer: invalid sampling profile")
	}
	if duration <= 5 {
		return []SampleWindow{{Index: 0, Duration: duration}}, nil
	}
	type interval struct{ start, end float64 }
	candidates := make([]interval, 0, len(centers))
	for _, center := range centers {
		start := center*duration - 2.5
		start = math.Max(0, math.Min(duration-5, start))
		candidates = append(candidates, interval{start: start, end: start + 5})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].start < candidates[j].start })
	// Remove overlap rather than counting it twice. Short remnants under the
	// MERT 400-sample floor are omitted and recorded as uncovered by callers.
	const minimum = 400.0 / 24000.0
	coveredUntil := 0.0
	out := make([]SampleWindow, 0, len(candidates))
	for _, candidate := range candidates {
		start := math.Max(candidate.start, coveredUntil)
		if candidate.end-start < minimum {
			continue
		}
		out = append(out, SampleWindow{Index: len(out), Start: start, Duration: candidate.end - start})
		coveredUntil = candidate.end
	}
	return out, nil
}
