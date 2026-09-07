package audio

import (
	"encoding/json"
	"fmt"
	"os"
)

// Preprocessing is compiled Go code. The downloaded configuration must describe
// that exact contract, rather than silently changing its interpretation.
type preprocessingConfig struct {
	Version          string  `json:"version"`
	SamplingRate     int     `json:"samplingRate"`
	SegmentSamples   int     `json:"segmentSamples"`
	FFTSize          int     `json:"fftSize"`
	HopSize          int     `json:"hopSize"`
	MelBins          int     `json:"melBins"`
	MinimumFrequency int     `json:"minimumFrequency"`
	MaximumFrequency int     `json:"maximumFrequency"`
	MelScale         string  `json:"melScale"`
	Padding          string  `json:"padding"`
	Floor            float64 `json:"floor"`
}

func CheckPreprocessing(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var config preprocessingConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return err
	}
	want := preprocessingConfig{Version: PreprocessingVersion, SamplingRate: SampleRate, SegmentSamples: SegmentSamples, FFTSize: 1024, HopSize: 480, MelBins: MelBins, MinimumFrequency: 50, MaximumFrequency: 14000, MelScale: "slaney", Padding: "reflect", Floor: 1e-10}
	if config != want {
		return fmt.Errorf("audio: preprocessing configuration does not match compiled worker")
	}
	return nil
}
