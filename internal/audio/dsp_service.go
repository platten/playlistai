package audio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/platten/playlistai/internal/core"
)

const OriginalPCMVersion = "go-mp3/0.3.4-s16-stereo-original-rate/v1"

// DSPAnalysisVersion invalidates only DSP when extraction, decode or sampling
// changes. Ranking and neural model versions do not participate in this cache.
const DSPAnalysisVersion = DSPVersion + ";" + OriginalPCMVersion + ";" + PreviewSamplingVersion

// AnalyzeDSPPreview is a bounded, explicit model-independent analysis operation.
// Authorization remains necessary; installing a neural model is not required.
// Cached results are returned without resolving, fetching or decoding again.
func (s *Service) AnalyzeDSPPreview(ctx context.Context, ref core.TrackRef, catalog string) (core.DSPAnalysis, int64, error) {
	if s == nil || !s.Authorized || s.Resolver == nil || s.DSPStore == nil {
		return core.DSPAnalysis{}, 0, fmt.Errorf("audio: DSP store and provider authorization required")
	}
	if err := ctx.Err(); err != nil {
		return core.DSPAnalysis{}, 0, err
	}
	if cached, ok, err := s.DSPStore.Find(ctx, catalog, ref.ID, core.ProvisionalRecordingKey(ref), DSPAnalysisVersion); err != nil || ok {
		return cached, 0, err
	}
	var enriched core.EnrichedTrack
	if s.Recordings != nil {
		enriched, _ = s.Recordings.CachedRecording(ref)
	}
	preview, err := s.Resolver.ResolveAudioPreview(ctx, ref, enriched)
	if err != nil {
		return core.DSPAnalysis{}, 0, err
	}
	if preview.Identity.Status != core.ResolutionResolved || preview.Identity.Provider != "deezer" || preview.URL == "" {
		return core.DSPAnalysis{}, 0, fmt.Errorf("audio: preview identity is unresolved, ambiguous, or unavailable")
	}
	encoded, err := s.fetch(ctx, preview.URL)
	defer clear(encoded)
	n := int64(len(encoded))
	if err != nil {
		return core.DSPAnalysis{}, n, err
	}
	hash := sha256.Sum256(encoded)
	original, err := DecodeOriginalMP3(ctx, encoded)
	clear(encoded)
	defer clear(original.Samples)
	if err != nil {
		return core.DSPAnalysis{}, n, err
	}
	// Use the established interval policy without resampling PCM for DSP.
	frames48k := (len(original.Samples) / original.Channels) * SampleRate / original.SampleRate
	first, last := randomEvidenceBounds(frames48k)
	a, err := s.storeDSPInterval(ctx, ref, catalog, preview.Identity, hex.EncodeToString(hash[:]), original, first, last)
	return a, n, err
}

func decodeForAnalysis(ctx context.Context, encoded []byte, withDSP bool) ([]float32, DecodedPCM, error) {
	if !withDSP {
		samples, err := DecodeMP3(ctx, encoded)
		return samples, DecodedPCM{}, err
	}
	original, err := DecodeOriginalMP3(ctx, encoded)
	if err != nil {
		return nil, original, err
	}
	samples, err := clapFromOriginal(ctx, original)
	return samples, original, err
}

// first/last are CLAP-frame coordinates; choose contained whole source frames.
// Coverage records those actual source boundaries, excluding any model padding.
func (s *Service) storeDSPInterval(ctx context.Context, ref core.TrackRef, catalog string, identity core.PreviewIdentity, audioHash string, original DecodedPCM, first, last int) (core.DSPAnalysis, error) {
	// A CLAP upgrade must not replace independently cached DSP or randomly
	// select another DSP interval. Reuse only verified identical preview bytes
	// and identity; the cached record retains its own explicit coverage.
	cached, ok, err := s.DSPStore.Find(ctx, catalog, ref.ID, core.ProvisionalRecordingKey(ref), DSPAnalysisVersion)
	if err != nil {
		return core.DSPAnalysis{}, err
	}
	if ok && cached.AudioSHA256 == audioHash && Fingerprint(cached.Identity) == Fingerprint(identity) {
		return cached, nil
	}
	start := (first*original.SampleRate + SampleRate - 1) / SampleRate
	end := min(last*original.SampleRate/SampleRate, len(original.Samples)/original.Channels)
	if end <= start {
		return core.DSPAnalysis{}, fmt.Errorf("audio: no observed source frames in DSP interval")
	}
	interval := original
	interval.Samples = original.Samples[start*original.Channels : end*original.Channels]
	features, err := MeasureDSP(ctx, interval)
	if err != nil {
		return core.DSPAnalysis{}, err
	}
	a := core.DSPAnalysis{
		TrackID: ref.ID, CatalogVersion: catalog, TrackKey: core.ProvisionalRecordingKey(ref),
		Identity: identity, AudioSHA256: audioHash, Version: DSPAnalysisVersion,
		SampleRate: original.SampleRate, Channels: original.Channels, Features: features,
		Coverage: core.PreviewCoverage{Available: true, Source: identity.Provider,
			StartSeconds: float64(start) / float64(original.SampleRate), EndSeconds: float64(end) / float64(original.SampleRate),
			CoveredSeconds: float64(end-start) / float64(original.SampleRate)},
		AnalyzedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := ctx.Err(); err != nil {
		return core.DSPAnalysis{}, err
	}
	a.ID = Fingerprint(a)
	if err := s.DSPStore.Put(ctx, a); err != nil {
		return core.DSPAnalysis{}, err
	}
	return a, nil
}
