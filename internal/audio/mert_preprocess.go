package audio

import (
	"context"
	"fmt"
	"math"
)

const MERTSampleRate = 24000
const MERTSegmentSamples = 5 * MERTSampleRate
const MERTDimension = 768
const MERTPreprocessingVersion = "mono-sinc64-24k-segments5s-zmuv-eps1e-7-pad0/v1"
const MERTPoolingVersion = "layer12-masked-mean-l2;duration-weighted-segment-mean-l2/v1"
const MERTRevision = "12af15fef9d0ac838c3f475bfbbf26d2060dd4f5"

// MERTResample uses a windowed low-pass sinc before downsampling. It returns
// owned mono PCM; the caller clears it. DSP retains the original channel power.
func MERTResample(ctx context.Context, pcm DecodedPCM) ([]float32, error) {
	if pcm.SampleRate < 8000 || pcm.SampleRate > 96000 || pcm.Channels < 1 || pcm.Channels > 8 || len(pcm.Samples)%pcm.Channels != 0 || len(pcm.Samples) == 0 || len(pcm.Samples)/pcm.Channels > pcm.SampleRate*MaxPreviewSeconds {
		return nil, fmt.Errorf("audio: invalid MERT source PCM")
	}
	frames := len(pcm.Samples) / pcm.Channels
	mono := make([]float64, frames)
	defer clear(mono)
	for i := range mono {
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		for c := 0; c < pcm.Channels; c++ {
			x := float64(pcm.Samples[i*pcm.Channels+c])
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return nil, fmt.Errorf("audio: nonfinite PCM")
			}
			mono[i] += x / float64(pcm.Channels)
		}
	}
	out := make([]float32, frames*MERTSampleRate/pcm.SampleRate)
	success := false
	defer func() {
		if !success {
			clear(out)
		}
	}()
	cutoff := math.Min(1, float64(MERTSampleRate)/float64(pcm.SampleRate)) * 0.94
	for i := range out {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		pos := float64(i) * float64(pcm.SampleRate) / MERTSampleRate
		var total, weight float64
		for j := int(math.Floor(pos)) - 63; j <= int(math.Floor(pos))+64; j++ {
			d := float64(j) - pos
			if math.Abs(d) >= 64 {
				continue
			}
			z := math.Pi * d * cutoff
			sinc := 1.0
			if z != 0 {
				sinc = math.Sin(z) / z
			}
			w := cutoff * sinc * (0.5 + 0.5*math.Cos(math.Pi*d/64))
			// Clamped edges preserve DC without introducing undefined source frames.
			total += mono[max(0, min(frames-1, j))] * w
			weight += w
		}
		out[i] = float32(total / weight)
	}
	success = true
	return out, nil
}

// MERTInput normalizes observed samples only, then pads. The mask excludes all
// padded samples from the transformer's attention and temporal pooling.
func MERTInput(samples []float32) ([]float32, []int64, error) {
	if len(samples) < 400 || len(samples) > MERTSegmentSamples {
		return nil, nil, fmt.Errorf("audio: invalid MERT input length")
	}
	var mean, variance float64
	for _, x := range samples {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return nil, nil, fmt.Errorf("audio: nonfinite MERT input")
		}
		mean += float64(x)
	}
	mean /= float64(len(samples))
	for _, x := range samples {
		d := float64(x) - mean
		variance += d * d
	}
	scale := math.Sqrt(variance/float64(len(samples)) + 1e-7)
	input := make([]float32, MERTSegmentSamples)
	mask := make([]int64, MERTSegmentSamples)
	for i, x := range samples {
		input[i] = float32((float64(x) - mean) / scale)
		mask[i] = 1
	}
	return input, mask, nil
}
