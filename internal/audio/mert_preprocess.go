package audio

import (
	"container/list"
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

const MERTSampleRate = 24000
const MERTSegmentSamples = 5 * MERTSampleRate
const MERTDimension = 768
const MERTPreprocessingVersion = "mono-sinc64-24k-segments5s-zmuv-eps1e-7-pad0/v1"
const MERTPoolingVersion = "layer12-masked-mean-l2;duration-weighted-segment-mean-l2/v1"
const MERTRevision = "12af15fef9d0ac838c3f475bfbbf26d2060dd4f5"
const MERTLocalPreprocessingVersion = "mono-polyphase-sinc64-24k-segments5s-zmuv-eps1e-7-pad0-source8to192k-float/v3"

const (
	mertLocalMaximumRate       = 192000
	mertLocalFilterRadius      = 64
	mertLocalFilterTaps        = 2 * mertLocalFilterRadius
	mertLocalMaximumPhases     = 4096
	mertLocalCoefficientBudget = 32 << 20
)

// MERTLocalMetrics records local-indexing decisions derived while validating
// and downmixing source PCM. Keeping them with the resampler avoids rescanning
// large interleaved decoder buffers before preprocessing.
type MERTLocalMetrics struct {
	SignalVariance           float64
	HasSignal                bool
	DownmixCancellationRatio float64
	DownmixDuration          time.Duration
	ResampleDuration         time.Duration
}

// MERTResample uses a windowed low-pass sinc before downsampling. It returns
// owned mono PCM; the caller clears it. DSP retains the original channel power.
func MERTResample(ctx context.Context, pcm DecodedPCM) ([]float32, error) {
	return mertResample(ctx, pcm, 96000)
}

// MERTResampleLocal extends the established sinc64 contract to validated local
// float PCM through 192 kHz. Its distinct identity prevents old preview-cache
// parity from being inferred solely from the shared 768-dimensional output.
func MERTResampleLocal(ctx context.Context, pcm DecodedPCM) ([]float32, error) {
	resampled, _, err := MERTResampleLocalWithMetrics(ctx, pcm)
	return resampled, err
}

// MERTResampleLocalWithMetrics applies the local-library resampling contract
// and returns measurements made during its single source-PCM pass. It returns
// owned mono PCM; the caller clears it.
func MERTResampleLocalWithMetrics(ctx context.Context, pcm DecodedPCM) ([]float32, MERTLocalMetrics, error) {
	var metrics MERTLocalMetrics
	if pcm.SampleRate < 8000 || pcm.SampleRate > mertLocalMaximumRate || pcm.Channels < 1 || pcm.Channels > 8 || len(pcm.Samples)%pcm.Channels != 0 || len(pcm.Samples) == 0 || len(pcm.Samples)/pcm.Channels > pcm.SampleRate*MaxPreviewSeconds {
		return nil, metrics, fmt.Errorf("audio: invalid MERT source PCM")
	}
	downmixStarted := time.Now()
	frames := len(pcm.Samples) / pcm.Channels
	mono := make([]float64, frames)
	defer clear(mono)
	channels := float64(pcm.Channels)
	var sum, squares, channelPower, monoPower float64
	for frame := range mono {
		if frame%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, metrics, err
			}
		}
		var signalMono float64
		for channel := 0; channel < pcm.Channels; channel++ {
			x := float64(pcm.Samples[frame*pcm.Channels+channel])
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return nil, metrics, fmt.Errorf("audio: nonfinite PCM")
			}
			mono[frame] += x / channels
			signalMono += x
			channelPower += x * x / channels
		}
		signalMono /= channels
		sum += signalMono
		squares += signalMono * signalMono
		monoPower += mono[frame] * mono[frame]
	}
	mean := sum / float64(frames)
	metrics.SignalVariance = squares/float64(frames) - mean*mean
	metrics.HasSignal = frames >= 2 && metrics.SignalVariance > 1e-12
	metrics.DownmixCancellationRatio = 1
	if pcm.Channels > 1 && channelPower > 1e-16 {
		metrics.DownmixCancellationRatio = monoPower / channelPower
	}
	metrics.DownmixDuration = time.Since(downmixStarted)

	resampleStarted := time.Now()
	phaseCount, phaseStep := mertLocalPhases(pcm.SampleRate)
	var coefficients []float64
	if phaseCount <= mertLocalMaximumPhases {
		table, err := mertLocalCoefficients.get(ctx, pcm.SampleRate)
		if err != nil {
			metrics.ResampleDuration = time.Since(resampleStarted)
			return nil, metrics, err
		}
		coefficients = table.coefficients
	}
	out := make([]float32, frames*MERTSampleRate/pcm.SampleRate)
	success := false
	defer func() {
		if !success {
			clear(out)
		}
	}()
	cutoff := math.Min(1, float64(MERTSampleRate)/float64(pcm.SampleRate)) * 0.94
	sinCutoffStep, cosCutoffStep := math.Sincos(math.Pi * cutoff)
	sinWindowStep, cosWindowStep := math.Sincos(math.Pi / mertLocalFilterRadius)
	var fallback [mertLocalFilterTaps]float64
	sourceIndex, phase := 0, 0
	for i := range out {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				metrics.ResampleDuration = time.Since(resampleStarted)
				return nil, metrics, err
			}
		}
		row := fallback[:]
		if coefficients != nil {
			row = coefficients[phase*mertLocalFilterTaps : (phase+1)*mertLocalFilterTaps]
		} else {
			mertLocalRecurrenceCoefficients(row, cutoff, phase, phaseCount, sinCutoffStep, cosCutoffStep, sinWindowStep, cosWindowStep)
		}
		out[i] = float32(mertLocalDot(mono, row, sourceIndex))
		next := phase + phaseStep
		sourceIndex += next / phaseCount
		phase = next % phaseCount
	}
	metrics.ResampleDuration = time.Since(resampleStarted)
	success = true
	return out, metrics, nil
}

