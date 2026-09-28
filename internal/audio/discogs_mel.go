package audio

import (
	"context"
	"errors"
	"fmt"
	"math"
)

const (
	discogsRate     = 16000
	discogsFFT      = 512
	discogsHop      = 256
	discogsBands    = 96
	discogsPatch    = 128
	discogsPatchHop = 62
)

// DiscogsMelPatches prepares the observed 16 kHz mono audio for the official
// dynamic-batch Discogs-EffNet ONNX encoder. Its frame and patch boundaries
// match Essentia's TensorflowPredictEffnetDiscogs repeat-last-patch policy.
// The caller owns the returned tensor and must clear it after inference.
func DiscogsMelPatches(samples []float32) ([]float32, int, error) {
	if len(samples) < 3*discogsRate || len(samples) > 60*discogsRate {
		return nil, 0, errors.New("Discogs-EffNet requires 3–60 seconds of observed 16 kHz mono audio")
	}
	frames := 1 + (len(samples)-discogsFFT/2+discogsHop-1)/discogsHop
	filters := discogsFilters()
	window := make([]float64, discogsFFT)
	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(discogsFFT-1))
	}
	work := make([]complex128, discogsFFT)
	defer clear(work)
	mel := make([]float32, frames*discogsBands)
	defer clear(mel)
	for frame := 0; frame < frames; frame++ {
		start := frame*discogsHop - discogsFFT/2
		for i := range work {
			position := start + i
			if position >= 0 && position < len(samples) {
				work[i] = complex(float64(samples[position])*window[i], 0)
			} else {
				work[i] = 0
			}
		}
		fft(work)
		for band := 0; band < discogsBands; band++ {
			energy := 0.0
			for bin := 0; bin <= discogsFFT/2; bin++ {
				re, im := real(work[bin]), imag(work[bin])
				energy += (re*re + im*im) * filters[band*(discogsFFT/2+1)+bin]
			}
			mel[frame*discogsBands+band] = float32(math.Log10(1 + 10000*energy))
		}
	}
	patches := 1 + (max(0, frames-discogsPatch)+discogsPatchHop-1)/discogsPatchHop
	out := make([]float32, patches*discogsPatch*discogsBands)
	for patch := 0; patch < patches; patch++ {
		start := patch * discogsPatchHop
		available := min(discogsPatch, frames-start)
		if available <= 0 {
			return nil, 0, errors.New("Discogs-EffNet patch has no observed frames")
		}
		for frame := 0; frame < discogsPatch; frame++ {
			source := start + frame%available
			copy(out[(patch*discogsPatch+frame)*discogsBands:], mel[source*discogsBands:(source+1)*discogsBands])
		}
	}
	return out, patches, nil
}

func discogsFilters() []float64 {
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
	edges := make([]float64, discogsBands+2)
	high := toMel(discogsRate / 2)
	for i := range edges {
		edges[i] = toHz(high * float64(i) / float64(discogsBands+1))
	}
	filters := make([]float64, discogsBands*(discogsFFT/2+1))
	for band := 0; band < discogsBands; band++ {
		weight := 2 / (edges[band+2] - edges[band])
		for bin := 0; bin <= discogsFFT/2; bin++ {
			hz := float64(bin) * discogsRate / discogsFFT
			left := (hz - edges[band]) / (edges[band+1] - edges[band])
			right := (edges[band+2] - hz) / (edges[band+2] - edges[band+1])
			filters[band*(discogsFFT/2+1)+bin] = math.Max(0, math.Min(left, right)) * weight
		}
	}
	return filters
}

// DiscogsResample keeps the original observed interval and low-pass filters
// before converting it to the encoder's 16 kHz mono input. No PCM is retained.
func DiscogsResample(ctx context.Context, pcm DecodedPCM) ([]float32, error) {
	if pcm.SampleRate < 8000 || pcm.SampleRate > 192000 || pcm.Channels < 1 || pcm.Channels > 8 || len(pcm.Samples) == 0 || len(pcm.Samples)%pcm.Channels != 0 || len(pcm.Samples)/pcm.Channels > pcm.SampleRate*60 {
		return nil, fmt.Errorf("audio: invalid Discogs source PCM")
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
		for channel := 0; channel < pcm.Channels; channel++ {
			v := float64(pcm.Samples[i*pcm.Channels+channel])
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, fmt.Errorf("audio: nonfinite Discogs PCM")
			}
			mono[i] += v / float64(pcm.Channels)
		}
	}
	if pcm.SampleRate == discogsRate {
		out := make([]float32, frames)
		for i, v := range mono {
			out[i] = float32(v)
		}
		return out, nil
	}
	out := make([]float32, frames*discogsRate/pcm.SampleRate)
	cutoff := math.Min(1, float64(discogsRate)/float64(pcm.SampleRate)) * .94
	const radius = 64
	for i := range out {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				clear(out)
				return nil, err
			}
		}
		position := float64(i) * float64(pcm.SampleRate) / discogsRate
		center := int(position)
		var value, total float64
		for j := center - radius + 1; j <= center+radius; j++ {
			if j < 0 || j >= frames {
				continue
			}
			distance := position - float64(j)
			weight := cutoff
			if distance != 0 {
				weight = math.Sin(math.Pi*cutoff*distance) / (math.Pi * distance)
			}
			weight *= .5 + .5*math.Cos(math.Pi*distance/radius)
			value += mono[j] * weight
			total += weight
		}
		if total != 0 {
			out[i] = float32(value / total)
		}
	}
	return out, nil
}
