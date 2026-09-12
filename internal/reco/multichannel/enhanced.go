package multichannel

import (
	"math"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

const EnhancedPolicyVersion = core.EnhancedAudioPolicyVersion

func enhancedWeight(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return clamp(v, 0, .15)
}

func validEnhancedModel(m core.AudioRepresentationIdentity) bool {
	return m.Model != "" && m.Revision != "" && m.Preprocessing != "" && m.Runtime != "" && m.Dimension > 0 && m.WeightsSHA256 != "" && m.Pooling != ""
}

func representation(input core.EnhancedAudioInput, track core.TrackRef) ([]float32, bool) {
	a, ok := input.Representations[track.ID]
	if !ok || input.PolicyVersion != EnhancedPolicyVersion || input.CatalogVersion == "" || !validEnhancedModel(input.Model) || a.TrackID != track.ID || a.TrackKey != core.ProvisionalRecordingKey(track) || a.CatalogVersion != input.CatalogVersion || a.Model != input.Model {
		return nil, false
	}
	_, ok = enhancedCosine(a.Pooled, a.Pooled, input.Model.Dimension)
	return a.Pooled, ok
}

func enhancedCosine(a, b []float32, dimension int) (float64, bool) {
	if dimension <= 0 || len(a) != dimension || len(b) != dimension {
		return 0, false
	}
	var dot, na, nb float64
	for i, v := range a {
		x, y := float64(v), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 || math.IsNaN(dot) || math.IsInf(dot, 0) {
		return 0, false
	}
	return clamp(dot/math.Sqrt(na*nb), -1, 1), true
}

// DSP mappings are deliberately small and signed. They are preview-local soft
// preferences, never claims of genre, mood, instrumentation or hard suitability.
func enhancedDSP(a core.DSPAnalysis, clauses []core.AudioClause) (float64, bool) {
	var sum float64
	requested, observed := 0, 0
	for _, clause := range clauses {
		if clause.Kind != "texture" && clause.Kind != "description" {
			continue
		}
		if clause.Scope != "" && clause.Scope != "playlist" {
			continue
		}
		var value core.DSPValue
		var low, high float64
		sign := 1.0
		degree := 1.0
		if clause.Degree == "reduced" {
			degree = .5
		}
		switch strings.ToLower(strings.TrimSpace(clause.Text)) {
		case "bass heavy", "bass-heavy", "strong bass", "deep bass", "more bass":
			value, low, high = a.Features.BassEnergyRatio, 0, .6
		case "strong sub-bass", "strong subbass":
			value, low, high = a.Features.SubbassEnergyRatio, 0, .4
		case "bright", "bright sound":
			value, low, high = a.Features.SpectralCentroidHz, 500, 5000
		case "dark", "dark sound", "darker":
			value, low, high, sign = a.Features.SpectralCentroidHz, 500, 5000, -1
		case "dynamic", "wide dynamics", "big dynamic swings":
			value, low, high = a.Features.RMSWindowSpreadDB, 0, 15
		case "compressed", "compressed dynamics":
			value, low, high, sign = a.Features.RMSWindowSpreadDB, 0, 15, -1
		case "lots of transients", "sharp attacks", "percussive":
			// Onset rate and spectral flux describe the same transient axis.
			// Keep a single bounded preference rather than counting both twice.
			requested++
			onset, onsetOK := dspSignedValue(a.Features.OnsetRateHz, 0, 8)
			flux, fluxOK := dspSignedValue(a.Features.PositiveSpectralFlux, 0, 1)
			if onsetOK || fluxOK {
				if clause.Negative {
					sign = -sign
				}
				sum += degree * sign * (onset + flux) / 2
				observed++
			}
			continue
		default:
			continue
		}
		requested++
		if value.State != core.FeatureKnown || value.Value == nil || math.IsNaN(*value.Value) || math.IsInf(*value.Value, 0) {
			continue
		}
		if clause.Negative {
			sign = -sign
		}
		sum += degree * sign * (2*clamp((*value.Value-low)/(high-low), 0, 1) - 1)
		observed++
	}
	if requested == 0 || observed == 0 {
		return 0, false
	}
	return sum / float64(requested), true
}

func dspSignedValue(value core.DSPValue, low, high float64) (float64, bool) {
	if value.State != core.FeatureKnown || value.Value == nil || math.IsNaN(*value.Value) || math.IsInf(*value.Value, 0) {
		return 0, false
	}
	return 2*clamp((*value.Value-low)/(high-low), 0, 1) - 1, true
}

func (r *TransparentRanker) enhancedScores(candidates []core.Candidate, intent core.MusicIntent, snapshot *core.EnhancedAudioSnapshot) {
	if intent.Controls.RecommendationMode != core.EnhancedHybrid || snapshot == nil {
		return
	}
	input := snapshot.Input()
	if input.PolicyVersion != EnhancedPolicyVersion {
		return
	}
	clauses := enhancedClauses(intent)
	positive := r.enhancedReferences(input, positiveReferenceVectors(r.cat, intent))
	negative := r.enhancedReferences(input, negativeReferenceVectors(r.cat, intent))
	if len(positive) == 0 {
		positive = append(positive, enhancedReference{input.PositiveCentroid, 1})
	}
	if len(negative) == 0 {
		negative = append(negative, enhancedReference{input.NegativeCentroid, 1})
	}
	var mertEnabled, dspEnabled bool
	for i := range candidates {
		c := &candidates[i]
		c.Scores.EnhancedDSP, c.Available.EnhancedDSP = 0, false
		c.Scores.EnhancedMERT, c.Available.EnhancedMERT = 0, false
		if a, ok := input.DSP[c.Track.ID]; ok && input.CatalogVersion != "" && input.DSPVersion != "" && a.TrackID == c.Track.ID && a.TrackKey == core.ProvisionalRecordingKey(c.Track) && a.CatalogVersion == input.CatalogVersion && a.Version == input.DSPVersion {
			c.Scores.EnhancedDSP, c.Available.EnhancedDSP = enhancedDSP(a, clauses)
		}
		if v, ok := representation(input, c.Track); ok {
			p, pok := enhancedReferenceSimilarity(v, positive, input.Model.Dimension)
			n, nok := enhancedReferenceSimilarity(v, negative, input.Model.Dimension)
			c.Scores.EnhancedMERT = clamp(p-math.Max(0, n), -1, 1)
			c.Available.EnhancedMERT = pok || nok
		}
		mertEnabled = mertEnabled || c.Available.EnhancedMERT
		dspEnabled = dspEnabled || c.Available.EnhancedDSP
	}
	wm, wd := 0.0, 0.0
	if mertEnabled {
		wm = enhancedWeight(r.cfg.EnhancedMERTWeight)
	}
	if dspEnabled {
		wd = enhancedWeight(r.cfg.EnhancedDSPWeight)
	}
	if wm+wd == 0 {
		return
	}
	for i := range candidates {
		c := &candidates[i]
		// One denominator for the whole ranked pool, including unknown tracks.
		c.Scores.Total = (c.Scores.Total + wm*c.Scores.EnhancedMERT + wd*c.Scores.EnhancedDSP) / (1 + wm + wd)
	}
}

type enhancedReference struct {
	vector []float32
	weight float64
}

func (r *TransparentRanker) enhancedReferences(input core.EnhancedAudioInput, refs []referenceVectors) []enhancedReference {
	var out []enhancedReference
	for _, group := range refs {
		var weight float64
		var representatives []enhancedReference
		for _, rep := range group.reps {
			meta, ok := r.cat.Meta(rep.id)
			if !ok {
				continue
			}
			vector, ok := representation(input, meta.Ref)
			if !ok {
				continue
			}
			weight += rep.weight
			representatives = append(representatives, enhancedReference{vector, rep.weight})
		}
		if weight > 0 {
			for _, rep := range representatives {
				rep.weight /= weight
				out = append(out, rep)
			}
		}
	}
	return out
}

func enhancedReferenceSimilarity(vector []float32, refs []enhancedReference, dimension int) (float64, bool) {
	var total float64
	var weights float64
	for _, ref := range refs {
		if similarity, ok := enhancedCosine(vector, ref.vector, dimension); ok {
			total += similarity * ref.weight
			weights += ref.weight
		}
	}
	if weights == 0 {
		return 0, false
	}
	return total / weights, true
}

func (s *GreedySequencer) enhancedTransition(left, right core.TrackRef, requestInput core.EnhancedAudioInput, intent core.MusicIntent) float64 {
	if intent.Controls.RecommendationMode != core.EnhancedHybrid {
		return 0
	}
	a, aok := representation(requestInput, left)
	b, bok := representation(requestInput, right)
	if !aok || !bok {
		return 0
	}
	similarity, ok := enhancedCosine(a, b, requestInput.Model.Dimension)
	if !ok {
		return 0
	}
	return enhancedWeight(s.cfg.EnhancedTransitionWeight) * intent.Controls.TransitionSmoothness * similarity
}
