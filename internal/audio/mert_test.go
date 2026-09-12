package audio

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

type mertFake struct {
	model    core.AudioRepresentationIdentity
	calls    int
	borrowed []float32
	invalid  bool
	cancel   context.CancelFunc
}

func (m *mertFake) Identity() core.AudioRepresentationIdentity { return m.model }
func (m *mertFake) EmbedAudio(ctx context.Context, pcm []float32) ([]float32, error) {
	m.calls++
	m.borrowed = pcm
	if m.cancel != nil {
		m.cancel()
		return nil, ctx.Err()
	}
	v := make([]float32, MERTDimension)
	v[0] = 1
	if m.invalid {
		v[0] = float32(math.NaN())
	}
	return v, nil
}
func mertTestModel() core.AudioRepresentationIdentity {
	return core.AudioRepresentationIdentity{Model: "m-a-p/MERT-v1-95M", Revision: MERTRevision, Preprocessing: MERTPreprocessingVersion, Runtime: "onnxruntime/1.26.0/cpu", Dimension: MERTDimension, WeightsSHA256: strings.Repeat("a", 64), Pooling: MERTPoolingVersion}
}
func TestMERTSharedFetchCacheAndCleanup(t *testing.T) {
	for _, clap := range []bool{false, true} {
		t.Run(map[bool]string{false: "model-independent", true: "with-clap"}[clap], func(t *testing.T) {
			preview, _, resolver, _ := testService(t)
			store := preview.Store.(*Store)
			preview.DSPStore = store.DSP()
			if !clap {
				preview.Analyzer = nil
			}
			fake := &mertFake{model: mertTestModel()}
			s := &MERTService{Preview: preview, Analyzer: fake, Store: store.Representations(), ParityValidated: true}
			ref := core.TrackRef{ID: "123", Artist: "Synthetic", Title: "Silence"}
			d, r, n, err := s.AnalyzeEnhancedPreview(context.Background(), ref, "catalog")
			if err != nil || n == 0 || d.ID == "" || r.ID == "" || resolver.calls != 1 {
				t.Fatalf("shared fetch failed: n=%d calls=%d err=%v", n, resolver.calls, err)
			}
			for _, x := range fake.borrowed {
				if x != 0 {
					t.Fatal("MERT samples retained")
				}
			}
			_, again, n, err := s.AnalyzeEnhancedPreview(context.Background(), ref, "catalog")
			if err != nil || n != 0 || again.ID != r.ID || resolver.calls != 1 {
				t.Fatal("compatible cache missed", err)
			}
			oldCalls := fake.calls
			fake.model.WeightsSHA256 = strings.Repeat("b", 64)
			_, changed, _, err := s.AnalyzeEnhancedPreview(context.Background(), ref, "catalog")
			if err != nil || changed.Model == r.Model || fake.calls <= oldCalls {
				t.Fatal("model cache identity ignored", err)
			}
			preview.Authorized = false
			if _, _, err := s.AnalyzePreview(context.Background(), ref, "catalog"); err == nil {
				t.Fatal("authorization ignored")
			}
		})
	}
}
func TestMERTInvalidOutputAndCancellationDoNotCache(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		preview, _, _, _ := testService(t)
		preview.Analyzer = nil
		store := preview.Store.(*Store)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		fake := &mertFake{model: mertTestModel(), invalid: !canceled}
		if canceled {
			fake.cancel = cancel
		}
		s := &MERTService{Preview: preview, Analyzer: fake, Store: store.Representations(), ParityValidated: true}
		_, _, err := s.AnalyzePreview(ctx, core.TrackRef{ID: "123", Artist: "Synthetic", Title: "Silence"}, "catalog")
		if err == nil || canceled && !errors.Is(err, context.Canceled) {
			t.Fatal("invalid output/cancel accepted", err)
		}
		usage, err := s.Store.Usage(context.Background())
		if err != nil || usage.Records != 0 {
			t.Fatal("failed analysis cached", err)
		}
	}
}
func TestMERTPreprocessingMasksPaddingAndRejectsInvalid(t *testing.T) {
	input, mask, err := MERTInput([]float32{1, 2})
	if err == nil || input != nil || mask != nil {
		t.Fatal("too-short input accepted")
	}
	samples := make([]float32, 1000)
	for i := range samples {
		samples[i] = float32(i % 2)
	}
	input, mask, err = MERTInput(samples)
	if err != nil {
		t.Fatal(err)
	}
	for i := range input {
		if i < len(samples) {
			if mask[i] != 1 || math.Abs(math.Abs(float64(input[i]))-1) > 1e-5 {
				t.Fatal("normalization mismatch")
			}
		} else if mask[i] != 0 || input[i] != 0 {
			t.Fatal("padding was included")
		}
	}
	samples[0] = float32(math.Inf(1))
	if _, _, err = MERTInput(samples); err == nil {
		t.Fatal("nonfinite input accepted")
	}
}
func TestMERTResamplerRejectsAliasingAndCancels(t *testing.T) {
	energy := func(hz float64) float64 {
		t.Helper()
		pcm := DecodedPCM{Samples: make([]float32, 48000), SampleRate: 48000, Channels: 1}
		for i := range pcm.Samples {
			pcm.Samples[i] = float32(math.Sin(2 * math.Pi * hz * float64(i) / 48000))
		}
		out, err := MERTResample(context.Background(), pcm)
		if err != nil {
			t.Fatal(err)
		}
		var total float64
		for _, x := range out[100 : len(out)-100] {
			total += float64(x) * float64(x)
		}
		return total / float64(len(out)-200)
	}
	if energy(1000) < 0.49 || energy(18000) > 0.00001 {
		t.Fatal("resampler fails passband or antialias rejection")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := MERTResample(ctx, DecodedPCM{Samples: make([]float32, 48000), SampleRate: 48000, Channels: 1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestMERTWorkerCanceledWaitAndInvalidLength(t *testing.T) {
	w := &MERTWorker{}
	w.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.EmbedAudio(ctx, make([]float32, 400)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	w.mu.Unlock()
	if _, err := w.EmbedAudio(context.Background(), make([]float32, 399)); err == nil {
		t.Fatal("invalid length accepted")
	}
}

func BenchmarkMERTResampleFiveSeconds(b *testing.B) {
	pcm := DecodedPCM{Samples: make([]float32, 5*48000*2), SampleRate: 48000, Channels: 2}
	for i := 0; i < len(pcm.Samples)/2; i++ {
		x := float32(0.1 * math.Sin(2*math.Pi*440*float64(i)/48000))
		pcm.Samples[2*i] = x
		pcm.Samples[2*i+1] = x
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		samples, err := MERTResample(context.Background(), pcm)
		if err != nil {
			b.Fatal(err)
		}
		clear(samples)
	}
}

func TestOptionalMERTFailurePreservesCLAP(t *testing.T) {
	preview, _, _, _ := testService(t)
	store := preview.Store.(*Store)
	fake := &mertFake{model: mertTestModel(), invalid: true}
	mert := &MERTService{Preview: preview, Analyzer: fake, Store: store.Representations(), ParityValidated: true}
	preview.MERT = mert
	r, _, err := preview.AnalyzePreview(context.Background(), core.TrackRef{ID: "123", Artist: "Synthetic", Title: "Silence"}, "catalog")
	if err != nil || r.ID == "" {
		t.Fatal("optional MERT error discarded CLAP", err)
	}
	usage, err := mert.Store.Usage(context.Background())
	if err != nil || usage.Records != 0 {
		t.Fatal("invalid MERT cached", err)
	}
}
