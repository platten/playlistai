package audio

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/platten/playlistai/internal/core"
)

// DSPVersion changes whenever any measurement algorithm or constant changes.
// Its complete specification is docs/dsp-measurements.md.
const DSPVersion = "dsp-original-channelpower-hann-pow2ge100ms-hopquarter-band20to12000-rms400ms-p95p10-fluxpower-mad3-refractory100ms/v1"

const dspSilencePower = 1e-12

func dspKnown(value float64) core.DSPValue {
	return core.DSPValue{Value: &value, State: core.FeatureKnown}
}

func dspUnknown(reason string) core.DSPValue {
	return core.DSPValue{State: core.FeatureUnknown, Reason: reason}
}

func dspUnavailable(reason string) core.DSPFeatures {
	u := dspUnknown(reason)
	return core.DSPFeatures{RMSDBFS: u, SamplePeakDBFS: u, CrestFactorDB: u,
		RMSWindowSpreadDB: u, SubbassEnergyRatio: u, BassEnergyRatio: u,
		TrebleEnergyRatio: u, SpectralCentroidHz: u, PositiveSpectralFlux: u, OnsetRateHz: u}
}

func dspPowerDB(power float64) float64 { return 10 * math.Log10(math.Max(power, 1e-16)) }

// MeasureDSP borrows normalized, original-rate interleaved PCM without modifying
// it. It measures channel powers separately, preserving anti-phase stereo. All
// allocated audio and spectrum buffers are cleared before return.
func MeasureDSP(ctx context.Context, pcm DecodedPCM) (core.DSPFeatures, error) {
	if err := ctx.Err(); err != nil {
		return core.DSPFeatures{}, err
	}
	if pcm.SampleRate < 8000 || pcm.SampleRate > 96000 || pcm.Channels < 1 || pcm.Channels > 8 ||
		len(pcm.Samples) == 0 || len(pcm.Samples)%pcm.Channels != 0 ||
		len(pcm.Samples)/pcm.Channels > pcm.SampleRate*MaxPreviewSeconds {
		return core.DSPFeatures{}, fmt.Errorf("audio: invalid DSP PCM dimensions")
	}
	frames := len(pcm.Samples) / pcm.Channels
	windowFrames := pcm.SampleRate * 2 / 5
	windows := make([]float64, 0, frames/windowFrames)
	defer func() { clear(windows) }()
	var power, peak, windowPower float64
	for i, sample := range pcm.Samples {
		if i%16384 == 0 {
			if err := ctx.Err(); err != nil {
				return core.DSPFeatures{}, err
			}
		}
		x := float64(sample)
		if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > 1 {
			return core.DSPFeatures{}, fmt.Errorf("audio: invalid DSP PCM sample")
		}
		power += x * x
		windowPower += x * x
		peak = math.Max(peak, math.Abs(x))
		if (i+1)%(windowFrames*pcm.Channels) == 0 {
			windows = append(windows, dspPowerDB(windowPower/float64(windowFrames*pcm.Channels)))
			windowPower = 0
		}
	}
	if frames < windowFrames {
		return dspUnavailable("insufficient_duration"), nil
	}
	meanPower := power / float64(len(pcm.Samples))
	result := dspUnavailable("silent_audio")
	result.RMSDBFS = dspKnown(dspPowerDB(meanPower))
	result.SamplePeakDBFS = dspKnown(dspPowerDB(peak * peak))
	if meanPower <= dspSilencePower {
		return result, nil
	}
	result.CrestFactorDB = dspKnown(math.Max(0, dspPowerDB(peak*peak)-dspPowerDB(meanPower)))
	result.RMSWindowSpreadDB = dspUnknown("insufficient_rms_windows")
	if len(windows) >= 2 {
		sort.Float64s(windows)
		result.RMSWindowSpreadDB = dspKnown(dspQuantile(windows, 0.95) - dspQuantile(windows, 0.10))
	}
	if pcm.SampleRate < 24000 {
		u := dspUnknown("insufficient_spectral_bandwidth")
		result.SubbassEnergyRatio, result.BassEnergyRatio, result.TrebleEnergyRatio = u, u, u
		result.SpectralCentroidHz, result.PositiveSpectralFlux, result.OnsetRateHz = u, u, u
		return result, nil
	}
	return dspSpectrum(ctx, pcm, result)
}

