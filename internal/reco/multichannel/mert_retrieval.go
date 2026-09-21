package multichannel

import (
	"context"
	"math"
	"sort"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const (
	ChannelMERTAudio         = "mert_audio"
	maxMERTSimilarityQueries = 64
)

// MERTSimilaritySearchProvider prepares missing reference representations and
// searches one immutable compatible cache view. It runs before candidate
// retrieval; ranking and sequencing remain cache-only.
type MERTSimilaritySearchProvider func(
	context.Context,
	core.MusicIntent,
	core.TasteProfile,
	[]core.MERTSimilarityQuery,
	map[string]struct{},
	int,
) (*core.MERTSimilaritySearch, error)

func (o *Orchestrator) WithMERTSimilaritySearchProvider(provider MERTSimilaritySearchProvider) *Orchestrator {
	o.mertSearchProvider = provider
	return o
}

func mertSimilarityQueries(cat ports.Catalog, intent core.MusicIntent) []core.MERTSimilarityQuery {
	groups := intentReferenceTracks(cat, intent, core.InfluencePositive, false)
	if len(groups) == 0 {
		for i, ref := range intent.RequiredTracks {
			if reps := referenceTrackIdentities(cat, ref, false); len(reps) > 0 {
				groups = append(groups, referenceTracks{id: "required:" + itoa(i), reps: reps})
			}
		}
	}
	var out []core.MERTSimilarityQuery
	for _, group := range groups {
		var total float64
		for _, rep := range group.reps {
			if rep.Weight > 0 {
				total += rep.Weight
			}
		}
		if total <= 0 {
			continue
		}
		for _, rep := range group.reps {
			if len(out) == maxMERTSimilarityQueries {
				return out
			}
			meta, ok := cat.Meta(rep.TrackID)
			if !ok || rep.Weight <= 0 {
				continue
			}
			out = append(out, core.MERTSimilarityQuery{
				GroupID: group.id, Track: meta.Ref, Weight: rep.Weight / total,
			})
		}
	}
	return out
}

// primaryMERTQueries bounds live provider acquisition to one representative
// per resolved reference group. The full representative set still drives
// catalog/library retrieval; this only prevents optional preview inference
// from consuming the whole request deadline.
func primaryMERTQueries(queries []core.MERTSimilarityQuery) []core.MERTSimilarityQuery {
	out := make([]core.MERTSimilarityQuery, 0, len(queries))
	positions := map[string]int{}
	for _, query := range queries {
		if index, ok := positions[query.GroupID]; ok {
			if query.Weight > out[index].Weight {
				out[index] = query
			}
			continue
		}
		positions[query.GroupID] = len(out)
		out = append(out, query)
	}
	return out
}

func (o *Orchestrator) mertCandidates(search *core.MERTSimilaritySearch) []core.Candidate {
	if search == nil || !search.Recorded || search.CatalogVersion == "" || !validEnhancedModel(search.Model) {
		return nil
	}
	if o.resolver != nil && search.CatalogVersion != o.analysisCatalogVersion() {
		return nil
	}
	queries := make(map[string]core.MERTSimilarityQuery, len(search.Queries))
	for _, query := range search.Queries {
		if query.GroupID == "" || query.Track.ID == "" || query.RepresentationID == "" || query.Weight <= 0 || math.IsNaN(query.Weight) || math.IsInf(query.Weight, 0) {
			continue
		}
		queries[query.GroupID+"\x00"+query.Track.ID] = query
	}
	byID := make(map[string]*core.Candidate)
	for _, hit := range search.Hits {
		query, ok := queries[hit.GroupID+"\x00"+hit.QueryTrackID]
		if !ok || hit.TrackID == "" || hit.Rank <= 0 || math.IsNaN(hit.Score) || math.IsInf(hit.Score, 0) || hit.Score < -1 || hit.Score > 1 {
			continue
		}
		meta, ok := o.cat.Meta(hit.TrackID)
		if !ok || hit.Representation.TrackID != hit.TrackID || hit.Representation.CatalogVersion != search.CatalogVersion ||
			hit.Representation.ID == "" || hit.Representation.TrackKey != core.ProvisionalRecordingKey(meta.Ref) || hit.Representation.Model != search.Model {
			continue
		}
		if _, ok := enhancedCosine(hit.Representation.Pooled, hit.Representation.Pooled, search.Model.Dimension); !ok {
			continue
		}
		candidate := byID[hit.TrackID]
		if candidate == nil {
			candidate = &core.Candidate{Track: meta.Ref}
			byID[hit.TrackID] = candidate
		}
		candidate.Sources = append(candidate.Sources, core.RetrievalEvidence{
			Channel: ChannelMERTAudio, QueryID: hit.GroupID + ":" + hit.QueryTrackID,
			Rank: hit.Rank, Score: hit.Score, QueryWeight: query.Weight,
		})
	}
	out := make([]core.Candidate, 0, len(byID))
	for _, candidate := range byID {
		sort.SliceStable(candidate.Sources, func(i, j int) bool {
			if candidate.Sources[i].QueryID != candidate.Sources[j].QueryID {
				return candidate.Sources[i].QueryID < candidate.Sources[j].QueryID
			}
			return candidate.Sources[i].Rank < candidate.Sources[j].Rank
		})
		out = append(out, *candidate)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Track.ID < out[j].Track.ID })
	return out
}

