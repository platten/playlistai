package multichannel

import (
	"context"
	"math"
	"sort"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type TransparentRanker struct {
	cat ports.Catalog
	cfg Config
}

func NewRanker(cat ports.Catalog, cfg Config) *TransparentRanker {
	return &TransparentRanker{cat: cat, cfg: cfg.normalized()}
}

func (r *TransparentRanker) Rank(ctx context.Context, candidates []core.Candidate, request ports.RankRequest) ([]core.Candidate, error) {
	intent := request.Intent.Normalized()
	clauses := audio.Clauses(intent)
	metadata := map[string]core.EnrichedTrack{}
	if intent.Knowledge != nil {
		for _, track := range intent.Knowledge.Tracks {
			metadata[track.Ref.ID] = track
		}
	}
	positiveRefs := positiveReferenceVectors(r.cat, intent)
	negativeRefs := negativeReferenceVectors(r.cat, intent)
	audioWeight, trackWeight := intent.Controls.AudioWeight, intent.Controls.CooccurrenceWeight
	recentExposures := r.exposuresByRecording(request.Profile.RecentExposures)
	result := append([]core.Candidate(nil), candidates...)
	for index := range result {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		vectors, ok := r.cat.Vectors(result[index].Track.ID)
		comparisons := acousticComparisons(metadata[result[index].Track.ID], clauses)
		if preview, ok := request.PreviewAssessments[result[index].Track.ID]; ok {
			if intent.Controls.RecommendationMode == core.CLAPFirst {
				audio.ApplyScores(&result[index], preview)
				comparisons = preferCLAPRanking(comparisons, preview)
			} else {
				preferAcousticRanking(&result[index], comparisons, preview)
			}
		}
		result[index].Scores.AcousticIntent, result[index].Available.AcousticIntent = acousticIntentScore(comparisons)
		if !ok {
			continue
		}
		candidate := &result[index]
		candidate.Scores.AudioSeedAffinity, candidate.Available.AudioSeedAffinity =
			weightedSpaceSimilarity(vectors.Audio, positiveRefs, true)
		candidate.Scores.CooccurrenceAffinity, candidate.Available.CooccurrenceAffinity =
			weightedSpaceSimilarity(vectors.Track, positiveRefs, false)

		historyPositive, historyAvailable := positiveListenerAffinity(request.Profile, vectors, audioWeight, trackWeight)
		requestPositive, requestPositiveAvailable := affinitySimilarity(request.Profile.RequestPositive, vectors, audioWeight, trackWeight)
		switch {
		case requestPositiveAvailable:
			candidate.Scores.ListenerAffinity = priorityBlend(requestPositive, historyPositive, historyAvailable)
			candidate.Available.ListenerAffinity = true
		case historyAvailable:
			candidate.Scores.ListenerAffinity = historyPositive
			candidate.Available.ListenerAffinity = true
		}

		explicitNegative, explicitNegativeAvailable := negativeSeedAffinity(vectors, negativeRefs, audioWeight, trackWeight)
		requestNegative, requestNegativeAvailable := affinitySimilarity(request.Profile.RequestNegative, vectors, audioWeight, trackWeight)
		historyNegative, historyNegativeAvailable := affinitySimilarity(request.Profile.Negative, vectors, audioWeight, trackWeight)
		candidate.Scores.NegativeMatch, candidate.Available.NegativeMatch = priorityNegative(
			explicitNegative, explicitNegativeAvailable,
			requestNegative, requestNegativeAvailable,
			historyNegative, historyNegativeAvailable,
		)
		if exposure, available := recentExposures[core.ProvisionalRecordingKey(candidate.Track)]; available {
			candidate.Scores.RecentExposure = clamp(exposure, 0, 1)
			candidate.Available.RecentExposure = true
		} else if request.Profile.ExposureCount > 0 {
			candidate.Available.RecentExposure = true
		}
		if historyAvailable {
			candidate.Scores.Novelty = clamp((1-historyPositive)/2, 0, 1)
			candidate.Available.Novelty = true
		}
	}
	availability := rankAvailability(result)
	for index := range result {
		result[index].Scores.Total = r.total(result[index], intent, availability)
	}
	r.enhancedScores(result, intent, request.EnhancedAudio)
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Scores.Total != result[j].Scores.Total {
			return result[i].Scores.Total > result[j].Scores.Total
		}
		if result[i].Scores.RetrievalFusion != result[j].Scores.RetrievalFusion {
			return result[i].Scores.RetrievalFusion > result[j].Scores.RetrievalFusion
		}
		return result[i].Track.ID < result[j].Track.ID
	})
	return result, nil
}

func (r *TransparentRanker) exposuresByRecording(exposures map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(exposures))
	for trackID, exposure := range exposures {
		meta, ok := r.cat.Meta(trackID)
		if !ok {
			continue
		}
		key := core.ProvisionalRecordingKey(meta.Ref)
		if exposure > result[key] {
			result[key] = exposure
		}
	}
	return result
}

