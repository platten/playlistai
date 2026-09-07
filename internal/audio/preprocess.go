package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"

	mp3 "github.com/hajimehoshi/go-mp3"
)

const (
	SampleRate           = 48000
	SegmentSamples       = SampleRate * 10
	MaxPreviewSeconds    = 60
	MaxEncodedBytes      = 8 << 20
	PreprocessingVersion = "mp3-s16-stereo-mono-linear48k-quant32767-segments10s-repeatpad-slaney64-fft1024-hop480/v1"
)

// DecodeMP3 returns owned mono PCM. The caller clears it after use. The decoder
// and its internal state become unreachable on return; no files are created.
func DecodeMP3(ctx context.Context, encoded []byte) ([]float32, error) {
	if len(encoded) == 0 || len(encoded) > MaxEncodedBytes {
		return nil, fmt.Errorf("audio: preview size out of bounds")
	}
	decoder, err := mp3.NewDecoder(&contextReader{ctx: ctx, reader: bytes.NewReader(encoded)})
	if err != nil {
		return nil, fmt.Errorf("audio: MP3 decode failed")
	}
	rate := decoder.SampleRate()
	if rate < 8000 || rate > 96000 {
		return nil, fmt.Errorf("audio: unsupported sample rate")
	}
	maxBytes := rate * MaxPreviewSeconds * 4
	pcm, err := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, reader: decoder}, int64(maxBytes)+1))
	defer clear(pcm)
	if err != nil {
		return nil, err
	}
	if len(pcm) == 0 || len(pcm) > maxBytes || len(pcm)%4 != 0 {
		return nil, fmt.Errorf("audio: decoded duration out of bounds")
	}
	frames := len(pcm) / 4
	out := make([]float32, frames*SampleRate/rate)
	ok := false
	defer func() {
		if !ok {
			clear(out)
		}
	}()
	mono := func(i int) float64 {
		left := int16(binary.LittleEndian.Uint16(pcm[i*4:]))    //nolint:gosec // signed PCM bit pattern
		right := int16(binary.LittleEndian.Uint16(pcm[i*4+2:])) //nolint:gosec
		return (float64(left) + float64(right)) / (2 * 32768)
	}
	for i := range out {
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		position := float64(i) * float64(rate) / SampleRate
		lo := int(position)
		hi := min(lo+1, frames-1)
		fraction := position - float64(lo)
		value := mono(lo)*(1-fraction) + mono(hi)*fraction
		out[i] = float32(math.Trunc(max(-1, min(1, value))*32767) / 32767)
	}
	ok = true
	return out, nil
}

// Segment starts are deterministic, non-overlapping, and relative to the
// preview. Short final segments repeat then zero-pad, as declared by the model
// bundle; padded time is never counted as observed coverage.
func forEachSegment(ctx context.Context, samples []float32, visit func([]float32, float64, float64) error) error {
	for start := 0; start < len(samples); start += SegmentSamples {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(start+SegmentSamples, len(samples))
		segment := make([]float32, SegmentSamples)
		length := end - start
		for repeat := 0; repeat < SegmentSamples/length; repeat++ {
			copy(segment[repeat*length:], samples[start:end])
		}
		err := visit(segment, float64(start)/SampleRate, float64(end)/SampleRate)
		clear(segment)
		if err != nil {
			return err
		}
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
