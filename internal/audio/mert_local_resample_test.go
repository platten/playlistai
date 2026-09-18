package audio

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
)

func TestMERTResampleLocalMatchesScalarContract(t *testing.T) {
	rates := []int{8000, 22050, 24000, 32000, 44100, 48000, 88200, 96000, 176400, 192000}
	for _, rate := range rates {
		for _, channels := range []int{1, 2, 8} {
			t.Run(testRateChannelsName(rate, channels), func(t *testing.T) {
				frames := max(257, rate/100)
				pcm := DecodedPCM{Samples: make([]float32, frames*channels), SampleRate: rate, Channels: channels}
				var state uint32 = 734
				for frame := range frames {
					for channel := range channels {
						state = state*1664525 + 1013904223
						noise := float64(int32(state)) / (1 << 31)
						value := 0.31*math.Sin(2*math.Pi*997*float64(frame)/float64(rate)) +
							0.08*math.Sin(2*math.Pi*3503*float64(frame)/float64(rate)+float64(channel)*0.19) + 0.002*noise
						pcm.Samples[frame*channels+channel] = float32(value)
					}
				}
				assertLocalMatchesScalar(t, pcm, 1e-5)
			})
		}
	}
}

func TestMERTResampleLocalSignalsAndEdges(t *testing.T) {
	const rate = 48000
	const frames = 960
	tests := map[string]func(int) (float32, float32){
		"silence": func(int) (float32, float32) { return 0, 0 },
		"dc":      func(int) (float32, float32) { return 0.25, 0.25 },
		"tone": func(frame int) (float32, float32) {
			x := float32(0.4 * math.Sin(2*math.Pi*440*float64(frame)/rate))
			return x, x
		},
		"anti-phase": func(frame int) (float32, float32) {
			x := float32(0.4 * math.Sin(2*math.Pi*440*float64(frame)/rate))
			return x, -x
		},
		"impulse-start": func(frame int) (float32, float32) {
			if frame == 0 {
				return 0.8, -0.2
			}
			return 0, 0
		},
		"impulse-middle": func(frame int) (float32, float32) {
			if frame == frames/2 {
				return 0.8, -0.2
			}
			return 0, 0
		},
		"impulse-end": func(frame int) (float32, float32) {
			if frame == frames-1 {
				return 0.8, -0.2
			}
			return 0, 0
		},
		"noise": func(frame int) (float32, float32) {
			x := uint32(frame+1)*747796405 + 2891336453
			x = ((x >> ((x >> 28) + 4)) ^ x) * 277803737
			value := float32(int32((x>>22)^x)) / (1 << 31)
			return value * 0.2, value * -0.07
		},
	}
	for name, signal := range tests {
		t.Run(name, func(t *testing.T) {
			pcm := DecodedPCM{Samples: make([]float32, frames*2), SampleRate: rate, Channels: 2}
			for frame := range frames {
				pcm.Samples[frame*2], pcm.Samples[frame*2+1] = signal(frame)
			}
			out, metrics, err := MERTResampleLocalWithMetrics(context.Background(), pcm)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(out)
			if metrics.DownmixDuration <= 0 || metrics.ResampleDuration <= 0 {
				t.Fatalf("missing stage timings: %+v", metrics)
			}
			want := scalarMERTResampleLocal(pcm)
			defer clear(want)
			assertWaveformClose(t, out, want, 1e-5)
			switch name {
			case "silence", "dc", "anti-phase":
				if metrics.HasSignal {
					t.Fatalf("degenerate downmix passed signal gate: %+v", metrics)
				}
			case "tone", "noise", "impulse-start", "impulse-middle", "impulse-end":
				if !metrics.HasSignal {
					t.Fatalf("varying downmix failed signal gate: %+v", metrics)
				}
			}
			if name == "anti-phase" {
				if metrics.DownmixCancellationRatio != 0 {
					t.Fatalf("anti-phase cancellation ratio=%g", metrics.DownmixCancellationRatio)
				}
				for i, sample := range out {
					if sample != 0 {
						t.Fatalf("anti-phase output[%d]=%g", i, sample)
					}
				}
			}
		})
	}

	for _, frames := range []int{1, 2, 63, 64, 65, 127} {
		t.Run("short-"+testIntegerName(frames), func(t *testing.T) {
			pcm := DecodedPCM{Samples: make([]float32, frames*8), SampleRate: 192000, Channels: 8}
			for i := range pcm.Samples {
				pcm.Samples[i] = float32((i%17)-8) / 16
			}
			assertLocalMatchesScalar(t, pcm, 1e-5)
		})
	}
}