type componentAvailability struct {
	audio, cooccurrence, listener, retrieval, semantic bool
	acoustic                                           bool
}

func rankAvailability(candidates []core.Candidate) componentAvailability {
	var out componentAvailability
	for _, candidate := range candidates {
		out.audio = out.audio || candidate.Available.AudioSeedAffinity
		out.cooccurrence = out.cooccurrence || candidate.Available.CooccurrenceAffinity
		out.listener = out.listener || candidate.Available.ListenerAffinity
		out.retrieval = out.retrieval || candidate.Available.RetrievalFusion
		out.semantic = out.semantic || candidate.Available.SemanticMatch
		out.acoustic = out.acoustic || candidate.Available.AcousticIntent
	}
	return out
}

func (r *TransparentRanker) total(candidate core.Candidate, intent core.MusicIntent, availability componentAvailability) float64 {
	var relevance, weights float64
	add := func(score, weight float64, channelEnabled, candidateAvailable bool) {
		if channelEnabled && weight > 0 {
			// Missing candidate evidence contributes a neutral zero on a fixed
			// request-wide scale; it never deletes the component's denominator.
			if candidateAvailable {
				relevance += weight * score
			}
			weights += weight
		}
	}
	add(candidate.Scores.AudioSeedAffinity, intent.Controls.AudioWeight, availability.audio, candidate.Available.AudioSeedAffinity)
	add(candidate.Scores.CooccurrenceAffinity, intent.Controls.CooccurrenceWeight, availability.cooccurrence, candidate.Available.CooccurrenceAffinity)
	add(candidate.Scores.ListenerAffinity, r.cfg.ListenerWeight, availability.listener, candidate.Available.ListenerAffinity)
	add(candidate.Scores.RetrievalFusion, r.cfg.RetrievalWeight, availability.retrieval, candidate.Available.RetrievalFusion)
	add(candidate.Scores.SemanticMatch, r.cfg.SemanticWeight, availability.semantic, candidate.Available.SemanticMatch)
	add(candidate.Scores.AcousticIntent, acousticWeight(intent), availability.acoustic, candidate.Available.AcousticIntent)
	if weights > 0 {
		relevance /= weights
	}
	if candidate.Available.NegativeMatch {
		relevance -= r.cfg.NegativePenalty * clamp(candidate.Scores.NegativeMatch, 0, 1)
	}
	if candidate.Available.SemanticNegativeMatch {
		relevance -= r.cfg.SemanticNegativePenalty * clamp(candidate.Scores.SemanticNegativeMatch, 0, 1)
	}
	if candidate.Available.RecentExposure {
		relevance -= r.cfg.ExposurePenalty * candidate.Scores.RecentExposure
	}
	if candidate.Available.Novelty {
		relevance += r.cfg.NoveltyWeight * intent.Controls.Discovery * candidate.Scores.Novelty
	}
	return relevance
}

func positiveListenerAffinity(profile core.TasteProfile, candidate ports.Vectors, audioWeight, trackWeight float64) (float64, bool) {
	best, available := affinitySimilarity(profile.Positive, candidate, audioWeight, trackWeight)
	for _, cluster := range profile.Clusters {
		score, ok := affinitySimilarity(cluster.Affinity, candidate, audioWeight, trackWeight)
		if ok && (!available || score > best) {
			best, available = score, true
		}
	}
	return best, available
}

func negativeSeedAffinity(candidate ports.Vectors, refs []referenceVectors, audioWeight, trackWeight float64) (float64, bool) {
	audio, audioOK := weightedSpaceSimilarity(candidate.Audio, refs, true)
	track, trackOK := weightedSpaceSimilarity(candidate.Track, refs, false)
	var score, weights float64
	if audioOK && audioWeight > 0 {
		score += audioWeight * audio
		weights += audioWeight
	}
	if trackOK && trackWeight > 0 {
		score += trackWeight * track
		weights += trackWeight
	}
	if weights == 0 {
		return 0, false
	}
	return score / weights, true
}

func priorityBlend(primary, secondary float64, secondaryAvailable bool) float64 {
	if !secondaryAvailable {
		return primary
	}
	return (primary + .25*secondary) / 1.25
}

func priorityNegative(explicit float64, explicitOK bool, request float64, requestOK bool, history float64, historyOK bool) (float64, bool) {
	switch {
	case explicitOK:
		return clamp(math.Max(0, explicit)+.25*math.Max(0, request)+.0625*math.Max(0, history), 0, 1), true
	case requestOK:
		return clamp(math.Max(0, request)+.25*math.Max(0, history), 0, 1), true
	case historyOK:
		return clamp(math.Max(0, history), 0, 1), true
	default:
		return 0, false
	}
}

var _ ports.Ranker = (*TransparentRanker)(nil)
