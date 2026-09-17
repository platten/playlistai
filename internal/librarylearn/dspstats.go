package librarylearn

import (
	"bytes"
	"container/heap"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
)

const (
	DSPStatisticsVersion = "library-dsp-priority-quantiles/v1"
	DSPQuantileMethod    = "deterministic-keyed-priority-sample-type7/v1"
)

var DSPFeatureNames = []string{
	"rms_dbfs", "sample_peak_dbfs", "crest_factor_db", "short_window_rms_spread_db",
	"subbass_energy_ratio", "bass_energy_ratio", "treble_energy_ratio", "spectral_centroid_hz",
	"positive_spectral_flux", "onset_rate_hz",
}

type DSPContract struct {
	Version  string `json:"version"`
	Sampling string `json:"sampling"`
	Scope    string `json:"scope"`
}

type DSPFeatureValue struct {
	Name          string
	Value         *float64
	MissingReason string
	Partial       bool
}

type DSPTrack struct {
	TrackID  string
	Contract DSPContract
	Features []DSPFeatureValue
}

type DSPSource interface {
	Next(context.Context) (DSPTrack, bool, error)
}

type DSPStatisticsOptions struct {
	Seed                 uint64
	Workers              int
	MaxSamplesPerFeature int
	MaxScratchBytes      int64
	CorpusTracks         int
}

type DSPReasonCount struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

type DSPQuantiles struct {
	P05 float64 `json:"p05"`
	P25 float64 `json:"p25"`
	P50 float64 `json:"p50"`
	P75 float64 `json:"p75"`
	P95 float64 `json:"p95"`
}

type DSPFeatureStatistics struct {
	Name           string           `json:"name"`
	Known          int              `json:"known"`
	Missing        int              `json:"missing"`
	Partial        int              `json:"partial"`
	MissingReasons []DSPReasonCount `json:"missingReasons,omitempty"`
	SampleCount    int              `json:"sampleCount"`
	Approximate    bool             `json:"approximate"`
	Quantiles      *DSPQuantiles    `json:"quantiles,omitempty"`
	Breakpoints    []float64        `json:"breakpoints,omitempty"`
}

type DSPStatisticsGroup struct {
	ID       string                 `json:"id"`
	Contract DSPContract            `json:"contract"`
	Tracks   int                    `json:"tracks"`
	Features []DSPFeatureStatistics `json:"features"`
}

type DSPStatisticsModel struct {
	Version        string               `json:"version"`
	Generation     string               `json:"generation"`
	QuantileMethod string               `json:"quantileMethod"`
	Seed           uint64               `json:"seed"`
	CorpusTracks   int                  `json:"corpusTracks"`
	DSPTracks      int                  `json:"dspTracks"`
	MissingDSP     int                  `json:"missingDsp"`
	Groups         []DSPStatisticsGroup `json:"groups"`
}

type dspSample struct {
	priority [32]byte
	trackID  string
	value    float64
}

type dspMaxHeap []dspSample

func (h dspMaxHeap) Len() int { return len(h) }
func (h dspMaxHeap) Less(i, j int) bool {
	cmp := bytes.Compare(h[i].priority[:], h[j].priority[:])
	if cmp == 0 {
		return h[i].trackID > h[j].trackID
	}
	return cmp > 0
}
func (h dspMaxHeap) Swap(i, j int)   { h[i], h[j] = h[j], h[i] }
func (h *dspMaxHeap) Push(value any) { *h = append(*h, value.(dspSample)) }
func (h *dspMaxHeap) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
}

type dspFeatureAccumulator struct {
	known, missing, partial int
	reasons                 map[string]int
	samples                 dspMaxHeap
}

type dspGroupAccumulator struct {
	contract DSPContract
	tracks   int
	features map[string]*dspFeatureAccumulator
}