func TestMERTResampleLocalPreservesDC(t *testing.T) {
	for _, rate := range []int{8000, 22050, 24000, 32000, 44100, 48000, 88200, 96000, 176400, 192000} {
		pcm := DecodedPCM{Samples: make([]float32, max(257, rate/100)*8), SampleRate: rate, Channels: 8}
		for i := range pcm.Samples {
			pcm.Samples[i] = 0.25
		}
		out, err := MERTResampleLocal(context.Background(), pcm)
		if err != nil {
			t.Fatal(err)
		}
		for i, sample := range out {
			if math.Abs(float64(sample)-0.25) > 1e-7 {
				t.Fatalf("rate=%d sample[%d]=%g", rate, i, sample)
			}
		}
		clear(out)
	}
}

func TestMERTResampleLocalPassbandAndAliasing(t *testing.T) {
	energy := func(hz float64) float64 {
		pcm := DecodedPCM{Samples: make([]float32, 48000), SampleRate: 48000, Channels: 1}
		for i := range pcm.Samples {
			pcm.Samples[i] = float32(math.Sin(2 * math.Pi * hz * float64(i) / 48000))
		}
		out, err := MERTResampleLocal(context.Background(), pcm)
		if err != nil {
			t.Fatal(err)
		}
		defer clear(out)
		var total float64
		for _, sample := range out[100 : len(out)-100] {
			total += float64(sample) * float64(sample)
		}
		return total / float64(len(out)-200)
	}
	passband := energy(1000)
	if passband < 0.49 {
		t.Fatalf("passband energy=%g", passband)
	}
	if ratio := energy(18000) / passband; ratio >= 0.001 {
		t.Fatalf("out-of-band energy ratio=%g", ratio)
	}
}

func TestMERTResampleLocalFallbackMatchesScalar(t *testing.T) {
	const rate = 47999 // coprime with 24 kHz: 24,000 phases exercises fallback.
	phaseCount, _ := mertLocalPhases(rate)
	if phaseCount <= mertLocalMaximumPhases {
		t.Fatalf("fixture has only %d phases", phaseCount)
	}
	pcm := DecodedPCM{Samples: make([]float32, rate/50*2), SampleRate: rate, Channels: 2}
	for frame := 0; frame < len(pcm.Samples)/2; frame++ {
		pcm.Samples[frame*2] = float32(0.3 * math.Sin(2*math.Pi*431*float64(frame)/rate))
		pcm.Samples[frame*2+1] = float32(0.2 * math.Cos(2*math.Pi*1703*float64(frame)/rate))
	}
	assertLocalMatchesScalar(t, pcm, 1e-5)
}

func TestMERTLocalRecurrenceCoefficientsMatchDirect(t *testing.T) {
	const rate = 47999
	phaseCount, _ := mertLocalPhases(rate)
	cutoff := math.Min(1, float64(MERTSampleRate)/rate) * 0.94
	sinCutoffStep, cosCutoffStep := math.Sincos(math.Pi * cutoff)
	sinWindowStep, cosWindowStep := math.Sincos(math.Pi / mertLocalFilterRadius)
	for phase := 0; phase < phaseCount; phase++ {
		var got, want [mertLocalFilterTaps]float64
		mertLocalRecurrenceCoefficients(got[:], cutoff, phase, phaseCount, sinCutoffStep, cosCutoffStep, sinWindowStep, cosWindowStep)
		mertLocalDirectCoefficients(want[:], cutoff, phase, phaseCount)
		for tap := range got {
			if difference := math.Abs(got[tap] - want[tap]); difference > 1e-12 {
				t.Fatalf("phase=%d tap=%d recurrence=%g direct=%g difference=%g", phase, tap, got[tap], want[tap], difference)
			}
		}
	}
}

