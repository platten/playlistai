package audio

import (
	"context"
	"fmt"

	"github.com/platten/playlistai/internal/core"
)

// DiscogsClassifier is optional preview-scoped evidence. Its scores are
// uncalibrated estimates and do not satisfy strict musical requirements.
type DiscogsClassifier struct {
	Worker *DiscogsWorker
	Model  DiscogsModel
	Store  *Store
}

func (s *DiscogsClassifier) AnalyzeDecoded(ctx context.Context, ref core.TrackRef, catalog string, identity core.PreviewIdentity, audioHash string, pcm DecodedPCM) (core.MusicClassifierEvidence, error) {
	if s == nil || s.Worker == nil || s.Store == nil || !identity.CurrentPolicy() {
		return core.MusicClassifierEvidence{}, fmt.Errorf("audio: Discogs classifier unavailable")
	}
	if previous, hit, err := s.Store.FindClassifier(ctx, catalog, ref.ID, core.ProvisionalRecordingKey(ref), audioHash, identity.ProviderID, s.Model.Fingerprint()); err != nil || hit {
		return previous, err
	}
	mono, err := DiscogsResample(ctx, pcm)
	if err != nil {
		return core.MusicClassifierEvidence{}, err
	}
	defer clear(mono)
	scores, err := s.Worker.Classify(ctx, mono)
	if err != nil {
		return core.MusicClassifierEvidence{}, err
	}
	heads := make([]core.MusicClassifierHead, len(s.Model.Heads))
	for i, head := range s.Model.Heads {
		head.Scores = scores[i]
		heads[i] = head
	}
	duration := float64(len(mono)) / discogsRate
	evidence := core.MusicClassifierEvidence{
		Version: core.MusicClassifierEvidenceVersion, Encoder: s.Model.Encoder,
		Preprocessing: DiscogsPreprocessing, Runtime: DiscogsRuntime,
		AudioSHA256: audioHash, Source: "deezer-preview", SourceID: identity.ProviderID,
		License:  "local-only preview derivative; Deezer permission; Essentia noncommercial model notices",
		Coverage: core.LibraryCLAPCoverage{CoveredSeconds: duration, Incomplete: true, PartialReason: "sampled provider preview only", Segments: []core.LibraryAudioInterval{{StartSeconds: 0, EndSeconds: duration}}},
		Heads:    heads,
	}
	if err := evidence.Validate(); err != nil {
		return core.MusicClassifierEvidence{}, err
	}
	if err := s.Store.PutClassifier(ctx, catalog, ref.ID, core.ProvisionalRecordingKey(ref), identity, evidence); err != nil {
		return core.MusicClassifierEvidence{}, err
	}
	return evidence, nil
}
