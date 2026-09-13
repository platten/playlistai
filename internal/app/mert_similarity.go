package app

import (
	"context"
	"math"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// SearchMERTSimilarity prepares only the resolved query recordings within the
// generation's shared enhanced budget, then searches the compatible local cache.
// Candidate inference remains in the later bounded preparation stage.
func (c *Container) SearchMERTSimilarity(
	ctx context.Context,
	intent core.MusicIntent,
	profile core.TasteProfile,
	queries []core.MERTSimilarityQuery,
	exclude map[string]struct{},
	limit int,
) (*core.MERTSimilaritySearch, error) {
	if intent.Controls.RecommendationMode != core.EnhancedHybrid || limit <= 0 {
		return nil, nil
	}
	c.enhanced.mu.Lock()
	enabled := c.enhanced.mertEnabled
	c.enhanced.mu.Unlock()
	if !enabled || c.analysis.store == nil {
		return nil, nil
	}
	tracks := make([]core.TrackRef, 0, len(queries))
	for _, query := range queries {
		if query.Track.ID != "" {
			tracks = append(tracks, query.Track)
		}
	}
	snapshot, err := c.prepareEnhancedAudio(ctx, intent, profile, tracks, true, nil)
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if snapshot == nil {
		return nil, err
	}
	input := snapshot.Input()
	search := &core.MERTSimilaritySearch{
		Recorded: true, CatalogVersion: input.CatalogVersion, Model: input.Model,
		Queries: append([]core.MERTSimilarityQuery(nil), queries...),
	}
	if input.Model.Dimension <= 0 {
		return search, err
	}
	validQueries := 0
	for index := range search.Queries {
		if representation, ok := input.Representations[search.Queries[index].Track.ID]; ok {
			search.Queries[index].RepresentationID = representation.ID
			validQueries++
		}
	}
	searcher, ok := any(c.analysis.store.Representations()).(ports.AudioRepresentationBatchSearcher)
	if !ok {
		return search, err
	}
	if validQueries == 0 {
		results, coverageErr := searcher.SearchBatch(ctx, []ports.AudioRepresentationQuery{{CatalogVersion: input.CatalogVersion, Model: input.Model}})
		if coverageErr != nil {
			return search, coverageErr
		}
		coverage := results[0]
		search.SearchableTracks, search.ViewFingerprint = coverage.SearchableTracks, coverage.Fingerprint
		return search, err
	}
	perQuery := max(1, int(math.Ceil(float64(limit)/float64(validQueries))))
	if perQuery > limit {
		perQuery = limit
	}
	baseExclude := make(map[string]struct{}, len(exclude)+len(queries))
	for id := range exclude {
		baseExclude[id] = struct{}{}
	}
	for _, query := range queries {
		baseExclude[query.Track.ID] = struct{}{}
	}
	batch := make([]ports.AudioRepresentationQuery, 0, validQueries)
	queryIndexes := make([]int, 0, validQueries)
	for index, query := range search.Queries {
		representation, ok := input.Representations[query.Track.ID]
		if !ok || representation.ID != query.RepresentationID {
			continue
		}
		queryIndexes = append(queryIndexes, index)
		batch = append(batch, ports.AudioRepresentationQuery{
			CatalogVersion: input.CatalogVersion, Model: input.Model,
			Vector: representation.Pooled, Limit: perQuery, Exclude: baseExclude,
		})
	}
	results, searchErr := searcher.SearchBatch(ctx, batch)
	if searchErr != nil {
		return search, searchErr
	}
	for resultIndex, result := range results {
		query := search.Queries[queryIndexes[resultIndex]]
		search.SearchableTracks, search.ViewFingerprint = result.SearchableTracks, result.Fingerprint
		for rank, match := range result.Matches {
			search.Hits = append(search.Hits, core.MERTSimilarityHit{
				GroupID: query.GroupID, QueryTrackID: query.Track.ID,
				TrackID: match.Representation.TrackID, Rank: rank + 1,
				Score: match.Score, QueryWeight: query.Weight,
				Representation: match.Representation,
			})
		}
	}
	return search, err
}
