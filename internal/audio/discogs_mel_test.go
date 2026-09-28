package audio

import (
	"math"
	"testing"
)

func TestDiscogsMelPatchesMatchEssentia(t *testing.T) {
	samples := make([]float32, 8*discogsRate)
	for i := range samples {
		samples[i] = float32(.25 * math.Sin(2*math.Pi*440*float64(i)/discogsRate))
	}
	patches, count, err := DiscogsMelPatches(samples)
	if err != nil || count != 7 {
		t.Fatalf("patches=%d error=%v", count, err)
	}
	// Reference: Essentia 2.1b6 TensorflowInputMusiCNN with the official
	// EffNet 512/256 framing, on the same generated 16 kHz sine wave.
	for _, sample := range []struct {
		frame, band int
		want        float64
	}{
		{0, 0, 2.8288767}, {0, 10, 3.6381783}, {0, 40, 1.037096},
		{1, 10, .5427268}, {50, 20, .0131083},
		{372, 10, .5427},
	} {
		got := float64(patches[sample.frame*discogsBands+sample.band])
		if math.Abs(got-sample.want) > .001 {
			t.Errorf("frame %d band %d: got %.7f want %.7f", sample.frame, sample.band, got, sample.want)
		}
	}
	if _, _, err := DiscogsMelPatches(samples[:discogsRate]); err == nil {
		t.Fatal("short audio was accepted")
	}
}
