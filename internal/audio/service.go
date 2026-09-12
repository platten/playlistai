package audio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/deezerhttp"
	"github.com/platten/playlistai/internal/ports"
)

type Policy struct {
	Version         string  `json:"version"`
	DevelopmentSet  string  `json:"developmentSet"`
	MinimumPositive float64 `json:"minimumPositive"`
	MaximumNegative float64 `json:"maximumNegative"`
}

func (p Policy) Valid() bool {
	return p.Version != "" && p.DevelopmentSet != "" && p.MinimumPositive > 0 && p.MinimumPositive < 1 && p.MaximumNegative > 0 && p.MaximumNegative < 1
}

type Service struct {
	Resolver ports.AudioPreviewResolver
	Analyzer ports.AudioAnalyzer
	Store    ports.AnalysisStore
	// DSPStore is separately opt-in. Nil preserves the existing CLAP-only path.
	DSPStore ports.DSPStore
	// MERT is opt-in on an Enhanced request's service copy only.
	MERT       *MERTService
	Recordings ports.CachedRecordingReader
	Policy     Policy
	// Authorized is a distribution-level provider permission gate, independent
	// of whether an individual listener enables network preview access.
	Authorized      bool
	ParityValidated bool
	HTTPClient      *http.Client
	// AllowPreviewURL exists for isolated fixture servers. Production leaves it
	// nil and only permits HTTPS on Deezer's preview CDN, including redirects.
	AllowPreviewURL func(*url.URL) bool
}

func (s *Service) Ready() bool {
	return s.InferenceReady() && s.Policy.Valid()
}

// InferenceReady permits preview similarity ranking and zero-shot vocal
// screening without claiming a calibrated general musical-fit policy.
func (s *Service) InferenceReady() bool {
	return s != nil && s.Authorized && s.ParityValidated && s.Resolver != nil && s.Analyzer != nil && s.Store != nil && s.Analyzer.Identity().Preprocessing == PreprocessingVersion
}

func (s *Service) ReadyFor(intent core.MusicIntent) bool {
	return s.Ready() || s.InferenceReady() && (core.WantsInstrumental(intent) || intent.VerificationPolicy == core.BestAvailable)
}

