package evaluation

import (
	"context"
	"math"
	"sort"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func RecallAtK(ids []string, relevance map[string]float64, k int) (float64, bool) {
	var relevant int
	for _, grade := range relevance {
		if grade > 0 {
			relevant++
		}
	}
	if relevant == 0 {
		return 0, false
	}
	seen, hits := map[string]struct{}{}, 0
	for _, id := range ids[:min(k, len(ids))] {
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		if relevance[id] > 0 {
			hits++
		}
	}
	return float64(hits) / float64(relevant), true
}

func NDCGAtK(ids []string, relevance map[string]float64, k int) (float64, bool) {
	if len(relevance) == 0 {
		return 0, false
	}
	dcg := 0.0
	for index, id := range ids[:min(k, len(ids))] {
		dcg += gain(relevance[id], index)
	}
	grades := make([]float64, 0, len(relevance))
	for _, grade := range relevance {
		if grade > 0 {
			grades = append(grades, grade)
		}
	}
	if len(grades) == 0 {
		return 0, false
	}
	sort.Slice(grades, func(i, j int) bool { return grades[i] > grades[j] })
	idcg := 0.0
	for index, grade := range grades[:min(k, len(grades))] {
		idcg += gain(grade, index)
	}
	if idcg == 0 {
		return 0, false
	}
	return dcg / idcg, true
}

func gain(grade float64, zeroBasedRank int) float64 {
	return (math.Pow(2, grade) - 1) / math.Log2(float64(zeroBasedRank)+2)
}

func PlaylistDiagnostics(cat ports.Catalog, playlist core.Playlist, recent []string) (duplicates int, artistDiversity, maxArtistShare, coverage, repetition float64, transition *float64) {
	if len(playlist.Tracks) == 0 {
		return 0, 0, 0, 0, 0, nil
	}
	recordings, artists, recentSet := map[string]struct{}{}, map[string]int{}, map[string]struct{}{}
	for _, id := range recent {
		if meta, ok := cat.Meta(id); ok {
			recentSet[core.ProvisionalRecordingKey(meta.Ref)] = struct{}{}
		} else {
			recentSet[id] = struct{}{}
		}
	}
	covered, repeated := 0, 0
	var transitionSum float64
	transitionCount := 0
	for index, track := range playlist.Tracks {
		key := core.ProvisionalRecordingKey(track)
		if _, exists := recordings[key]; exists {
			duplicates++
		} else {
			recordings[key] = struct{}{}
		}
		artists[core.NormalizeIdentityPart(track.Artist)]++
		if _, ok := cat.RowOf(track.ID); ok {
			covered++
		}
		if _, ok := recentSet[key]; ok {
			repeated++
		}
		if index > 0 {
			left, leftOK := cat.Vectors(playlist.Tracks[index-1].ID)
			right, rightOK := cat.Vectors(track.ID)
			if leftOK && rightOK {
				if score, ok := vectorSimilarity(left, right, playlist.Intent.Controls.AudioWeight, playlist.Intent.Controls.CooccurrenceWeight); ok {
					transitionSum += score
					transitionCount++
				}
			}
		}
	}
	maxArtist := 0
	for _, count := range artists {
		if count > maxArtist {
			maxArtist = count
		}
	}
	artistDiversity = float64(len(artists)) / float64(len(playlist.Tracks))
	maxArtistShare = float64(maxArtist) / float64(len(playlist.Tracks))
	coverage = float64(covered) / float64(len(playlist.Tracks))
	repetition = float64(repeated) / float64(len(playlist.Tracks))
	if transitionCount > 0 {
		value := transitionSum / float64(transitionCount)
		transition = &value
	}
	return
}

func HardConstraintViolations(ctx context.Context, playlist core.Playlist, features ports.FeatureStore) int {
	excluded := map[string]struct{}{}
	referenceArtists := map[string]struct{}{}
	for _, reference := range playlist.Intent.References {
		if reference.Resolution != nil && reference.Resolution.Selected != nil {
			referenceArtists[core.NormalizeIdentityPart(reference.Resolution.Selected.Artist)] = struct{}{}
		}
	}
	noAdjacent := false
	excludeReferenceArtists := false
	for _, constraint := range playlist.Intent.HardConstraints {
		if !constraint.RuntimeEnforced {
			continue
		}
		switch constraint.Kind {
		case "exclude_artist":
			excluded[core.NormalizeIdentityPart(constraint.Value)] = struct{}{}
		case "no_back_to_back_artist":
			noAdjacent = true
		case "exclude_reference_artists":
			excludeReferenceArtists = true
		}
	}
	violations := 0
	for index, track := range playlist.Tracks {
		if _, found := excluded[core.NormalizeIdentityPart(track.Artist)]; found {
			violations++
		}
		if _, found := referenceArtists[core.NormalizeIdentityPart(track.Artist)]; excludeReferenceArtists && found {
			violations++
		}
		if noAdjacent && index > 0 && core.NormalizeIdentityPart(track.Artist) == core.NormalizeIdentityPart(playlist.Tracks[index-1].Artist) {
			violations++
		}
		if features != nil {
			feature, ok, err := features.Features(ctx, track.ID)
			if err != nil || !ok {
				for _, constraint := range playlist.Intent.HardConstraints {
					if constraint.RuntimeEnforced && semanticConstraint(constraint.Kind) {
						violations++
					}
				}
				continue
			}
			for _, constraint := range playlist.Intent.HardConstraints {
				if !constraint.RuntimeEnforced {
					continue
				}
				switch constraint.Kind {
				case "exclude_vocals", "require_instrumental":
					if feature.VocalEvidence.Missingness != core.FeatureKnown || !strings.EqualFold(feature.VocalEvidence.Value, "instrumental") {
						violations++
					}
				case "require_vocals":
					if feature.VocalEvidence.Missingness != core.FeatureKnown || strings.EqualFold(feature.VocalEvidence.Value, "instrumental") {
						violations++
					}
				case "exclude_style":
					if !featureKnown(feature.Styles, feature.Tags) || featureHas(constraint.Value, feature.Styles, feature.Tags) {
						violations++
					}
				case "require_style":
					if !featureHas(constraint.Value, feature.Styles, feature.Tags) {
						violations++
					}
				}
			}
		}
	}
	return violations
}

func featureHas(want string, groups ...[]core.FeatureValue) bool {
	want = normalizeLabel(want)
	for _, group := range groups {
		for _, value := range group {
			if value.Missingness == core.FeatureKnown && normalizeLabel(value.Value) == want {
				return true
			}
		}
	}
	return false
}
func featureKnown(groups ...[]core.FeatureValue) bool {
	for _, group := range groups {
		for _, value := range group {
			if value.Missingness == core.FeatureKnown {
				return true
			}
		}
	}
	return false
}
func semanticConstraint(kind string) bool {
	switch kind {
	case "exclude_vocals", "require_instrumental", "require_vocals", "exclude_style", "require_style":
		return true
	default:
		return false
	}
}

func vectorSimilarity(left, right ports.Vectors, audioWeight, trackWeight float64) (float64, bool) {
	var score, weights float64
	if audioWeight > 0 {
		if value, ok := cosine(left.Audio, right.Audio); ok {
			score += audioWeight * value
			weights += audioWeight
		}
	}
	if trackWeight > 0 {
		if value, ok := cosine(left.Track, right.Track); ok {
			score += trackWeight * value
			weights += trackWeight
		}
	}
	if weights == 0 {
		return 0, false
	}
	return score / weights, true
}

func cosine(left, right []float32) (float64, bool) {
	if len(left) == 0 || len(left) != len(right) {
		return 0, false
	}
	var dot, a, b float64
	for i := range left {
		dot += float64(left[i] * right[i])
		a += float64(left[i] * left[i])
		b += float64(right[i] * right[i])
	}
	if a == 0 || b == 0 {
		return 0, false
	}
	return dot / math.Sqrt(a*b), true
}

func normalizeLabel(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func aggregate(cases []CaseMetrics) AggregateMetrics {
	result := AggregateMetrics{Cases: len(cases)}
	var recall, ndcg, diversity, share, coverage, repetition, transition float64
	var recallN, ndcgN, transitionN int
	for _, item := range cases {
		if item.Error != "" {
			continue
		}
		result.SuccessfulCases++
		if item.RecallAtK != nil {
			recall += *item.RecallAtK
			recallN++
		}
		if item.NDCGAtK != nil {
			ndcg += *item.NDCGAtK
			ndcgN++
		}
		result.HardConstraintViolations += item.HardConstraintViolations
		result.RecordingDuplicates += item.RecordingDuplicates
		diversity += item.ArtistDiversity
		share += item.MaxArtistShare
		coverage += item.CatalogCoverage
		repetition += item.RecentExposureRepetition
		if item.TransitionQuality != nil {
			transition += *item.TransitionQuality
			transitionN++
		}
		result.Latency.ParseMicros += item.Latency.ParseMicros
		result.Latency.RetrievalMicros += item.Latency.RetrievalMicros
		result.Latency.RankingMicros += item.Latency.RankingMicros
		result.Latency.SequencingMicros += item.Latency.SequencingMicros
		result.Latency.TotalMicros += item.Latency.TotalMicros
	}
	if recallN > 0 {
		value := recall / float64(recallN)
		result.RecallAtK = &value
	}
	if ndcgN > 0 {
		value := ndcg / float64(ndcgN)
		result.NDCGAtK = &value
	}
	if transitionN > 0 {
		value := transition / float64(transitionN)
		result.TransitionQuality = &value
	}
	if result.SuccessfulCases > 0 {
		n := int64(result.SuccessfulCases)
		result.ArtistDiversity = diversity / float64(n)
		result.MaxArtistShare = share / float64(n)
		result.CatalogCoverage = coverage / float64(n)
		result.RecentExposureRepetition = repetition / float64(n)
		result.Latency.ParseMicros /= n
		result.Latency.RetrievalMicros /= n
		result.Latency.RankingMicros /= n
		result.Latency.SequencingMicros /= n
		result.Latency.TotalMicros /= n
	}
	return result
}

func uncertainty(cases []CaseMetrics) map[string]Interval {
	values := map[string][]float64{"artistDiversity": {}, "maxArtistShare": {}, "catalogCoverage": {}, "recentExposureRepetition": {}, "hardConstraintViolations": {}, "recordingDuplicates": {}, "totalLatencyMicros": {}}
	for _, item := range cases {
		if item.Error != "" {
			continue
		}
		values["artistDiversity"] = append(values["artistDiversity"], item.ArtistDiversity)
		values["maxArtistShare"] = append(values["maxArtistShare"], item.MaxArtistShare)
		values["catalogCoverage"] = append(values["catalogCoverage"], item.CatalogCoverage)
		values["recentExposureRepetition"] = append(values["recentExposureRepetition"], item.RecentExposureRepetition)
		values["hardConstraintViolations"] = append(values["hardConstraintViolations"], float64(item.HardConstraintViolations))
		values["recordingDuplicates"] = append(values["recordingDuplicates"], float64(item.RecordingDuplicates))
		values["totalLatencyMicros"] = append(values["totalLatencyMicros"], float64(item.Latency.TotalMicros))
		if item.RecallAtK != nil {
			values["recallAtK"] = append(values["recallAtK"], *item.RecallAtK)
		}
		if item.NDCGAtK != nil {
			values["ndcgAtK"] = append(values["ndcgAtK"], *item.NDCGAtK)
		}
		if item.TransitionQuality != nil {
			values["transitionQuality"] = append(values["transitionQuality"], *item.TransitionQuality)
		}
	}
	result := map[string]Interval{}
	for name, samples := range values {
		if len(samples) > 0 {
			result[name] = meanInterval(samples)
		}
	}
	return result
}

func meanInterval(samples []float64) Interval {
	var mean float64
	for _, v := range samples {
		mean += v
	}
	mean /= float64(len(samples))
	if len(samples) == 1 {
		return Interval{Mean: mean, Low95: mean, High95: mean, Cases: 1}
	}
	var variance float64
	for _, v := range samples {
		d := v - mean
		variance += d * d
	}
	variance /= float64(len(samples) - 1)
	margin := 1.96 * math.Sqrt(variance/float64(len(samples)))
	return Interval{Mean: mean, Low95: mean - margin, High95: mean + margin, Cases: len(samples)}
}