type mertLocalCoefficientTable struct {
	coefficients []float64
}

type mertLocalCoefficientEntry struct {
	rate  int
	ready chan struct{}
	table *mertLocalCoefficientTable
	err   error
	elem  *list.Element
}

type mertLocalCoefficientCache struct {
	mu         sync.Mutex
	maximum    int
	bytes      int
	entries    map[int]*mertLocalCoefficientEntry
	leastFirst list.List
}

func newMERTLocalCoefficientCache(maximum int) *mertLocalCoefficientCache {
	return &mertLocalCoefficientCache{maximum: maximum, entries: make(map[int]*mertLocalCoefficientEntry)}
}

var mertLocalCoefficients = newMERTLocalCoefficientCache(mertLocalCoefficientBudget)

func (c *mertLocalCoefficientCache) get(ctx context.Context, rate int) (*mertLocalCoefficientTable, error) {
	for {
		c.mu.Lock()
		if entry := c.entries[rate]; entry != nil {
			if entry.elem != nil {
				c.leastFirst.MoveToFront(entry.elem)
			}
			ready := entry.ready
			c.mu.Unlock()
			select {
			case <-ready:
				if entry.err != nil && ctx.Err() == nil {
					continue
				}
				return entry.table, entry.err
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		entry := &mertLocalCoefficientEntry{rate: rate, ready: make(chan struct{})}
		c.entries[rate] = entry
		c.mu.Unlock()

		table, err := buildMERTLocalCoefficientTable(ctx, rate)
		c.mu.Lock()
		entry.table, entry.err = table, err
		if err != nil {
			delete(c.entries, rate)
		} else {
			size := len(table.coefficients) * 8
			if size <= c.maximum {
				entry.elem = c.leastFirst.PushFront(entry)
				c.bytes += size
				for c.bytes > c.maximum {
					oldest := c.leastFirst.Back()
					oldEntry := oldest.Value.(*mertLocalCoefficientEntry)
					c.leastFirst.Remove(oldest)
					oldEntry.elem = nil
					delete(c.entries, oldEntry.rate)
					c.bytes -= len(oldEntry.table.coefficients) * 8
				}
			} else {
				delete(c.entries, rate)
			}
		}
		close(entry.ready)
		c.mu.Unlock()
		return table, err
	}
}

func buildMERTLocalCoefficientTable(ctx context.Context, rate int) (*mertLocalCoefficientTable, error) {
	phaseCount, _ := mertLocalPhases(rate)
	coefficients := make([]float64, phaseCount*mertLocalFilterTaps)
	cutoff := math.Min(1, float64(MERTSampleRate)/float64(rate)) * 0.94
	for phase := 0; phase < phaseCount; phase++ {
		if phase%32 == 0 {
			if err := ctx.Err(); err != nil {
				clear(coefficients)
				return nil, err
			}
		}
		mertLocalDirectCoefficients(coefficients[phase*mertLocalFilterTaps:(phase+1)*mertLocalFilterTaps], cutoff, phase, phaseCount)
	}
	return &mertLocalCoefficientTable{coefficients: coefficients}, nil
}

func mertLocalDirectCoefficients(coefficients []float64, cutoff float64, phase, phaseCount int) {
	fraction := float64(phase) / float64(phaseCount)
	var total float64
	for tap := range coefficients {
		distance := float64(tap-mertLocalFilterRadius+1) - fraction
		if math.Abs(distance) >= mertLocalFilterRadius {
			coefficients[tap] = 0
			continue
		}
		z := math.Pi * distance * cutoff
		sinc := 1.0
		if z != 0 {
			sinc = math.Sin(z) / z
		}
		weight := cutoff * sinc * (0.5 + 0.5*math.Cos(math.Pi*distance/mertLocalFilterRadius))
		coefficients[tap] = weight
		total += weight
	}
	for tap := range coefficients {
		coefficients[tap] /= total
	}
}

func mertLocalRecurrenceCoefficients(coefficients []float64, cutoff float64, phase, phaseCount int, sinCutoffStep, cosCutoffStep, sinWindowStep, cosWindowStep float64) {
	fraction := float64(phase) / float64(phaseCount)
	distance := float64(-mertLocalFilterRadius+1) - fraction
	sinCutoff, cosCutoff := math.Sincos(math.Pi * distance * cutoff)
	sinWindow, cosWindow := math.Sincos(math.Pi * distance / mertLocalFilterRadius)
	var total float64
	for tap := range coefficients {
		distance = float64(tap-mertLocalFilterRadius+1) - fraction
		if tap > 0 && tap%4 == 0 {
			sinCutoff, cosCutoff = math.Sincos(math.Pi * distance * cutoff)
			sinWindow, cosWindow = math.Sincos(math.Pi * distance / mertLocalFilterRadius)
		}
		if math.Abs(distance) < mertLocalFilterRadius {
			lowpass := cutoff
			if distance != 0 {
				lowpass = sinCutoff / (math.Pi * distance)
			}
			coefficients[tap] = lowpass * (0.5 + 0.5*cosWindow)
			total += coefficients[tap]
		} else {
			coefficients[tap] = 0
		}
		sinCutoff, cosCutoff = sinCutoff*cosCutoffStep+cosCutoff*sinCutoffStep, cosCutoff*cosCutoffStep-sinCutoff*sinCutoffStep
		sinWindow, cosWindow = sinWindow*cosWindowStep+cosWindow*sinWindowStep, cosWindow*cosWindowStep-sinWindow*sinWindowStep
	}
	for tap := range coefficients {
		coefficients[tap] /= total
	}
}

func mertLocalDot(mono, coefficients []float64, sourceIndex int) float64 {
	start := sourceIndex - mertLocalFilterRadius + 1
	if start >= 0 && start+len(coefficients) <= len(mono) {
		var total float64
		for tap, coefficient := range coefficients {
			total += mono[start+tap] * coefficient
		}
		return total
	}
	var total float64
	for tap, coefficient := range coefficients {
		index := max(0, min(len(mono)-1, start+tap))
		total += mono[index] * coefficient
	}
	return total
}

func mertLocalPhases(rate int) (count, step int) {
	divisor := mertLocalGCD(rate, MERTSampleRate)
	return MERTSampleRate / divisor, rate / divisor
}

func mertLocalGCD(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func mertResample(ctx context.Context, pcm DecodedPCM, maximumRate int) ([]float32, error) {
	if pcm.SampleRate < 8000 || pcm.SampleRate > maximumRate || pcm.Channels < 1 || pcm.Channels > 8 || len(pcm.Samples)%pcm.Channels != 0 || len(pcm.Samples) == 0 || len(pcm.Samples)/pcm.Channels > pcm.SampleRate*MaxPreviewSeconds {
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
