package librarylearn

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

type dspSliceSource struct {
	rows  []DSPTrack
	index int
}

func (s *dspSliceSource) Next(ctx context.Context) (DSPTrack, bool, error) {
	if err := ctx.Err(); err != nil {
		return DSPTrack{}, false, err
	}
	if s.index == len(s.rows) {
		return DSPTrack{}, false, nil
	}
	row := s.rows[s.index]
	s.index++
	return row, true, nil
}

func dspNumber(value float64) *float64 { return &value }

func TestDSPStatisticsSeparatesContractsAndPreservesMissingness(t *testing.T) {
	a := DSPContract{Version: "dsp/v1", Sampling: "balanced/v1", Scope: "sampled_windows"}
	b := DSPContract{Version: "dsp/v2", Sampling: "balanced/v1", Scope: "sampled_windows"}
	rows := []DSPTrack{
		{TrackID: "a", Contract: a, Features: []DSPFeatureValue{{Name: "rms_dbfs", Value: dspNumber(-20)}, {Name: "bass_energy_ratio", MissingReason: "insufficient_bandwidth"}}},
		{TrackID: "b", Contract: a, Features: []DSPFeatureValue{{Name: "rms_dbfs", Value: dspNumber(-10), Partial: true}, {Name: "bass_energy_ratio", Value: dspNumber(.4)}}},
		{TrackID: "c", Contract: b, Features: []DSPFeatureValue{{Name: "rms_dbfs", Value: dspNumber(-2)}}},
	}
	model, err := BuildDSPStatistics(context.Background(), &dspSliceSource{rows: rows}, DSPStatisticsOptions{Seed: 9, Workers: 4, MaxSamplesPerFeature: 8, MaxScratchBytes: 1 << 20, CorpusTracks: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Groups) != 2 || model.DSPTracks != 3 || model.MissingDSP != 2 || model.Generation == "" {
		t.Fatalf("model=%+v", model)
	}
	var first *DSPStatisticsGroup
	for i := range model.Groups {
		if model.Groups[i].Contract == a {
			first = &model.Groups[i]
		}
	}
	if first == nil {
		t.Fatal("compatible group missing")
	}
	var rms, bass DSPFeatureStatistics
	for _, feature := range first.Features {
		switch feature.Name {
		case "rms_dbfs":
			rms = feature
		case "bass_energy_ratio":
			bass = feature
		}
	}
	if rms.Known != 2 || rms.Partial != 1 || rms.Quantiles == nil || rms.Quantiles.P50 != -15 {
		t.Fatalf("rms=%+v", rms)
	}
	if bass.Known != 1 || bass.Missing != 1 || len(bass.MissingReasons) != 1 || bass.MissingReasons[0].Reason != "insufficient_bandwidth" {
		t.Fatalf("bass=%+v", bass)
	}
	if percentile, ok := model.Percentile(first.ID, "rms_dbfs", -15); !ok || percentile != .5 {
		t.Fatalf("percentile=%v ok=%v", percentile, ok)
	}
	if _, ok := model.Percentile(first.ID, "onset_rate_hz", 1); ok {
		t.Fatal("missing evidence produced a percentile")
	}
}

func TestDSPStatisticsWorkerInvariantAndBoundedSample(t *testing.T) {
	contract := DSPContract{Version: "dsp/v1", Sampling: "deep/v1", Scope: "sampled_windows"}
	rows := make([]DSPTrack, 100)
	for i := range rows {
		rows[i] = DSPTrack{TrackID: string(rune('a'+i/26)) + string(rune('a'+i%26)), Contract: contract, Features: []DSPFeatureValue{{Name: "rms_dbfs", Value: dspNumber(float64(i))}}}
	}
	build := func(workers int) DSPStatisticsModel {
		model, err := BuildDSPStatistics(context.Background(), &dspSliceSource{rows: rows}, DSPStatisticsOptions{Seed: 42, Workers: workers, MaxSamplesPerFeature: 7, MaxScratchBytes: 1 << 20, CorpusTracks: len(rows)})
		if err != nil {
			t.Fatal(err)
		}
		return model
	}
	one, four := build(1), build(4)
	a, _ := json.Marshal(one)
	b, _ := json.Marshal(four)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("worker count changed DSP statistics")
	}
	feature := one.Groups[0].Features[0]
	if feature.SampleCount != 7 || !feature.Approximate || len(feature.Breakpoints) != 7 {
		t.Fatalf("feature=%+v", feature)
	}
}

func TestDSPStatisticsRejectsScratchOverflow(t *testing.T) {
	contract := DSPContract{Version: "dsp/v1", Sampling: "fast/v1", Scope: "sampled_windows"}
	rows := []DSPTrack{{TrackID: "a", Contract: contract, Features: []DSPFeatureValue{{Name: "rms_dbfs", Value: dspNumber(-10)}}}}
	if _, err := BuildDSPStatistics(context.Background(), &dspSliceSource{rows: rows}, DSPStatisticsOptions{MaxSamplesPerFeature: 1, MaxScratchBytes: 79, CorpusTracks: 1}); err == nil {
		t.Fatal("scratch overflow accepted")
	}
}