// AnalyzePreview computes reusable embeddings without making request-specific
// judgments. Calibration gates categorical musical-fit judgments separately.
func (s *Service) AnalyzePreview(ctx context.Context, ref core.TrackRef, catalog string) (core.AudioAnalysis, int64, error) {
	if s == nil || !s.Authorized || !s.ParityValidated || s.Resolver == nil || s.Analyzer == nil || s.Store == nil || s.Analyzer.Identity().Preprocessing != PreprocessingVersion {
		return core.AudioAnalysis{}, 0, fmt.Errorf("audio: verified model and provider authorization required")
	}
	var identity core.EnrichedTrack
	if s.Recordings != nil {
		identity, _ = s.Recordings.CachedRecording(ref)
	}
	preview, err := s.Resolver.ResolveAudioPreview(ctx, ref, identity)
	if err != nil {
		return core.AudioAnalysis{}, 0, err
	}
	if preview.Identity.Status != core.ResolutionResolved || preview.Identity.Provider != "deezer" || preview.URL == "" {
		return core.AudioAnalysis{Identity: preview.Identity}, 0, fmt.Errorf("audio: preview identity is unresolved, ambiguous, or unavailable")
	}
	encoded, err := s.fetch(ctx, preview.URL)
	defer clear(encoded)
	if err != nil {
		return core.AudioAnalysis{}, int64(len(encoded)), err
	}
	hash := sha256.Sum256(encoded)
	audioHash := hex.EncodeToString(hash[:])
	withEnhanced := s.DSPStore != nil || s.MERT != nil
	enhancedCtx, enhancedCancel := context.WithCancel(ctx)
	defer enhancedCancel()
	if budget := EnhancedBudgetFor(ctx); withEnhanced && budget != nil {
		// Cache-only compatibility checks never consume an admission. Once a
		// generation's optional budget expires, CLAP continues on its own ctx.
		withEnhanced = s.enhancedCacheMiss(ctx, ref, catalog, preview.Identity, audioHash) && budget.Allow(ref.ID)
		if withEnhanced {
			enhancedCancel()
			enhancedCtx, enhancedCancel = budget.Context(ctx)
			defer enhancedCancel()
		}
	}
	samples, original, err := decodeForAnalysis(ctx, encoded, withEnhanced)
	clear(encoded)
	defer clear(samples)
	defer clear(original.Samples)
	if err != nil {
		return core.AudioAnalysis{}, int64(len(encoded)), err
	}
	first, last := randomEvidenceBounds(len(samples))
	offset := float64(first) / SampleRate
	record := core.AudioAnalysis{
		TrackID: ref.ID, CatalogVersion: catalog, TrackKey: core.ProvisionalRecordingKey(ref), Identity: preview.Identity,
		Model: s.Analyzer.Identity(), AudioSHA256: audioHash, AnalyzedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Sampling: &core.AudioSampling{Policy: PreviewSamplingVersion, AvailableSeconds: float64(len(samples)) / SampleRate},
		Coverage: core.PreviewCoverage{Available: true, StartSeconds: offset, EndSeconds: float64(last) / SampleRate, CoveredSeconds: float64(last-first) / SampleRate, Source: "deezer"},
	}
	if withEnhanced && s.DSPStore != nil {
		_, _ = s.storeDSPInterval(enhancedCtx, ref, catalog, preview.Identity, record.AudioSHA256, original, first, last)
	}
	if withEnhanced && s.MERT != nil {
		// Optional representation failure must not discard usable CLAP evidence.
		// The MERT caller separately checks its cache; cancellation still applies
		// to all subsequent inference and writes through this shared context.
		_, _ = s.MERT.AnalyzeDecoded(enhancedCtx, ref, catalog, preview.Identity, record.AudioSHA256, original)
	}
	clear(original.Samples)
	err = forEachSegment(ctx, samples[first:last], func(segment []float32, start, end float64) error {
		vector, err := s.Analyzer.EmbedAudio(ctx, segment)
		if err != nil {
			return err
		}
		if !validVector(vector, record.Model.Dimension) {
			return fmt.Errorf("audio: incompatible model output")
		}
		record.Segments = append(record.Segments, core.AudioSegment{StartSeconds: offset + start, EndSeconds: offset + end, Embedding: append([]float32(nil), vector...)})
		return nil
	})
	clear(samples)
	if err != nil {
		return core.AudioAnalysis{}, int64(len(encoded)), err
	}
	if err := ctx.Err(); err != nil {
		return core.AudioAnalysis{}, int64(len(encoded)), err
	}
	record.ID = Fingerprint(record)
	if err := s.Store.Put(ctx, record); err != nil {
		return core.AudioAnalysis{}, int64(len(encoded)), err
	}
	return record, int64(len(encoded)), nil
}

func (s *Service) fetch(ctx context.Context, address string) ([]byte, error) {
	allowed := s.AllowPreviewURL
	if allowed == nil {
		allowed = func(u *url.URL) bool {
			return u.Scheme == "https" && u.User == nil && u.Port() == "" && (u.Hostname() == "dzcdn.net" || strings.HasSuffix(u.Hostname(), ".dzcdn.net"))
		}
	}
	u, err := url.Parse(address)
	if err != nil || !allowed(u) {
		return nil, fmt.Errorf("audio: preview URL is outside the authorized provider")
	}
	client := http.Client{Timeout: 20 * time.Second}
	if s.HTTPClient != nil {
		client = *s.HTTPClient
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !allowed(req.URL) {
			return fmt.Errorf("audio: refused preview redirect")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cache-Control", "no-store")
	resp, err := deezerhttp.Client(&client).Do(req)
	if err != nil {
		return nil, fmt.Errorf("audio: preview fetch failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength > MaxEncodedBytes {
		return nil, fmt.Errorf("audio: unavailable or oversized preview")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxEncodedBytes+1))
	if err != nil || len(raw) > MaxEncodedBytes {
		clear(raw)
		return raw, fmt.Errorf("audio: incomplete or oversized preview")
	}
	return raw, nil
}