func TestMERTResampleLocalRejectsNonfiniteAndCancels(t *testing.T) {
	valid := DecodedPCM{Samples: make([]float32, 48000), SampleRate: 48000, Channels: 1}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, _, err := MERTResampleLocalWithMetrics(ctx, valid); !errors.Is(err, context.Canceled) || out != nil {
		t.Fatalf("canceled resample returned samples=%d err=%v", len(out), err)
	}
	for _, nonfinite := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		pcm := DecodedPCM{Samples: []float32{0, nonfinite, 0, 0}, SampleRate: 48000, Channels: 2}
		if out, _, err := MERTResampleLocalWithMetrics(context.Background(), pcm); err == nil || out != nil {
			t.Fatalf("nonfinite %g accepted", nonfinite)
		}
	}
	for _, pcm := range []DecodedPCM{
		{},
		{Samples: []float32{0}, SampleRate: 7999, Channels: 1},
		{Samples: []float32{0}, SampleRate: 192001, Channels: 1},
		{Samples: []float32{0}, SampleRate: 48000, Channels: 0},
		{Samples: make([]float32, 9), SampleRate: 48000, Channels: 9},
		{Samples: make([]float32, 3), SampleRate: 48000, Channels: 2},
	} {
		if out, _, err := MERTResampleLocalWithMetrics(context.Background(), pcm); err == nil || out != nil {
			t.Fatalf("invalid PCM accepted: rate=%d channels=%d samples=%d", pcm.SampleRate, pcm.Channels, len(pcm.Samples))
		}
	}

	buildCtx := &cancelAfterChecks{Context: context.Background(), after: 3}
	if _, err := buildMERTLocalCoefficientTable(buildCtx, 48006); !errors.Is(err, context.Canceled) {
		t.Fatalf("coefficient build cancellation=%v", err)
	}
}

func TestMERTLocalCoefficientCacheConcurrentAndBounded(t *testing.T) {
	cache := newMERTLocalCoefficientCache(200000)
	const callers = 16
	start := make(chan struct{})
	tables := make([]*mertLocalCoefficientTable, callers)
	errs := make([]error, callers)
	var group sync.WaitGroup
	for i := range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			tables[i], errs[i] = cache.get(context.Background(), 22050)
		}()
	}
	close(start)
	group.Wait()
	for i := range callers {
		if errs[i] != nil || tables[i] != tables[0] {
			t.Fatalf("caller %d table=%p err=%v; first=%p", i, tables[i], errs[i], tables[0])
		}
	}
	for _, rate := range []int{44100, 88200, 176400} {
		if _, err := cache.get(context.Background(), rate); err != nil {
			t.Fatal(err)
		}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.bytes > cache.maximum {
		t.Fatalf("coefficient cache retained %d bytes, maximum %d", cache.bytes, cache.maximum)
	}
	if cache.entries[22050] != nil {
		t.Fatal("least-recently-used coefficient table was not evicted")
	}
}

func TestMERTLocalCoefficientCacheDropsCanceledBuild(t *testing.T) {
	cache := newMERTLocalCoefficientCache(1 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cache.get(ctx, 22050); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cache build=%v", err)
	}
	cache.mu.Lock()
	entries := len(cache.entries)
	cache.mu.Unlock()
	if entries != 0 {
		t.Fatalf("canceled build retained %d entries", entries)
	}
	if _, err := cache.get(context.Background(), 22050); err != nil {
		t.Fatalf("cache did not recover after cancellation: %v", err)
	}
}

func BenchmarkMERTResampleLocalFiveSeconds(b *testing.B) {
	pcm := DecodedPCM{Samples: make([]float32, 5*48000*2), SampleRate: 48000, Channels: 2}
	for i := 0; i < len(pcm.Samples)/2; i++ {
		x := float32(0.1 * math.Sin(2*math.Pi*440*float64(i)/48000))
		pcm.Samples[2*i], pcm.Samples[2*i+1] = x, x
	}
	warm, err := MERTResampleLocal(context.Background(), pcm)
	if err != nil {
		b.Fatal(err)
	}
	clear(warm)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := MERTResampleLocal(context.Background(), pcm)
		if err != nil {
			b.Fatal(err)
		}
		clear(out)
	}
}

