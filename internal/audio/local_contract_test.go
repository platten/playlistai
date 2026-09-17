package audio

import (
	"context"
	"math"
	"testing"
)

func TestLocalContractsAccept192kAndFloatHeadroomWithoutChangingPreviewContract(t *testing.T) {
	pcm := DecodedPCM{SampleRate: 192000, Channels: 2, Samples: make([]float32, 192000*2)}
	for frame := range 192000 {
		value := float32(1.1 * math.Sin(2*math.Pi*1000*float64(frame)/192000))
		pcm.Samples[frame*2], pcm.Samples[frame*2+1] = value, -value
	}
	defer clear(pcm.Samples)
	if _, err := MeasureDSP(context.Background(), pcm); err == nil {
		t.Fatal("legacy preview DSP accepted the extended local contract")
	}
	features, err := MeasureLocalDSP(context.Background(), pcm)
	if err != nil || features.RMSDBFS.Value == nil || features.SamplePeakDBFS.Value == nil || *features.SamplePeakDBFS.Value <= 0 {
		t.Fatalf("local DSP rejected float headroom at 192 kHz: %+v %v", features, err)
	}
	if _, err := MERTResample(context.Background(), pcm); err == nil {
		t.Fatal("legacy preview MERT resampler accepted 192 kHz")
	}
	resampled, err := MERTResampleLocal(context.Background(), pcm)
	if err != nil || len(resampled) != MERTSampleRate {
		t.Fatalf("local MERT preprocessing: samples=%d err=%v", len(resampled), err)
	}
	clear(resampled)
}
