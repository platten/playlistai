package audio

import (
	"math"
	"math/cmplx"
	"testing"
)

func TestFFTMatchesDirectDiscreteTransform(t *testing.T) {
	for _, size := range []int{1, 2, 8, 16} {
		input := make([]complex128, size)
		for i := range input {
			input[i] = complex(math.Sin(float64(i)), math.Cos(float64(2*i)))
		}
		got := append([]complex128(nil), input...)
		fft(got)
		for frequency, value := range got {
			var want complex128
			for sample, amplitude := range input {
				want += amplitude * cmplx.Exp(complex(0, -2*math.Pi*float64(sample*frequency)/float64(size)))
			}
			if cmplx.Abs(value-want) > 1e-10 {
				t.Fatalf("size=%d bin=%d got=%v want=%v", size, frequency, value, want)
			}
		}
	}
}

func TestSlaneyFilterbankHasFiniteOrderedBandSupport(t *testing.T) {
	filters := slaneyFilters()
	if len(filters) != (FFTSize/2+1)*MelBins {
		t.Fatal("unexpected filterbank tensor shape")
	}
	previousPeak := -1
	for mel := range MelBins {
		peak, maximum := -1, 0.0
		for bin := 0; bin <= FFTSize/2; bin++ {
			value := filters[bin*MelBins+mel]
			frequency := float64(bin) * SampleRate / FFTSize
			if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) || (frequency <= 50 || frequency >= 14000) && value != 0 {
				t.Fatalf("invalid filter support mel=%d bin=%d value=%v", mel, bin, value)
			}
			if value > maximum {
				peak, maximum = bin, value
			}
		}
		if maximum == 0 || peak < previousPeak {
			t.Fatalf("empty or reversed mel band %d peak=%d previous=%d", mel, peak, previousPeak)
		}
		previousPeak = peak
	}
}

func TestLogMelSilenceShapeAndAmplitudeScaling(t *testing.T) {
	if _, err := LogMel(nil); err == nil {
		t.Fatal("invalid PCM shape accepted")
	}
	samples := make([]float32, SegmentSamples)
	silence, err := LogMel(samples)
	if err != nil || len(silence) != MelBins*MelFrames {
		t.Fatalf("silence tensor shape: %d %v", len(silence), err)
	}
	for index, value := range silence {
		if value != -100 {
			t.Fatalf("silence exceeded -100dB floor at %d: %v", index, value)
		}
	}
	for index := range samples {
		samples[index] = float32(.25 * math.Sin(2*math.Pi*440*float64(index)/SampleRate))
	}
	quiet, err := LogMel(samples)
	if err != nil {
		t.Fatal(err)
	}
	for index := range samples {
		samples[index] *= 2
	}
	loud, err := LogMel(samples)
	if err != nil {
		t.Fatal(err)
	}
	peak := 0
	for index := range MelBins {
		if quiet[500*MelBins+index] > quiet[500*MelBins+peak] {
			peak = index
		}
	}
	for _, frame := range []int{0, 1, 500, 999, 1000} {
		index := frame*MelBins + peak
		if delta := float64(loud[index] - quiet[index]); math.Abs(delta-20*math.Log10(2)) > .001 {
			t.Fatalf("amplitude scaling changed at reflected/interior frame %d: %v dB", frame, delta)
		}
	}
}