func dspQuantile(sorted []float64, q float64) float64 {
	position := q * float64(len(sorted)-1)
	lower := int(position)
	upper := min(lower+1, len(sorted)-1)
	return sorted[lower] + (sorted[upper]-sorted[lower])*(position-float64(lower))
}

func dspSpectrum(ctx context.Context, pcm DecodedPCM, result core.DSPFeatures) (core.DSPFeatures, error) {
	n := 1
	for n*10 < pcm.SampleRate {
		n *= 2
	}
	hop := n / 4
	window := make([]float64, n)
	var windowPower float64
	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n))
		windowPower += window[i] * window[i]
	}
	work := make([]complex128, n)
	defer clear(work)
	powers, previous := make([]float64, n/2+1), make([]float64, n/2+1)
	defer clear(powers)
	defer clear(previous)
	frames := len(pcm.Samples) / pcm.Channels
	flux := make([]float64, 0, (frames-n)/hop+1)
	defer func() { clear(flux) }()
	var total, subbass, bass, treble, weighted float64
	for start := 0; start+n <= frames; start += hop {
		if err := ctx.Err(); err != nil {
			return core.DSPFeatures{}, err
		}
		clear(powers)
		for channel := 0; channel < pcm.Channels; channel++ {
			for i := range work {
				work[i] = complex(float64(pcm.Samples[(start+i)*pcm.Channels+channel])*window[i], 0)
			}
			fft(work)
			for bin := range powers {
				factor := 2.0
				if bin == 0 || bin == n/2 {
					factor = 1
				}
				re, im := real(work[bin]), imag(work[bin])
				powers[bin] += factor * (re*re + im*im) / (float64(n*pcm.Channels) * windowPower)
			}
		}
		var positive float64
		for bin, power := range powers {
			hz := float64(bin*pcm.SampleRate) / float64(n)
			if hz < 20 || hz > 12000 {
				continue
			}
			total += power
			weighted += hz * power
			if hz < 80 {
				subbass += power
			}
			if hz < 250 {
				bass += power
			}
			if hz >= 4000 {
				treble += power
			}
			if start != 0 {
				positive += math.Max(0, power-previous[bin])
			}
		}
		copy(previous, powers)
		flux = append(flux, positive)
	}
	if total/float64(len(flux)) <= dspSilencePower {
		u := dspUnknown("insufficient_band_energy")
		result.SubbassEnergyRatio, result.BassEnergyRatio, result.TrebleEnergyRatio = u, u, u
		result.SpectralCentroidHz, result.PositiveSpectralFlux, result.OnsetRateHz = u, u, u
		return result, nil
	}
	result.SubbassEnergyRatio, result.BassEnergyRatio = dspKnown(subbass/total), dspKnown(bass/total)
	result.TrebleEnergyRatio, result.SpectralCentroidHz = dspKnown(treble/total), dspKnown(weighted/total)
	var sum float64
	for _, f := range flux[1:] {
		sum += f
	}
	result.PositiveSpectralFlux = dspKnown(sum / float64(len(flux)-1))
	result.OnsetRateHz = dspKnown(dspOnsetRate(flux, float64(hop)/float64(pcm.SampleRate), float64(frames)/float64(pcm.SampleRate)))
	return result, nil
}

func dspOnsetRate(flux []float64, hopSeconds, duration float64) float64 {
	values := append([]float64(nil), flux[1:]...)
	defer clear(values)
	sort.Float64s(values)
	median := dspQuantile(values, 0.5)
	for i := range values {
		values[i] = math.Abs(values[i] - median)
	}
	sort.Float64s(values)
	threshold := math.Max(1e-6, median+3*dspQuantile(values, 0.5))
	count, last := 0, -1e9
	for i := 1; i+1 < len(flux); i++ {
		time := float64(i) * hopSeconds
		if flux[i] > threshold && flux[i] > flux[i-1] && flux[i] >= flux[i+1] && time-last >= 0.1 {
			count++
			last = time
		}
	}
	return float64(count) / duration
}
