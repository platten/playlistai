package audio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// MERTService shares the authorized preview resolver and cache connection with
// CLAP/DSP, but has its own strictly audio-only identity and installation gate.
type MERTService struct {
	Preview         *Service
	Analyzer        ports.AudioRepresentationAnalyzer
	Store           ports.AudioRepresentationStore
	ParityValidated bool
}

func (s *MERTService) Ready() bool {
	return s != nil && s.Preview != nil && s.Preview.Authorized && s.Preview.Resolver != nil && s.Analyzer != nil && s.Store != nil && s.ParityValidated && s.Analyzer.Identity().Preprocessing == MERTPreprocessingVersion && s.Analyzer.Identity().Dimension == MERTDimension
}

func (s *MERTService) AnalyzePreview(ctx context.Context, ref core.TrackRef, catalog string) (core.AudioRepresentation, int64, error) {
	_, r, n, err := s.AnalyzeEnhancedPreview(ctx, ref, catalog)
	return r, n, err
}

// AnalyzeEnhancedPreview performs at most one preview download and original
// decode for missing DSP/MERT evidence. An installed CLAP analyzer also shares
// that decode. No network or PCM is required when both independent caches hit.
func (s *MERTService) AnalyzeEnhancedPreview(ctx context.Context, ref core.TrackRef, catalog string) (core.DSPAnalysis, core.AudioRepresentation, int64, error) {
	var dsp core.DSPAnalysis
	var r core.AudioRepresentation
	if !s.Ready() {
		return dsp, r, 0, fmt.Errorf("audio: verified MERT and preview authorization required")
	}
	if err := ctx.Err(); err != nil {
		return dsp, r, 0, err
	}
	key := core.ProvisionalRecordingKey(ref)
	r, hit, err := s.Store.Find(ctx, catalog, ref.ID, key, s.Analyzer.Identity())
	if err != nil {
		return dsp, r, 0, err
	}
	dspHit := s.Preview.DSPStore == nil
	if s.Preview.DSPStore != nil {
		dsp, dspHit, err = s.Preview.DSPStore.Find(ctx, catalog, ref.ID, key, DSPAnalysisVersion)
		if err != nil {
			return dsp, r, 0, err
		}
	}
	if hit && dspHit {
		return dsp, r, 0, nil
	}
	if s.Preview.InferenceReady() {
		shared := *s.Preview
		shared.MERT = s
		_, n, err := shared.AnalyzePreview(ctx, ref, catalog)
		if err != nil {
			return dsp, r, n, err
		}
		r, hit, err = s.Store.Find(ctx, catalog, ref.ID, key, s.Analyzer.Identity())
		if err != nil {
			return dsp, r, n, err
		}
		if !hit {
			return dsp, r, n, fmt.Errorf("audio: MERT representation unavailable")
		}
		if s.Preview.DSPStore != nil {
			dsp, _, err = s.Preview.DSPStore.Find(ctx, catalog, ref.ID, key, DSPAnalysisVersion)
		}
		return dsp, r, n, err
	}
	var enriched core.EnrichedTrack
	if s.Preview.Recordings != nil {
		enriched, _ = s.Preview.Recordings.CachedRecording(ref)
	}
	preview, err := s.Preview.Resolver.ResolveAudioPreview(ctx, ref, enriched)
	if err != nil {
		return dsp, r, 0, err
	}
	if preview.Identity.Status != core.ResolutionResolved || preview.Identity.Provider != "deezer" || preview.URL == "" {
		return dsp, r, 0, fmt.Errorf("audio: verified preview identity required")
	}
	encoded, err := s.Preview.fetch(ctx, preview.URL)
	defer clear(encoded)
	n := int64(len(encoded))
	if err != nil {
		return dsp, r, n, err
	}
	hash := sha256.Sum256(encoded)
	audioHash := hex.EncodeToString(hash[:])
	pcm, err := DecodeOriginalMP3(ctx, encoded)
	clear(encoded)
	defer clear(pcm.Samples)
	if err != nil {
		return dsp, r, n, err
	}
	if s.Preview.DSPStore != nil {
		frames := len(pcm.Samples) / pcm.Channels * SampleRate / pcm.SampleRate
		first, last := randomEvidenceBounds(frames)
		dsp, err = s.Preview.storeDSPInterval(ctx, ref, catalog, preview.Identity, audioHash, pcm, first, last)
		if err != nil {
			return dsp, r, n, err
		}
	}
	r, err = s.AnalyzeDecoded(ctx, ref, catalog, preview.Identity, audioHash, pcm)
	return dsp, r, n, err
}

