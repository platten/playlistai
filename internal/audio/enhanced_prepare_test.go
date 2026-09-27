package audio

import (
	"context"
	"errors"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type cancelAfterDSPStore struct {
	ports.DSPStore
	cancel context.CancelFunc
}

func (s cancelAfterDSPStore) Put(ctx context.Context, record core.DSPAnalysis) error {
	err := s.DSPStore.Put(ctx, record)
	if err == nil {
		s.cancel()
	}
	return err
}

func TestPrepareEnhancedRetainsCompletedRowsOnStop(t *testing.T) {
	preview, _, _, _ := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	preview.DSPStore = cancelAfterDSPStore{preview.Store.(*Store).DSP(), cancel}
	ref := core.TrackRef{ID: "completed", Artist: "Synthetic", Title: "Silence"}
	snapshot, err := PrepareEnhancedEvidence(ctx, preview, nil, "catalog", core.AudioRepresentationIdentity{}, []core.TrackRef{ref}, true, nil)
	if !errors.Is(err, context.Canceled) || snapshot == nil || snapshot.Input().DSP[ref.ID].ID == "" {
		t.Fatalf("completed analysis lost on stop: %+v %v", snapshot, err)
	}
}
