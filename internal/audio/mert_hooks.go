package audio

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

// enhancedCacheMiss runs after CLAP has fetched a verified preview, so a cache
// hit must match its encoded hash and full identity as well as model/version.
func (s *Service) enhancedCacheMiss(ctx context.Context, ref core.TrackRef, catalog string, identity core.PreviewIdentity, hash string) bool {
	key := core.ProvisionalRecordingKey(ref)
	if s.DSPStore != nil {
		a, ok, err := s.DSPStore.Find(ctx, catalog, ref.ID, key, DSPAnalysisVersion)
		if err != nil || !ok || a.AudioSHA256 != hash || Fingerprint(a.Identity) != Fingerprint(identity) {
			return true
		}
	}
	if s.MERT != nil && s.MERT.Ready() {
		a, ok, err := s.MERT.Store.Find(ctx, catalog, ref.ID, key, s.MERT.Analyzer.Identity())
		if err != nil || !ok || a.AudioSHA256 != hash || Fingerprint(a.Identity) != Fingerprint(identity) {
			return true
		}
	}
	return false
}