func assertLocalMatchesScalar(t *testing.T, pcm DecodedPCM, tolerance float64) {
	t.Helper()
	got, metrics, err := MERTResampleLocalWithMetrics(context.Background(), pcm)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(got)
	want := scalarMERTResampleLocal(pcm)
	defer clear(want)
	assertWaveformClose(t, got, want, tolerance)
	wantVariance, wantSignal, wantRatio := scalarMERTLocalMetrics(pcm)
	if metrics.SignalVariance != wantVariance || metrics.HasSignal != wantSignal || metrics.DownmixCancellationRatio != wantRatio {
		t.Fatalf("metrics=%+v want variance=%g signal=%t ratio=%g", metrics, wantVariance, wantSignal, wantRatio)
	}
}

func assertWaveformClose(t *testing.T, got, want []float32, tolerance float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("samples=%d want=%d", len(got), len(want))
	}
	var maximum float64
	for i := range got {
		difference := math.Abs(float64(got[i] - want[i]))
		maximum = math.Max(maximum, difference)
		if difference > tolerance {
			t.Fatalf("sample[%d]=%g want=%g difference=%g maximum=%g", i, got[i], want[i], difference, maximum)
		}
	}
}

// scalarMERTResampleLocal freezes the local v2 kernel independently of the
// production phase table and recurrence implementations.
func scalarMERTResampleLocal(pcm DecodedPCM) []float32 {
	frames := len(pcm.Samples) / pcm.Channels
	mono := make([]float64, frames)
	defer clear(mono)
	for i := range mono {
		for channel := 0; channel < pcm.Channels; channel++ {
			mono[i] += float64(pcm.Samples[i*pcm.Channels+channel]) / float64(pcm.Channels)
		}
	}
	out := make([]float32, frames*MERTSampleRate/pcm.SampleRate)
	cutoff := math.Min(1, float64(MERTSampleRate)/float64(pcm.SampleRate)) * 0.94
	for i := range out {
		position := float64(i) * float64(pcm.SampleRate) / MERTSampleRate
		var total, weight float64
		for source := int(math.Floor(position)) - 63; source <= int(math.Floor(position))+64; source++ {
			distance := float64(source) - position
			if math.Abs(distance) >= 64 {
				continue
			}
			z := math.Pi * distance * cutoff
			sinc := 1.0
			if z != 0 {
				sinc = math.Sin(z) / z
			}
			coefficient := cutoff * sinc * (0.5 + 0.5*math.Cos(math.Pi*distance/64))
			total += mono[max(0, min(frames-1, source))] * coefficient
			weight += coefficient
		}
		out[i] = float32(total / weight)
	}
	return out
}

func scalarMERTLocalMetrics(pcm DecodedPCM) (variance float64, signal bool, ratio float64) {
	frames := len(pcm.Samples) / pcm.Channels
	var sum, squares float64
	for frame := range frames {
		var mono float64
		for channel := range pcm.Channels {
			mono += float64(pcm.Samples[frame*pcm.Channels+channel])
		}
		mono /= float64(pcm.Channels)
		sum += mono
		squares += mono * mono
	}
	mean := sum / float64(frames)
	variance = squares/float64(frames) - mean*mean
	signal = frames >= 2 && variance > 1e-12
	if pcm.Channels <= 1 {
		return variance, signal, 1
	}
	var channelPower, monoPower float64
	for frame := range frames {
		var mono float64
		for channel := range pcm.Channels {
			value := float64(pcm.Samples[frame*pcm.Channels+channel])
			channelPower += value * value / float64(pcm.Channels)
			mono += value / float64(pcm.Channels)
		}
		monoPower += mono * mono
	}
	if channelPower <= 1e-16 {
		return variance, signal, 1
	}
	return variance, signal, monoPower / channelPower
}

type cancelAfterChecks struct {
	context.Context
	after  int32
	checks atomic.Int32
}

func (c *cancelAfterChecks) Err() error {
	if c.checks.Add(1) >= c.after {
		return context.Canceled
	}
	return nil
}

func testRateChannelsName(rate, channels int) string {
	return testIntegerName(rate) + "hz-" + testIntegerName(channels) + "ch"
}

func testIntegerName(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[position:])
}
