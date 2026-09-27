package multichannel

import (
	"context"
	"errors"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

type automaticCachedVerifier struct {
	durationVerifierFixture
	cachedCalls int
	cached      func(context.Context, core.EnrichedTrack) (core.EnrichedTrack, error)
}

func (v *automaticCachedVerifier) VerifyCachedRecording(ctx context.Context, row core.EnrichedTrack) (core.EnrichedTrack, error) {
	v.cachedCalls++
	return v.cached(ctx, row)
}

func TestAutomaticCachedConflictOutsideOnlineQuota(t *testing.T) {
	b := durationBatchFixture(64)
	last := b.ids[63]
	v := &automaticCachedVerifier{cached: func(_ context.Context, row core.EnrichedTrack) (core.EnrichedTrack, error) {
		if row.Ref.ID == last {
			row.IdentityStatus, row.Matched = core.ResolutionAmbiguous, false
		}
		return row, nil
	}}
	v.update = func(_ context.Context, row core.EnrichedTrack) (core.EnrichedTrack, error) {
		if v.cachedCalls != 64 || b.recordings[last].IdentityStatus != core.ResolutionAmbiguous {
			t.Fatal("online work preceded cached conflict check")
		}
		return row, nil
	}
	a := &AutomaticEngine{enricher: v}
	if err := a.acquireAutomaticEvidence(context.Background(), testCatalog(), b, core.MusicIntent{}, "", nil); err != nil || v.calls != 32 {
		t.Fatal("online bound changed", err, v.calls)
	}
	if automaticFacts(b, b.meta[last].Ref, core.MusicIntent{}, "playlist") {
		t.Fatal("known conflict escaped online quota")
	}
}

func TestAutomaticCachedVerificationCancellationAndForeignIdentity(t *testing.T) {
	b := durationBatchFixture(3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v := &automaticCachedVerifier{cached: func(_ context.Context, row core.EnrichedTrack) (core.EnrichedTrack, error) {
		if row.Ref.ID == b.ids[0] {
			row.Ref.ID = "foreign"
			row.IdentityStatus = core.ResolutionAmbiguous
			return row, nil
		}
		cancel()
		return row, context.Canceled
	}}
	a := &AutomaticEngine{enricher: v}
	if err := a.acquireAutomaticEvidence(ctx, testCatalog(), b, core.MusicIntent{}, "", nil); !errors.Is(err, context.Canceled) || v.calls != 0 || v.cachedCalls != 2 {
		t.Fatal("canceled cached check continued", err, v.calls, v.cachedCalls)
	}
	if b.recordings[b.ids[0]].IdentityStatus != core.ResolutionResolved {
		t.Fatal("foreign cached identity quarantined local row")
	}
}