func BuildDSPStatistics(ctx context.Context, source DSPSource, options DSPStatisticsOptions) (DSPStatisticsModel, error) {
	if source == nil {
		return DSPStatisticsModel{}, errors.New("librarylearn: DSP source is required")
	}
	if options.MaxSamplesPerFeature <= 0 {
		options.MaxSamplesPerFeature = 1024
	}
	if options.MaxScratchBytes <= 0 {
		options.MaxScratchBytes = 8 << 20
	}
	model := DSPStatisticsModel{Version: DSPStatisticsVersion, QuantileMethod: DSPQuantileMethod, Seed: options.Seed, CorpusTracks: options.CorpusTracks}
	groups := map[string]*dspGroupAccumulator{}
	lastID := ""
	totalSampleBytes := int64(0)
	for {
		track, ok, err := source.Next(ctx)
		if err != nil {
			return DSPStatisticsModel{}, err
		}
		if !ok {
			break
		}
		track.TrackID = strings.TrimSpace(track.TrackID)
		if track.TrackID == "" || len(track.TrackID) > 1024 || lastID != "" && track.TrackID <= lastID {
			return DSPStatisticsModel{}, errors.New("librarylearn: DSP track IDs must be unique and strictly increasing")
		}
		lastID = track.TrackID
		if strings.TrimSpace(track.Contract.Version) == "" || strings.TrimSpace(track.Contract.Sampling) == "" || strings.TrimSpace(track.Contract.Scope) == "" || len(track.Contract.Version)+len(track.Contract.Sampling)+len(track.Contract.Scope) > 3072 {
			return DSPStatisticsModel{}, errors.New("librarylearn: incomplete DSP compatibility contract")
		}
		groupID := dspGroupID(track.Contract)
		group := groups[groupID]
		if group == nil {
			if len(groups) >= 1024 || int64(len(groups)+1)*8192+totalSampleBytes > options.MaxScratchBytes {
				return DSPStatisticsModel{}, errors.New("librarylearn: DSP compatibility groups exceed scratch budget")
			}
			group = &dspGroupAccumulator{contract: track.Contract, features: map[string]*dspFeatureAccumulator{}}
			groups[groupID] = group
		}
		group.tracks++
		model.DSPTracks++
		values := map[string]DSPFeatureValue{}
		for _, value := range track.Features {
			if !validDSPFeature(value.Name) {
				return DSPStatisticsModel{}, errors.New("librarylearn: unknown DSP feature")
			}
			if _, exists := values[value.Name]; exists {
				return DSPStatisticsModel{}, errors.New("librarylearn: duplicate DSP feature")
			}
			values[value.Name] = value
		}
		for _, name := range DSPFeatureNames {
			acc := group.features[name]
			if acc == nil {
				acc = &dspFeatureAccumulator{reasons: map[string]int{}}
				group.features[name] = acc
			}
			value, exists := values[name]
			if !exists || value.Value == nil {
				acc.missing++
				reason := strings.TrimSpace(value.MissingReason)
				if reason == "" {
					reason = "not_observed"
				}
				if len(reason) > 256 {
					return DSPStatisticsModel{}, errors.New("librarylearn: DSP missingness reason exceeds limit")
				}
				if _, exists := acc.reasons[reason]; !exists && len(acc.reasons) >= 64 {
					return DSPStatisticsModel{}, errors.New("librarylearn: too many DSP missingness reasons")
				}
				acc.reasons[reason]++
				continue
			}
			if math.IsNaN(*value.Value) || math.IsInf(*value.Value, 0) {
				return DSPStatisticsModel{}, errors.New("librarylearn: nonfinite DSP value")
			}
			acc.known++
			if value.Partial {
				acc.partial++
			}
			sample := dspSample{priority: dspPriority(options.Seed, groupID, name, track.TrackID), trackID: track.TrackID, value: *value.Value}
			// Includes heap metadata/string bytes and the persisted float64
			// breakpoint built during the fixed-order final merge.
			sampleBytes := int64(96 + len(sample.trackID))
			if len(acc.samples) < options.MaxSamplesPerFeature {
				if int64(len(groups))*8192+totalSampleBytes+sampleBytes > options.MaxScratchBytes {
					return DSPStatisticsModel{}, errors.New("librarylearn: DSP quantile samples exceed scratch budget")
				}
				heap.Push(&acc.samples, sample)
				totalSampleBytes += sampleBytes
			} else if sampleLess(sample, acc.samples[0]) {
				replacementBytes := totalSampleBytes - int64(96+len(acc.samples[0].trackID)) + sampleBytes
				if int64(len(groups))*8192+replacementBytes > options.MaxScratchBytes {
					return DSPStatisticsModel{}, errors.New("librarylearn: DSP quantile samples exceed scratch budget")
				}
				acc.samples[0] = sample
				heap.Fix(&acc.samples, 0)
				totalSampleBytes = replacementBytes
			}
		}
	}
	model.MissingDSP = max(0, model.CorpusTracks-model.DSPTracks)
	groupIDs := make([]string, 0, len(groups))
	for id := range groups {
		groupIDs = append(groupIDs, id)
	}
	sort.Strings(groupIDs)
	for _, id := range groupIDs {
		acc := groups[id]
		group := DSPStatisticsGroup{ID: id, Contract: acc.contract, Tracks: acc.tracks}
		for _, name := range DSPFeatureNames {
			feature := acc.features[name]
			stats := DSPFeatureStatistics{Name: name, Known: feature.known, Missing: feature.missing, Partial: feature.partial, SampleCount: len(feature.samples), Approximate: feature.known > len(feature.samples)}
			reasons := make([]string, 0, len(feature.reasons))
			for reason := range feature.reasons {
				reasons = append(reasons, reason)
			}
			sort.Strings(reasons)
			for _, reason := range reasons {
				stats.MissingReasons = append(stats.MissingReasons, DSPReasonCount{Reason: reason, Count: feature.reasons[reason]})
			}
			for _, sample := range feature.samples {
				stats.Breakpoints = append(stats.Breakpoints, sample.value)
			}
			sort.Float64s(stats.Breakpoints)
			if len(stats.Breakpoints) > 0 {
				stats.Quantiles = &DSPQuantiles{P05: type7(stats.Breakpoints, .05), P25: type7(stats.Breakpoints, .25), P50: type7(stats.Breakpoints, .5), P75: type7(stats.Breakpoints, .75), P95: type7(stats.Breakpoints, .95)}
			}
			group.Features = append(group.Features, stats)
		}
		model.Groups = append(model.Groups, group)
	}
	identity := model
	identity.Generation = ""
	raw, _ := json.Marshal(identity)
	sum := sha256.Sum256(raw)
	model.Generation = "dspstats-" + hex.EncodeToString(sum[:12])
	return model, nil
}

