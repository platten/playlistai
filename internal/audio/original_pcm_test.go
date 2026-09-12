package audio

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"reflect"
	"testing"
)

// This reference freezes the pre-M2 int16 stereo arithmetic from bf915c4.
// It deliberately does not call the production resampling helper.
func legacyCLAPReference(pcm []int16, rate int) []float32 {
	frames := len(pcm) / 2
	out := make([]float32, frames*SampleRate/rate)
	mono := func(i int) float64 { return (float64(pcm[2*i]) + float64(pcm[2*i+1])) / (2 * 32768) }
	for i := range out {
		position := float64(i) * float64(rate) / SampleRate
		lo := int(position)
		hi := min(lo+1, frames-1)
		fraction := position - float64(lo)
		value := mono(lo)*(1-fraction) + mono(hi)*fraction
		out[i] = float32(math.Trunc(max(-1, min(1, value))*32767) / 32767)
	}
	return out
}

func TestOriginalChannelBranchPreservesCLAPBits(t *testing.T) {
	rng := rand.New(rand.NewPCG(101, 203))
	for _, rate := range []int{8000, 11025, 16000, 22050, 24000, 32000, 44100, 48000, 96000} {
		for _, frames := range []int{1, 7, 1003, rate / 3} {
			pcm16 := make([]int16, 2*frames)
			pcm := DecodedPCM{Samples: make([]float32, len(pcm16)), SampleRate: rate, Channels: 2}
			for i := range pcm16 {
				pcm16[i] = int16(rng.IntN(65536) - 32768) //nolint:gosec // bounded signed PCM fixture
				pcm.Samples[i] = float32(pcm16[i]) / 32768
			}
			pcm16[0], pcm16[1] = -32768, 32767
			pcm.Samples[0], pcm.Samples[1] = -1, float32(32767)/32768
			got, err := clapFromOriginal(context.Background(), pcm)
			if err != nil {
				t.Fatal(err)
			}
			want := legacyCLAPReference(pcm16, rate)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("CLAP bits changed at rate=%d frames=%d", rate, frames)
			}
		}
	}
}

func TestOriginalMP3SharedDecodeAndCancellation(t *testing.T) {
	encoded := syntheticMP3()
	original, err := DecodeOriginalMP3(context.Background(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(original.Samples)
	if original.SampleRate != 44100 || original.Channels != 2 || len(original.Samples) == 0 {
		t.Fatalf("source format lost: %+v", original)
	}
	legacy, err := DecodeMP3(context.Background(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := clapFromOriginal(context.Background(), original)
	if err != nil || !reflect.DeepEqual(legacy, shared) {
		t.Fatalf("shared decode changed CLAP: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := DecodeOriginalMP3(ctx, encoded); err == nil || len(got.Samples) != 0 {
		t.Fatal("canceled decoder returned PCM")
	}
	if got, err := clapFromOriginal(ctx, original); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatal("canceled preprocessing returned PCM")
	}
	if got, err := DecodeOriginalMP3(context.Background(), []byte("invalid")); err == nil || len(got.Samples) != 0 {
		t.Fatal("invalid MP3 accepted")
	}
}
