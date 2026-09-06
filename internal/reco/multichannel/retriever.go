package multichannel

import (
	"context"
	"math/rand"
	"sort"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const (
	ChannelSeedAudio        = "seed_audio"
	ChannelSeedCooccurrence = "seed_cooccurrence"
	ChannelTasteCluster     = "taste_cluster"
	ChannelExploration      = "exploration"
)

type Retriever struct {
	cat ports.Catalog
	sim ports.SimilarityEngine
	cfg Config
}

func NewRetriever(cat ports.Catalog, sim ports.SimilarityEngine, cfg Config) *Retriever {
	return &Retriever{cat: cat, sim: sim, cfg: cfg.normalized()}
}

type explorationOption struct {
	match   ports.Match
	queryID string
	rank    int
	weight  float64
}

func (r *Retriever) Retrieve(ctx context.Context, request ports.RetrievalRequest) ([]core.Candidate, error) {
	intent := request.Intent.Normalized()
	references := positiveReferenceVectors(r.cat, intent)
	if len(references) == 0 {
		references = requiredFallbackVectors(r.cat, intent)
	}
	exclude := make(map[string]struct{})
	for _, reference := range references {
		for _, representative := range reference.reps {
			exclude[representative.id] = struct{}{}
		}
	}
	for _, required := range intent.RequiredTracks {
		if required.TrackID != "" {
			exclude[required.TrackID] = struct{}{}
		}
	}
	totalQueries := 2 * countRepresentatives(references)
	clusterLimit := minInt(r.cfg.MaxTasteClusters, len(request.Profile.Clusters))
	totalQueries += clusterLimit
	extraPerQuery := 0
	if totalQueries > 0 {
		extraPerQuery = maxInt(1, r.cfg.ExplorationPool/totalQueries)
	}

	byID := make(map[string]*core.Candidate)
	var exploration []explorationOption
	for _, reference := range references {
		for repIndex, representative := range reference.reps {
			queryID := reference.id + ":" + representative.id + ":" + itoa(repIndex)
			if err := r.searchChannel(ctx, byID, &exploration, exclude, ChannelSeedAudio, queryID,
				representative.v.Audio, nil, [2]float32{1, 0}, representative.weight, r.cfg.SeedAudioBudget, extraPerQuery); err != nil {
				return nil, err
			}
			if err := r.searchChannel(ctx, byID, &exploration, exclude, ChannelSeedCooccurrence, queryID,
				nil, representative.v.Track, [2]float32{0, 1}, representative.weight, r.cfg.SeedCooccurrenceBudget, extraPerQuery); err != nil {
				return nil, err
			}
		}
	}
	for index := 0; index < clusterLimit; index++ {
		cluster := request.Profile.Clusters[index]
		if err := r.searchChannel(ctx, byID, &exploration, exclude, ChannelTasteCluster, cluster.ID,
			normalizeVector(cluster.Affinity.Audio), normalizeVector(cluster.Affinity.Cooccurrence),
			normalizedWeights(intent.Controls.AudioWeight, intent.Controls.CooccurrenceWeight),
			cluster.Weight, r.cfg.TasteClusterBudget, extraPerQuery); err != nil {
			return nil, err
		}
	}
	r.addExploration(byID, exploration, intent.Controls.Discovery, request.Seed)

	candidates := make([]core.Candidate, 0, len(byID))
	var maxFusion float64
	for _, candidate := range byID {
		candidate.Scores.RetrievalFusion = reciprocalRankFusion(candidate.Sources, r.cfg.ReciprocalRankConstant)
		if candidate.Scores.RetrievalFusion > maxFusion {
			maxFusion = candidate.Scores.RetrievalFusion
		}
		candidate.Available.RetrievalFusion = true
		sort.SliceStable(candidate.Sources, func(i, j int) bool {
			if candidate.Sources[i].Channel != candidate.Sources[j].Channel {
				return candidate.Sources[i].Channel < candidate.Sources[j].Channel
			}
			if candidate.Sources[i].QueryID != candidate.Sources[j].QueryID {
				return candidate.Sources[i].QueryID < candidate.Sources[j].QueryID
			}
			return candidate.Sources[i].Rank < candidate.Sources[j].Rank
		})
		candidates = append(candidates, *candidate)
	}
	if maxFusion > 0 {
		for index := range candidates {
			candidates[index].Scores.RetrievalFusion /= maxFusion
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Scores.RetrievalFusion != candidates[j].Scores.RetrievalFusion {
			return candidates[i].Scores.RetrievalFusion > candidates[j].Scores.RetrievalFusion
		}
		return candidates[i].Track.ID < candidates[j].Track.ID
	})
	if len(candidates) > r.cfg.MaxCandidates {
		candidates = candidates[:r.cfg.MaxCandidates]
	}
	return candidates, nil
}

func (r *Retriever) searchChannel(
	ctx context.Context,
	byID map[string]*core.Candidate,
	exploration *[]explorationOption,
	exclude map[string]struct{},
	channel, queryID string,
	audio, track []float32,
	weights [2]float32,
	queryWeight float64,
	budget, extra int,
) error {
	if (weights[0] == 0 || len(audio) == 0) && (weights[1] == 0 || len(track) == 0) {
		return nil
	}
	matches, err := r.sim.Search(ctx, ports.SimilarityQuery{
		AudioSum: audio, TrackSum: track, Weights: weights, K: minInt(r.sim.Len(), budget+extra), Exclude: exclude,
	})
	if err != nil {
		return err
	}
	for index, match := range matches {
		if index < budget {
			r.addSource(byID, match, core.RetrievalEvidence{
				Channel: channel, QueryID: queryID, Rank: index + 1, Score: float64(match.Score), QueryWeight: positiveWeight(queryWeight),
			})
		} else if float64(match.Score) >= r.cfg.ExplorationMinScore {
			*exploration = append(*exploration, explorationOption{
				match: match, queryID: channel + ":" + queryID, rank: index + 1, weight: positiveWeight(queryWeight),
			})
		}
	}
	return nil
}

func (r *Retriever) addSource(byID map[string]*core.Candidate, match ports.Match, source core.RetrievalEvidence) {
	candidate := byID[match.ID]
	if candidate == nil {
		meta, ok := r.cat.Meta(match.ID)
		if !ok {
			return
		}
		candidate = &core.Candidate{Track: meta.Ref, Sources: []core.RetrievalEvidence{}}
		byID[match.ID] = candidate
	}
	candidate.Sources = append(candidate.Sources, source)
}

func (r *Retriever) addExploration(byID map[string]*core.Candidate, options []explorationOption, discovery float64, seed int64) {
	budget := int(float64(r.cfg.ExplorationBudget)*clamp(discovery, 0, 1) + .5)
	if budget <= 0 || len(options) == 0 {
		return
	}
	best := make(map[string]explorationOption)
	for _, option := range options {
		current, ok := best[option.match.ID]
		if !ok || option.match.Score > current.match.Score ||
			(option.match.Score == current.match.Score && option.queryID < current.queryID) {
			best[option.match.ID] = option
		}
	}
	options = options[:0]
	for _, option := range best {
		options = append(options, option)
	}
	sort.Slice(options, func(i, j int) bool {
		if options[i].match.Score != options[j].match.Score {
			return options[i].match.Score > options[j].match.Score
		}
		return options[i].match.ID < options[j].match.ID
	})
	if len(options) > r.cfg.ExplorationPool {
		options = options[:r.cfg.ExplorationPool]
	}
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // generation seed controls reproducible exploration
	rng.Shuffle(len(options), func(i, j int) { options[i], options[j] = options[j], options[i] })
	for index := 0; index < minInt(budget, len(options)); index++ {
		option := options[index]
		r.addSource(byID, option.match, core.RetrievalEvidence{
			Channel: ChannelExploration, QueryID: option.queryID,
			Rank: option.rank, Score: float64(option.match.Score), QueryWeight: option.weight,
		})
	}
}

func requiredFallbackVectors(cat ports.Catalog, intent core.MusicIntent) []referenceVectors {
	result := make([]referenceVectors, 0, len(intent.RequiredTracks))
	for index, reference := range intent.RequiredTracks {
		if reps := referenceRepresentatives(cat, reference); len(reps) > 0 {
			result = append(result, referenceVectors{id: "required:" + itoa(index), reps: reps})
		}
	}
	return result
}

func countRepresentatives(references []referenceVectors) int {
	count := 0
	for _, reference := range references {
		count += len(reference.reps)
	}
	return count
}

func normalizedWeights(audio, track float64) [2]float32 {
	total := audio + track
	if total <= 0 {
		return [2]float32{.5, .5}
	}
	return [2]float32{float32(audio / total), float32(track / total)}
}

func reciprocalRankFusion(sources []core.RetrievalEvidence, constant float64) float64 {
	var score float64
	for _, source := range sources {
		score += positiveWeight(source.QueryWeight) / (constant + float64(source.Rank))
	}
	return score
}

func positiveWeight(weight float64) float64 {
	if weight > 0 {
		return weight
	}
	return 1
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var _ ports.CandidateRetriever = (*Retriever)(nil)