// AnalyzeDecoded borrows original PCM and never retains it. Matching cached
// preview identity and encoded hash reuse their original observed coverage.
func (s *MERTService) AnalyzeDecoded(ctx context.Context, ref core.TrackRef, catalog string, identity core.PreviewIdentity, audioHash string, pcm DecodedPCM) (core.AudioRepresentation, error) {
	var r core.AudioRepresentation
	if !s.Ready() {
		return r, fmt.Errorf("audio: MERT unavailable")
	}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	cached, hit, err := s.Store.Find(ctx, catalog, ref.ID, core.ProvisionalRecordingKey(ref), s.Analyzer.Identity())
	if err != nil {
		return r, err
	}
	if hit && cached.AudioSHA256 == audioHash && Fingerprint(cached.Identity) == Fingerprint(identity) {
		return cached, nil
	}
	samples, err := MERTResample(ctx, pcm)
	if err != nil {
		return r, err
	}
	defer clear(samples)
	// Limit preview analysis to 30 observed seconds. A sub-convolution tail is
	// omitted and never reported as covered time.
	end := min(len(samples), 30*MERTSampleRate)
	if end%MERTSegmentSamples > 0 && end%MERTSegmentSamples < 400 {
		end -= end % MERTSegmentSamples
	}
	if end < 400 {
		return r, fmt.Errorf("audio: insufficient observed MERT samples")
	}
	r = core.AudioRepresentation{TrackID: ref.ID, CatalogVersion: catalog, TrackKey: core.ProvisionalRecordingKey(ref), Identity: identity, AudioSHA256: audioHash, Model: s.Analyzer.Identity(), AnalyzedAt: time.Now().UTC().Format(time.RFC3339Nano), Coverage: core.PreviewCoverage{Available: true, Source: identity.Provider, EndSeconds: float64(end) / MERTSampleRate, CoveredSeconds: float64(end) / MERTSampleRate}}
	sums := make([]float64, MERTDimension)
	for start := 0; start < end; start += MERTSegmentSamples {
		if err := ctx.Err(); err != nil {
			return core.AudioRepresentation{}, err
		}
		last := min(start+MERTSegmentSamples, end)
		vector, err := s.Analyzer.EmbedAudio(ctx, samples[start:last])
		if err != nil {
			return core.AudioRepresentation{}, err
		}
		if !MERTParity(vector, vector) {
			clear(vector)
			return core.AudioRepresentation{}, fmt.Errorf("audio: invalid MERT output")
		}
		owned := append([]float32(nil), vector...)
		clear(vector)
		r.Segments = append(r.Segments, core.AudioRepresentationSegment{StartSeconds: float64(start) / MERTSampleRate, EndSeconds: float64(last) / MERTSampleRate, Vector: owned})
		for j, x := range owned {
			sums[j] += float64(x) * float64(last-start)
		}
	}
	var norm float64
	for _, x := range sums {
		norm += x * x
	}
	if norm <= 1e-12 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return core.AudioRepresentation{}, fmt.Errorf("audio: degenerate pooled MERT output")
	}
	r.Pooled = make([]float32, MERTDimension)
	for j, x := range sums {
		r.Pooled[j] = float32(x / math.Sqrt(norm))
	}
	if err := ctx.Err(); err != nil {
		return core.AudioRepresentation{}, err
	}
	r.ID = Fingerprint(r)
	if err := s.Store.Put(ctx, r); err != nil {
		return core.AudioRepresentation{}, err
	}
	return r, nil
}
