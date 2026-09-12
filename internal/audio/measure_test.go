package audio

import (
	"context"
	"math"
	"math/rand"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func dspTone(rate int, hz, amplitude, seconds float64) DecodedPCM {
	samples := make([]float32, int(float64(rate)*seconds))
	for i := range samples {
		samples[i] = float32(amplitude * math.Sin(2*math.Pi*hz*float64(i)/float64(rate)))
	}
	return DecodedPCM{Samples: samples, SampleRate: rate, Channels: 1}
}

func requireDSP(t *testing.T, pcm DecodedPCM) core.DSPFeatures {
	t.Helper()
	result, err := MeasureDSP(context.Background(), pcm)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func dspValue(t *testing.T, v core.DSPValue) float64 {
	t.Helper()
	if v.State != core.FeatureKnown || v.Value == nil || math.IsNaN(*v.Value) || math.IsInf(*v.Value, 0) {
		t.Fatalf("expected finite known value, got %+v", v)
	}
	return *v.Value
}

func TestMeasureDSPTones(t *testing.T) {
	for _, hz := range []float64{40, 100, 1000, 8000} {
		t.Run(fmtDSPHz(hz), func(t *testing.T) {
			got := requireDSP(t, dspTone(48000, hz, 0.5, 2))
			expectedCrest := 3.01029996
			if hz == 8000 {
				// Six samples per period miss the true peak by sqrt(3)/2.
				expectedCrest += 20 * math.Log10(math.Sqrt(3)/2)
			}
			if math.Abs(dspValue(t, got.RMSDBFS)-(-9.03089987)) > 0.001 || math.Abs(dspValue(t, got.CrestFactorDB)-expectedCrest) > 0.01 {
				t.Fatal("sine level or crest differs from analytic expectation")
			}
			if math.Abs(dspValue(t, got.SpectralCentroidHz)-hz) > 1 {
				t.Fatalf("centroid differs from %v Hz: %v", hz, *got.SpectralCentroidHz.Value)
			}
			if hz == 40 && dspValue(t, got.SubbassEnergyRatio) < 0.99 {
				t.Fatal("40 Hz should be subbass")
			}
			if hz <= 100 && dspValue(t, got.BassEnergyRatio) < 0.99 {
				t.Fatal("low tone should be bass")
			}
			if hz >= 1000 && dspValue(t, got.BassEnergyRatio) > 0.001 {
				t.Fatal("high tone should not be bass")
			}
			if hz == 8000 && dspValue(t, got.TrebleEnergyRatio) < 0.99 {
				t.Fatal("8 kHz should be treble")
			}
			if dspValue(t, got.OnsetRateHz) != 0 || dspValue(t, got.PositiveSpectralFlux) > 2e-5 {
				t.Fatalf("steady sine: onset=%v flux=%v", *got.OnsetRateHz.Value, *got.PositiveSpectralFlux.Value)
			}
		})
	}
}

func BenchmarkMeasureDSP(b *testing.B) {
	mono := dspTone(48000, 1000, 0.5, 30)
	pcm := DecodedPCM{SampleRate: 48000, Channels: 2, Samples: make([]float32, len(mono.Samples)*2)}
	for i, sample := range mono.Samples {
		pcm.Samples[2*i], pcm.Samples[2*i+1] = sample, -sample
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := MeasureDSP(context.Background(), pcm); err != nil {
			b.Fatal(err)
		}
	}
}

func fmtDSPHz(hz float64) string {
	switch hz {
	case 40:
		return "subbass"
	case 100:
		return "bass"
	case 1000:
		return "mid"
	default:
		return "treble"
	}
}

func TestMeasureDSPChannelPowerAndRepeatability(t *testing.T) {
	mono := dspTone(44100, 100, 0.7, 2)
	stereo := DecodedPCM{SampleRate: mono.SampleRate, Channels: 2, Samples: make([]float32, len(mono.Samples)*2)}
	for i, v := range mono.Samples {
		stereo.Samples[2*i], stereo.Samples[2*i+1] = v, -v
	}
	before := append([]float32(nil), stereo.Samples...)
	a, b := requireDSP(t, mono), requireDSP(t, stereo)
	if math.Abs(dspValue(t, a.RMSDBFS)-dspValue(t, b.RMSDBFS)) > 1e-10 ||
		math.Abs(dspValue(t, a.BassEnergyRatio)-dspValue(t, b.BassEnergyRatio)) > 1e-10 {
		t.Fatal("anti-phase channels changed power measurements")
	}
	if !reflect.DeepEqual(b, requireDSP(t, stereo)) || !reflect.DeepEqual(before, stereo.Samples) {
		t.Fatal("measurement must repeat exactly and leave caller PCM unchanged")
	}
}

func TestMeasureDSPSilenceAndInsufficientEvidence(t *testing.T) {
	silent := requireDSP(t, dspTone(48000, 100, 0, 1))
	if dspValue(t, silent.RMSDBFS) != -160 || dspValue(t, silent.SamplePeakDBFS) != -160 || silent.CrestFactorDB.State != core.FeatureUnknown || silent.BassEnergyRatio.Value != nil {
		t.Fatal("silence must have finite floor and unknown ratios")
	}
	short := requireDSP(t, dspTone(48000, 100, 0.5, 0.2))
	if short.RMSDBFS.Reason != "insufficient_duration" || short.RMSDBFS.Value != nil {
		t.Fatal("short clip must be unknown")
	}
	oneWindow := requireDSP(t, dspTone(48000, 100, 0.5, 0.6))
	if oneWindow.RMSWindowSpreadDB.Reason != "insufficient_rms_windows" {
		t.Fatal("one RMS window cannot establish a spread")
	}
	lowRate := requireDSP(t, dspTone(16000, 100, 0.5, 1))
	if lowRate.RMSDBFS.State != core.FeatureKnown || lowRate.BassEnergyRatio.Reason != "insufficient_spectral_bandwidth" || lowRate.OnsetRateHz.Value != nil {
		t.Fatal("low-rate spectrum must not use a changed denominator")
	}
	dc := dspTone(48000, 0, 0, 1)
	for i := range dc.Samples {
		dc.Samples[i] = 0.25
	}
	if requireDSP(t, dc).BassEnergyRatio.Reason != "insufficient_band_energy" {
		t.Fatal("DC signal has no eligible spectral energy")
	}
}

func TestMeasureDSPEnvelopeAndImpulseActivity(t *testing.T) {
	steady := dspTone(48000, 1000, 0.5, 4)
	envelope := dspTone(48000, 1000, 0.5, 4)
	for i := range envelope.Samples {
		if i < len(envelope.Samples)/2 {
			envelope.Samples[i] *= 0.1
		}
	}
	a, b := requireDSP(t, steady), requireDSP(t, envelope)
	if math.Abs(dspValue(t, b.RMSWindowSpreadDB)-20) > 0.001 || dspValue(t, a.RMSWindowSpreadDB) > 0.001 {
		t.Fatal("400 ms window spread should follow amplitude envelope")
	}
	impulses := dspTone(48000, 0, 0, 4)
	for i := 12000; i < len(impulses.Samples); i += 24000 {
		impulses.Samples[i] = 1
	}
	c := requireDSP(t, impulses)
	if dspValue(t, c.OnsetRateHz) < 1.5 || dspValue(t, c.OnsetRateHz) > 2.5 || dspValue(t, c.PositiveSpectralFlux) <= dspValue(t, a.PositiveSpectralFlux) || dspValue(t, c.CrestFactorDB) < 40 {
		t.Fatalf("impulses should show high crest and approximately 2 Hz activity: %+v", c)
	}
}

func TestMeasureDSPSeededNoise(t *testing.T) {
	pcm := dspTone(48000, 0, 0, 2)
	rng := rand.New(rand.NewSource(17))
	for i := range pcm.Samples {
		pcm.Samples[i] = float32((rng.Float64()*2 - 1) * 0.5)
	}
	got := requireDSP(t, pcm)
	if !reflect.DeepEqual(got, requireDSP(t, pcm)) {
		t.Fatal("seeded noise result not repeatable")
	}
	if centroid := dspValue(t, got.SpectralCentroidHz); centroid < 5500 || centroid > 6500 {
		t.Fatalf("flat noise centroid outside expected band: %v", centroid)
	}
	if ratio := dspValue(t, got.TrebleEnergyRatio); ratio < 0.60 || ratio > 0.73 {
		t.Fatalf("flat noise treble fraction outside expected band: %v", ratio)
	}
}

func TestMeasureDSPInvalidAndCanceled(t *testing.T) {
	valid := dspTone(48000, 100, 0.5, 1)
	for _, pcm := range []DecodedPCM{
		{}, {SampleRate: 48000, Channels: 0, Samples: valid.Samples},
		{SampleRate: 48000, Channels: 2, Samples: []float32{0}},
		{SampleRate: 1000, Channels: 1, Samples: valid.Samples},
		{SampleRate: 48000, Channels: 1, Samples: []float32{float32(math.NaN())}},
		{SampleRate: 48000, Channels: 1, Samples: []float32{float32(math.Inf(1))}},
		{SampleRate: 48000, Channels: 1, Samples: []float32{1.01}},
	} {
		if _, err := MeasureDSP(context.Background(), pcm); err == nil {
			t.Fatal("invalid PCM accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := MeasureDSP(ctx, valid); err != context.Canceled {
		t.Fatalf("expected cancellation: %v", err)
	}
	// One initial check and three input-loop checks precede the first FFT
	// frame for this one-second mono input; abort on the second FFT frame.
	duringFFT := &dspCancelContext{Context: context.Background(), remaining: 6}
	if _, err := MeasureDSP(duringFFT, valid); err != context.Canceled {
		t.Fatalf("expected cancellation during spectrum: %v", err)
	}
}

type dspCancelContext struct {
	context.Context
	remaining int
}

func (ctx *dspCancelContext) Err() error {
	ctx.remaining--
	if ctx.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestMeasureDSPSpectralRatesAndMinimumDuration(t *testing.T) {
	for _, rate := range []int{24000, 32000, 44100, 48000, 96000} {
		got := requireDSP(t, dspTone(rate, 1000, 0.5, 0.4))
		if math.Abs(dspValue(t, got.SpectralCentroidHz)-1000) > 1 || dspValue(t, got.OnsetRateHz) != 0 {
			t.Fatalf("minimum-duration spectral measurement failed at %v Hz", rate)
		}
	}
}

func TestMeasureDSPConstantAmplitudeCrestNonnegative(t *testing.T) {
	for _, amplitude := range []float32{0.1, 0.3, 0.7, 0.9} {
		pcm := dspTone(48000, 0, 0, 1)
		for i := range pcm.Samples {
			pcm.Samples[i] = amplitude
		}
		crest := dspValue(t, requireDSP(t, pcm).CrestFactorDB)
		if crest < 0 || crest > 1e-10 {
			t.Fatalf("constant %v PCM has invalid crest %v", amplitude, crest)
		}
	}
}
