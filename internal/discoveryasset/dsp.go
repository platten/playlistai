package discoveryasset

import (
	"context"
	"encoding/json"
	"math"

	"github.com/platten/playlistai/internal/librarylearn"
	"github.com/platten/playlistai/internal/librarypack"
)

type dspSource struct {
	tracks []librarypack.Track
	index  int
}

func (s *dspSource) Next(ctx context.Context) (librarylearn.DSPTrack, bool, error) {
	for s.index < len(s.tracks) {
		if e := ctx.Err(); e != nil {
			return librarylearn.DSPTrack{}, false, e
		}
		t := s.tracks[s.index]
		s.index++
		var record struct {
			Version  string `json:"version"`
			Sampling string `json:"sampling"`
			Scope    string `json:"scope"`
			Windows  []struct {
				ObservedSeconds float64 `json:"observedSeconds"`
				Features        map[string]struct {
					Value  *float64 `json:"value"`
					Reason string   `json:"reason"`
				} `json:"features"`
			} `json:"windows"`
		}
		if json.Unmarshal(t.DSP, &record) != nil || record.Version == "" || record.Sampling == "" || record.Scope == "" {
			continue
		}
		result := librarylearn.DSPTrack{TrackID: t.ID, Contract: librarylearn.DSPContract{Version: record.Version, Sampling: record.Sampling, Scope: record.Scope}}
		for _, name := range librarylearn.DSPFeatureNames {
			var total, known, weighted float64
			for _, w := range record.Windows {
				if w.ObservedSeconds <= 0 {
					continue
				}
				total += w.ObservedSeconds
				f := w.Features[name]
				if f.Value != nil && !math.IsNaN(*f.Value) && !math.IsInf(*f.Value, 0) {
					weighted += *f.Value * w.ObservedSeconds
					known += w.ObservedSeconds
				}
			}
			f := librarylearn.DSPFeatureValue{Name: name, Partial: known < total, MissingReason: "not_observed"}
			if known > 0 {
				value := weighted / known
				f.Value = &value
				f.MissingReason = ""
			}
			result.Features = append(result.Features, f)
		}
		return result, true, nil
	}
	return librarylearn.DSPTrack{}, false, nil
}
