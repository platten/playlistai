package multichannel

import (
	"context"
	"math"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// Versioned engineering scales, not calibrated musical-fit probabilities.
// Listening judgments are required before claiming these defaults improve music.
const EnhancedSemanticAdmissionMinimum = .15

const RequestFitPolicyVersion = "request-fit/v1"
const EvidenceCombinationPolicyVersion = "audio-family-observation/v1"

func candidateRequestFit(candidate core.Candidate, intent core.MusicIntent) (float64, bool) {
	if candidate.Available.RequestFit {
		return candidate.Scores.RequestFit, true
	}
	if intent.Controls.RecommendationMode == core.EnhancedHybrid {
		// Admission may use a direct retrieval comparison, but ranking must
		// keep the completed request-fit scale. A missing score is not a raw
		// cosine substitute that can outrank fully evaluated candidates.
		return 0, false
	}
	return enhancedRequestRelevance(candidate, intent)
}

func (r *TransparentRanker) requestFit(ctx context.Context, c core.Candidate, intent core.MusicIntent) (float64, bool) {
	var score, weights float64
	available := false
	add := func(value, weight float64, enabled, known bool) {
		if !enabled || weight <= 0 {
			return
		}
		weights += weight
		if known && !math.IsNaN(value) && !math.IsInf(value, 0) {
			score += weight * clamp(value, -1, 1)
			available = true
		}
	}
	references := len(intentReferenceTracksContext(ctx, r.cat, intent, core.InfluencePositive, false)) > 0
	descriptive := len(audio.Clauses(intent)) > 0
	add(c.Scores.AudioSeedAffinity, intent.Controls.AudioWeight, references, c.Available.AudioSeedAffinity)
	add(c.Scores.CooccurrenceAffinity, intent.Controls.CooccurrenceWeight, references, c.Available.CooccurrenceAffinity)
	add(c.Scores.SemanticMatch, r.cfg.SemanticWeight, descriptive, c.Available.SemanticMatch)
	add(c.Scores.AcousticIntent, acousticWeight(intent), descriptive, c.Available.AcousticIntent)
	add(c.Scores.LibraryMetadata, libraryMetadataWeight, descriptive || len(intent.Temporal) > 0, c.Available.LibraryMetadata)
	if weights > 0 {
		score /= weights
	}
	if c.Available.RequestNegativeMatch {
		score -= r.cfg.NegativePenalty * max(0, c.Scores.RequestNegativeMatch)
	}
	if c.Available.SemanticNegativeMatch {
		score -= r.cfg.SemanticNegativePenalty * max(0, c.Scores.SemanticNegativeMatch)
	}
	wm, wd := enhancedWeight(r.cfg.EnhancedMERTWeight), enhancedWeight(r.cfg.EnhancedDSPWeight)
	// A taste centroid is secondary preference, never direct request evidence.
	if (references || len(intentReferenceTracksContext(ctx, r.cat, intent, core.InfluenceNegative, false)) > 0) && c.Available.CombinedMERT {
		score += wm * c.Scores.CombinedMERT
		available = true
	}
	if c.Available.CombinedDSP {
		score += wd * c.Scores.CombinedDSP
		available = true
	}
	return score / (1 + wm + wd), available
}

// Observations compete on measured coverage, then immutable identity, never on
// the score that happens to flatter the request. Unknown coverage remains zero.
func preferObservation(left, right core.AudioObservation) bool {
	coverage := func(o core.AudioObservation) float64 {
		c := o.Coverage
		if !c.Available || math.IsNaN(c.CoveredSeconds) || math.IsInf(c.CoveredSeconds, 0) || c.CoveredSeconds < 0 {
			return 0
		}
		return c.CoveredSeconds
	}
	if a, b := coverage(left), coverage(right); a != b {
		return a > b
	}
	return left.Fingerprint < right.Fingerprint
}

func (r *TransparentRanker) combineEnhancedScores(ctx context.Context, candidates []core.Candidate, request ports.RankRequest) error {
	if request.Intent.Controls.RecommendationMode != core.EnhancedHybrid {
		return nil
	}
	input := request.EnhancedAudio.Input()
	mertEnabled, dspEnabled := false, false
	for i := range candidates {
		c := &candidates[i]
		mert, _ := selectedRepresentation(input, c.Track)
		c.Scores.CombinedMERT, c.Available.CombinedMERT = c.Scores.EnhancedMERT, c.Available.EnhancedMERT
		c.Scores.CombinedDSP, c.Available.CombinedDSP = c.Scores.EnhancedDSP, c.Available.EnhancedDSP
		for _, family := range []struct {
			name         string
			packed, live bool
			value        float64
			selected     *float64
			available    *bool
			observation  core.AudioObservation
		}{
			{"mert", c.Available.LibraryMERT, c.Available.EnhancedMERT, c.Scores.LibraryMERT, &c.Scores.CombinedMERT, &c.Available.CombinedMERT, core.AudioObservation{Coverage: mert.Coverage, Fingerprint: audio.Fingerprint(mert)}},
			{"dsp", c.Available.LibraryDSP, c.Available.EnhancedDSP, c.Scores.LibraryDSP, &c.Scores.CombinedDSP, &c.Available.CombinedDSP, core.AudioObservation{Coverage: input.DSP[c.Track.ID].Coverage, Fingerprint: audio.Fingerprint(input.DSP[c.Track.ID])}},
		} {
			if !family.packed {
				continue
			}
			observation := core.AudioObservation{Fingerprint: audio.Fingerprint(struct{ Track, Family string }{c.Track.ID, family.name})}
			if source, ok := r.cat.(interface {
				LibraryObservation(context.Context, string, string) (core.AudioObservation, error)
			}); ok {
				var err error
				observation, err = source.LibraryObservation(ctx, c.Track.ID, family.name)
				if err != nil {
					return err
				}
			}
			if !family.live || preferObservation(observation, family.observation) {
				*family.selected = family.value
			}
			*family.available = true
		}
		mertEnabled = mertEnabled || c.Available.CombinedMERT
		dspEnabled = dspEnabled || c.Available.CombinedDSP
	}
	wm, wd := 0.0, 0.0
	if mertEnabled {
		wm = enhancedWeight(r.cfg.EnhancedMERTWeight)
	}
	if dspEnabled {
		wd = enhancedWeight(r.cfg.EnhancedDSPWeight)
	}
	for i := range candidates {
		c := &candidates[i]
		c.Scores.Total = (c.Scores.Total + wm*c.Scores.CombinedMERT + wd*c.Scores.CombinedDSP) / (1 + wm + wd)
	}
	return ctx.Err()
}