// Percentile returns the empirical midrank within the retained deterministic
// sample. ok=false preserves missing/incompatible evidence explicitly.
func (m DSPStatisticsModel) Percentile(groupID, feature string, value float64) (float64, bool) {
	groupIndex := sort.Search(len(m.Groups), func(i int) bool { return m.Groups[i].ID >= groupID })
	if groupIndex == len(m.Groups) || m.Groups[groupIndex].ID != groupID || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	features := m.Groups[groupIndex].Features
	featureIndex := -1
	for i := range features {
		if features[i].Name == feature {
			featureIndex = i
			break
		}
	}
	if featureIndex < 0 || len(features[featureIndex].Breakpoints) == 0 {
		return 0, false
	}
	values := features[featureIndex].Breakpoints
	left := sort.SearchFloat64s(values, value)
	right := sort.Search(len(values), func(i int) bool { return values[i] > value })
	return (float64(left) + float64(right)) / (2 * float64(len(values))), true
}

func dspGroupID(contract DSPContract) string {
	raw, _ := json.Marshal(contract)
	sum := sha256.Sum256(raw)
	return "dsp-" + hex.EncodeToString(sum[:12])
}

func dspPriority(seed uint64, group, feature, track string) [32]byte {
	h := sha256.New()
	var raw [8]byte
	binary.LittleEndian.PutUint64(raw[:], seed)
	_, _ = h.Write([]byte(DSPStatisticsVersion))
	_, _ = h.Write(raw[:])
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(group))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(feature))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(track))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func sampleLess(left, right dspSample) bool {
	cmp := bytes.Compare(left.priority[:], right.priority[:])
	return cmp < 0 || cmp == 0 && left.trackID < right.trackID
}

func validDSPFeature(name string) bool {
	for _, candidate := range DSPFeatureNames {
		if name == candidate {
			return true
		}
	}
	return false
}

func type7(values []float64, probability float64) float64 {
	if len(values) == 1 {
		return values[0]
	}
	position := probability * float64(len(values)-1)
	lower := int(math.Floor(position))
	weight := position - float64(lower)
	return values[lower]*(1-weight) + values[min(lower+1, len(values)-1)]*weight
}
