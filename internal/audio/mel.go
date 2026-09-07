package audio

import (
	"fmt"
	"math"
)

const FFTSize = 1024
const HopSize = 480
const MelBins = 64
const MelFrames = 1001

// LogMel matches ClapFeatureExtractor's non-fusion Slaney filterbank, periodic
// Hann window, centered reflection padding, power spectrum, and dB floor.
// Input and output are borrowed/owned transient tensors respectively.
func LogMel(samples []float32) ([]float32, error) {
	if len(samples) != SegmentSamples {
		return nil, fmt.Errorf("audio: expected ten seconds at 48 kHz")
	}
	filters := slaneyFilters()
	window := make([]float64, FFTSize)
	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/FFTSize)
	}
	work := make([]complex128, FFTSize)
	defer clear(work)
	power := make([]float64, FFTSize/2+1)
	defer clear(power)
	out := make([]float32, MelFrames*MelBins)
	for frame := 0; frame < MelFrames; frame++ {
		for i := range work {
			index := frame*HopSize + i - FFTSize/2
			if index < 0 {
				index = -index
			}
			if index >= len(samples) {
				index = 2*len(samples) - 2 - index
			}
			work[i] = complex(float64(samples[index])*window[i], 0)
		}
		fft(work)
		for i := range power {
			re, im := real(work[i]), imag(work[i])
			power[i] = re*re + im*im
		}
		for mel := 0; mel < MelBins; mel++ {
			sum := 0.0
			for bin, p := range power {
				sum += p * filters[bin*MelBins+mel]
			}
			out[frame*MelBins+mel] = float32(10 * math.Log10(math.Max(1e-10, sum)))
		}
	}
	return out, nil
}

func slaneyFilters() []float64 {
	toMel := func(hz float64) float64 {
		if hz < 1000 {
			return hz / (200.0 / 3)
		}
		return 15 + math.Log(hz/1000)/(math.Log(6.4)/27)
	}
	toHz := func(mel float64) float64 {
		if mel < 15 {
			return mel * (200.0 / 3)
		}
		return 1000 * math.Exp((mel-15)*(math.Log(6.4)/27))
	}
	low, high := toMel(50), toMel(14000)
	edges := make([]float64, MelBins+2)
	for i := range edges {
		edges[i] = toHz(low + (high-low)*float64(i)/float64(MelBins+1))
	}
	filters := make([]float64, (FFTSize/2+1)*MelBins)
	for bin := 0; bin <= FFTSize/2; bin++ {
		hz := float64(bin) * SampleRate / FFTSize
		for mel := 0; mel < MelBins; mel++ {
			left := (hz - edges[mel]) / (edges[mel+1] - edges[mel])
			right := (edges[mel+2] - hz) / (edges[mel+2] - edges[mel+1])
			filters[bin*MelBins+mel] = math.Max(0, math.Min(left, right)) * 2 / (edges[mel+2] - edges[mel])
		}
	}
	return filters
}

func fft(values []complex128) {
	n := len(values)
	j := 0
	for i := 1; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			values[i], values[j] = values[j], values[i]
		}
	}
	for width := 2; width <= n; width *= 2 {
		angle := -2 * math.Pi / float64(width)
		step := complex(math.Cos(angle), math.Sin(angle))
		for start := 0; start < n; start += width {
			twiddle := complex(1, 0)
			for k := 0; k < width/2; k++ {
				even := values[start+k]
				odd := values[start+k+width/2] * twiddle
				values[start+k] = even + odd
				values[start+k+width/2] = even - odd
				twiddle *= step
			}
		}
	}
}