func (o *Orchestrator) analysisCatalogVersion() string {
	if o.sourceCatalogVersion != "" {
		return o.sourceCatalogVersion
	}
	if o.resolver != nil {
		return o.resolver.CatalogVersion()
	}
	return "unknown"
}

// mertAudioRetriever merges the frozen MERT neighbor set with every advancing
// Deej-AI retrieval page. It recomputes retrieval fusion after adding sources so
// MERT-only candidates participate on the same bounded RRF scale.
type mertAudioRetriever struct {
	base       ports.CandidateRetriever
	candidates []core.Candidate
	cfg        Config
}

func (r *mertAudioRetriever) SupportsIntentMetadata() bool {
	metadata, ok := r.base.(interface{ SupportsIntentMetadata() bool })
	return ok && metadata.SupportsIntentMetadata()
}

func (r *mertAudioRetriever) Retrieve(ctx context.Context, request ports.RetrievalRequest) ([]core.Candidate, error) {
	raw, err := r.base.Retrieve(ctx, request)
	if err != nil {
		return nil, err
	}
	excluded := make(map[string]bool, len(request.AttemptedIDs)+len(request.RecentSelections))
	for id := range request.AttemptedIDs {
		excluded[id] = true
	}
	for _, track := range request.RecentSelections {
		excluded[track.ID] = true
	}
	byID := make(map[string]*core.Candidate, len(raw)+len(r.candidates))
	for i := range raw {
		candidate := raw[i]
		byID[candidate.Track.ID] = &candidate
	}
	for _, frozen := range r.candidates {
		if excluded[frozen.Track.ID] {
			continue
		}
		if candidate := byID[frozen.Track.ID]; candidate != nil {
			candidate.Sources = append(candidate.Sources, frozen.Sources...)
		} else {
			candidate := frozen
			byID[frozen.Track.ID] = &candidate
		}
	}
	result := make([]core.Candidate, 0, len(byID))
	var maximum float64
	for _, candidate := range byID {
		candidate.Scores.RetrievalFusion = reciprocalRankFusion(candidate.Sources, r.cfg.ReciprocalRankConstant)
		candidate.Available.RetrievalFusion = true
		maximum = math.Max(maximum, candidate.Scores.RetrievalFusion)
		result = append(result, *candidate)
	}
	for i := range result {
		if maximum > 0 {
			result[i].Scores.RetrievalFusion /= maximum
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Scores.RetrievalFusion != result[j].Scores.RetrievalFusion {
			return result[i].Scores.RetrievalFusion > result[j].Scores.RetrievalFusion
		}
		return result[i].Track.ID < result[j].Track.ID
	})
	return result, nil
}

var _ ports.CandidateRetriever = (*mertAudioRetriever)(nil)
