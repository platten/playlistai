package app

import (
	"context"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// The provider keeps this delegate, rather than the current parser instance,
// so changing or disabling the local model cannot leave a stale model client.
func (c *Container) ExtractRecordingClaims(ctx context.Context, source ports.RecordingSource) ([]core.RecordingClaim, error) {
	if extractor, ok := c.IntentParser().(ports.RecordingSourceExtractor); ok {
		return extractor.ExtractRecordingClaims(ctx, source)
	}
	return nil, core.ErrUnavailable
}
