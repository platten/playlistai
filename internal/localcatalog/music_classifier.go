package localcatalog

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

// MusicClassifierEvidence returns observed scores with their original model,
// source terms and coverage. Ordinary tags never manufacture this evidence.
func (c *Catalog) MusicClassifierEvidence(ctx context.Context, id string) ([]core.MusicClassifierEvidence, error) {
	localID, err := c.localID(id)
	if err != nil {
		return nil, nil
	}
	g, done, err := c.withGeneration()
	if err != nil {
		return nil, err
	}
	defer done()
	return g.ClassifierEvidence(ctx, localID)
}

func (c *CompositeCatalog) MusicClassifierEvidence(ctx context.Context, id string) ([]core.MusicClassifierEvidence, error) {
	if c.local.owns(id) {
		return c.local.MusicClassifierEvidence(ctx, id)
	}
	if c.mode == ModeLibraryOnly {
		return nil, ctx.Err()
	}
	if meta, ok := c.baseMetadataContext(ctx, id); ok {
		if match, found := c.matchBase(ctx, meta); found {
			if evidence, err := c.local.MusicClassifierEvidence(ctx, match.ID); len(evidence) > 0 || err != nil {
				return evidence, err
			}
		}
	}
	if source, ok := c.base.(interface {
		MusicClassifierEvidence(context.Context, string) ([]core.MusicClassifierEvidence, error)
	}); ok {
		return source.MusicClassifierEvidence(ctx, id)
	}
	return nil, ctx.Err()
}
