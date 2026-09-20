package multichannel

import (
	"context"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// A bounded soft evidence channel, never a bonus for library membership. Its
// request-wide denominator treats an unavailable observation as neutral.
const libraryMetadataWeight = .1

func (r *TransparentRanker) libraryMetadataScores(ctx context.Context, candidates []core.Candidate, request ports.RankRequest) error {
	if !r.cfg.LibraryEvidenceEnabled || request.Intent.Controls.RecommendationMode != core.EnhancedHybrid {
		return nil
	}
	catalog, ok := r.cat.(ports.LibraryMetadataCatalog)
	if !ok {
		return nil
	}
	active := false
	for i := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &candidates[i]
		c.Scores.LibraryMetadata, c.Available.LibraryMetadata = catalog.LibraryPreferenceScore(ctx, c.Track.ID, request.Intent, "playlist")
		c.Scores.LibraryMetadata = clamp(c.Scores.LibraryMetadata, -1, 1)
		active = active || c.Available.LibraryMetadata
	}
	if active {
		for i := range candidates {
			c := &candidates[i]
			c.Scores.Total = (c.Scores.Total + libraryMetadataWeight*c.Scores.LibraryMetadata) / (1 + libraryMetadataWeight)
		}
	}
	return ctx.Err()
}
