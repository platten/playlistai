package localcatalog

import (
	"context"
	"encoding/json"
	"math"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarylearn"
	"github.com/platten/playlistai/internal/musicconcepts"
)

type portableDSPRecord struct {
	Version  string `json:"version"`
	Sampling string `json:"sampling"`
	Scope    string `json:"scope"`
	Windows  []struct {
		ObservedSeconds float64                    `json:"observedSeconds"`
		Features        map[string]json.RawMessage `json:"features"`
	} `json:"windows"`
}

type dspPreference struct {
	feature   string
	direction float64
}

// DSPPreferenceScore translates only reviewed concept-provider mappings. The
// result is a library-relative soft score over sampled measurements, never a
// categorical mood/quality claim or a hard filter.
func (c *Catalog) DSPPreferenceScore(ctx context.Context, id string, intent core.MusicIntent) (float64, bool) {
	if c.dspStats == nil {
		return 0, false
	}
	preferences := reviewedDSPPreferences(intent)
	if len(preferences) == 0 {
		return 0, false
	}
	localID, err := c.localID(id)
	if err != nil {
		return 0, false
	}
	generation, done, err := c.withGeneration()
	if err != nil {
		return 0, false
	}
	defer done()
	track, ok, err := generation.Lookup(ctx, localID)
	if err != nil || !ok || len(track.DSP) == 0 {
		return 0, false
	}
	var record portableDSPRecord
	if json.Unmarshal(track.DSP, &record) != nil {
		return 0, false
	}
	var group *librarylearn.DSPStatisticsGroup
	for index := range c.dspStats.Groups {
		candidate := &c.dspStats.Groups[index]
		if candidate.Contract.Version == record.Version && candidate.Contract.Sampling == record.Sampling && candidate.Contract.Scope == record.Scope {
			group = candidate
			break
		}
	}
	if group == nil {
		return 0, false
	}
	var total float64
	matched := 0
	for _, preference := range preferences {
		value, valueOK := sampledDSPMean(record, preference.feature)
		statistics, statisticsOK := dspFeatureStatistics(*group, preference.feature)
		if !valueOK || !statisticsOK || statistics.Quantiles == nil {
			continue
		}
		low, high := statistics.Quantiles.P25, statistics.Quantiles.P75
		if !(high > low) || math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		score := max(-1.0, min(1.0, (value-low)/(high-low)*2-1)) * preference.direction
		total += score
		matched++
	}
	if matched == 0 {
		return 0, false
	}
	return total / float64(matched), true
}

func reviewedDSPPreferences(intent core.MusicIntent) []dspPreference {
	all := append([]core.IntentPreference(nil), intent.Preferences.TextureDescriptions...)
	all = append(all, intent.Preferences.Styles...)
	all = append(all, intent.Preferences.Moods...)
	result := make([]dspPreference, 0)
	seen := map[string]bool{}
	for _, preference := range all {
		var concept musicconcepts.Concept
		var ok bool
		if preference.ConceptID != "" {
			concept, ok = musicconcepts.FindID(preference.ConceptID)
		} else {
			concept, ok = musicconcepts.Find("texture", preference.Value)
		}
		if !ok || len(concept.Providers.DSP) != 1 {
			continue
		}
		axis, direction, found := strings.Cut(concept.Providers.DSP[0], ":")
		if !found {
			continue
		}
		if axis == "transients" {
			axis = "positive_spectral_flux"
		}
		sign := 1.0
		if direction == "negative" {
			sign = -1
		}
		if preference.Influence == core.InfluenceNegative {
			sign = -sign
		}
		key := axis + ":" + direction
		if !seen[key] {
			seen[key] = true
			result = append(result, dspPreference{feature: axis, direction: sign})
		}
	}
	return result
}

func sampledDSPMean(record portableDSPRecord, feature string) (float64, bool) {
	var weighted, seconds float64
	for _, window := range record.Windows {
		if window.ObservedSeconds <= 0 {
			continue
		}
		raw, ok := window.Features[feature]
		if !ok {
			continue
		}
		var value core.DSPValue
		if json.Unmarshal(raw, &value) != nil || value.Value == nil {
			continue
		}
		weighted += *value.Value * window.ObservedSeconds
		seconds += window.ObservedSeconds
	}
	return weighted / seconds, seconds > 0
}

func dspFeatureStatistics(group librarylearn.DSPStatisticsGroup, feature string) (librarylearn.DSPFeatureStatistics, bool) {
	for _, statistics := range group.Features {
		if statistics.Name == feature {
			return statistics, true
		}
	}
	return librarylearn.DSPFeatureStatistics{}, false
}
