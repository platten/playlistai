// Package taste builds and stores local feedback-derived taste profiles.
package taste

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const (
	ProfileContractVersion  = 2
	ProfileAlgorithmVersion = "taste-profile/v2"
	profileHalfLife         = 30 * 24 * time.Hour
	exposureHalfLife        = 7 * 24 * time.Hour
	maxTasteClusters        = 4
	clusterDistanceFloor    = 0.30
)

type ProfileOptions struct {
	RequestID           string
	SessionID           string
	IncludeAllExposures bool
}

type evidencePoint struct {
	trackID string
	weight  float64
	vectors ports.Vectors
}

// BuildProfile deterministically projects explicit feedback into both catalog
// embedding spaces. Time decay is anchored to the newest contributing event,
// so rebuilding the same event set produces the same snapshot later.
func BuildProfile(ctx context.Context, catalog ports.Catalog, events []core.FeedbackEvent, options ProfileOptions) (core.TasteProfile, error) {
	if err := ctx.Err(); err != nil {
		return core.TasteProfile{}, err
	}
	catalogVersion := "unknown"
	if resolver, ok := catalog.(interface{ CatalogVersion() string }); ok {
		catalogVersion = resolver.CatalogVersion()
	}
	profile := core.TasteProfile{
		Version: ProfileContractVersion, AlgorithmVersion: ProfileAlgorithmVersion,
		CatalogVersion: catalogVersion, RequestID: options.RequestID, SessionID: options.SessionID,
		ColdStart: true, Clusters: []core.TasteCluster{}, RecentExposures: map[string]float64{},
	}
	if catalog == nil {
		profile.SnapshotID = snapshotID(profile, nil)
		return profile, nil
	}

	ordered := append([]core.FeedbackEvent(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].OccurredAt.Equal(ordered[j].OccurredAt) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].OccurredAt.Before(ordered[j].OccurredAt)
	})
	var contributing, exposures []core.FeedbackEvent
	for index, event := range ordered {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return core.TasteProfile{}, err
			}
		}
		if event.Type == core.FeedbackExposure {
			// A context-free profile reports all exposures. A contextual
			// profile reports only exposures from that request or session.
			if options.IncludeAllExposures || (options.RequestID == "" && options.SessionID == "") || matchesRequest(event, options) {
				profile.ExposureCount++
				exposures = append(exposures, event)
				if event.OccurredAt.After(profile.AsOf) {
					profile.AsOf = event.OccurredAt.UTC()
				}
			}
			continue
		}
		if event.Scope == core.FeedbackScopeRequest && !matchesRequest(event, options) {
			continue
		}
		if _, _, ok := feedbackWeight(event.Type); !ok {
			continue
		}
		if _, ok := catalog.Vectors(event.TrackID); !ok {
			continue
		}
		contributing = append(contributing, event)
		if event.OccurredAt.After(profile.AsOf) {
			profile.AsOf = event.OccurredAt.UTC()
		}
	}

	var positive, negative, requestPositive, requestNegative []evidencePoint
	for index, event := range contributing {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return core.TasteProfile{}, err
			}
		}
		vectors, _ := catalog.Vectors(event.TrackID)
		polarity, base, _ := feedbackWeight(event.Type)
		age := profile.AsOf.Sub(event.OccurredAt)
		if age < 0 {
			age = 0
		}
		weight := base * math.Exp2(-float64(age)/float64(profileHalfLife))
		point := evidencePoint{trackID: event.TrackID, weight: weight, vectors: vectors}
		if event.Scope == core.FeedbackScopeRequest {
			profile.RequestEvidence++
			if polarity > 0 {
				requestPositive = append(requestPositive, point)
			} else {
				requestNegative = append(requestNegative, point)
			}
		} else if polarity > 0 {
			profile.PositiveEvidence++
			positive = append(positive, point)
		} else {
			profile.NegativeEvidence++
			negative = append(negative, point)
		}
	}
	for _, event := range exposures {
		age := profile.AsOf.Sub(event.OccurredAt)
		if age < 0 {
			age = 0
		}
		profile.RecentExposures[event.TrackID] += math.Exp2(-float64(age) / float64(exposureHalfLife))
	}
	for trackID, exposure := range profile.RecentExposures {
		profile.RecentExposures[trackID] = 1 - math.Exp(-exposure)
	}

	profile.Positive = centroid(positive, catalog.Dim())
	profile.Negative = centroid(negative, catalog.Dim())
	profile.RequestPositive = centroid(requestPositive, catalog.Dim())
	profile.RequestNegative = centroid(requestNegative, catalog.Dim())
	profile.Clusters = buildClusters(positive, catalog.Dim())
	profile.ColdStart = len(contributing) == 0
	profile.SnapshotID = snapshotID(profile, append(contributing, exposures...))
	return profile, nil
}

func matchesRequest(event core.FeedbackEvent, options ProfileOptions) bool {
	if options.RequestID != "" {
		return event.RequestID == options.RequestID
	}
	return options.SessionID != "" && event.SessionID == options.SessionID
}

func feedbackWeight(kind core.FeedbackType) (polarity int, weight float64, ok bool) {
	switch kind {
	case core.FeedbackLike:
		return 1, 1, true
	case core.FeedbackDislike:
		return -1, 1, true
	case core.FeedbackMoreLike:
		return 1, 0.8, true
	case core.FeedbackLessLike:
		return -1, 0.8, true
	case core.FeedbackAccepted:
		return 1, 0.35, true
	case core.FeedbackRemoved:
		return -1, 0.35, true
	default:
		return 0, 0, false
	}
}

func centroid(points []evidencePoint, dim int) core.EmbeddingAffinity {
	audio, track := make([]float32, dim), make([]float32, dim)
	for _, point := range points {
		for i := 0; i < dim && i < len(point.vectors.Audio); i++ {
			audio[i] += float32(point.weight) * point.vectors.Audio[i]
		}
		for i := 0; i < dim && i < len(point.vectors.Track); i++ {
			track[i] += float32(point.weight) * point.vectors.Track[i]
		}
	}
	normalize(audio)
	normalize(track)
	return core.EmbeddingAffinity{Audio: audio, Cooccurrence: track}
}

func buildClusters(points []evidencePoint, dim int) []core.TasteCluster {
	points = aggregatePoints(points)
	if len(points) == 0 {
		return []core.TasteCluster{}
	}
	first := 0
	for i := 1; i < len(points); i++ {
		if points[i].weight > points[first].weight ||
			(points[i].weight == points[first].weight && points[i].trackID < points[first].trackID) {
			first = i
		}
	}
	medoids := []int{first}
	for len(medoids) < maxTasteClusters && len(medoids) < len(points) {
		candidate, farthest := -1, -1.0
		for index, point := range points {
			if containsInt(medoids, index) {
				continue
			}
			nearest := 2.0
			for _, medoid := range medoids {
				distance := 1 - vectorSimilarity(point.vectors, points[medoid].vectors)
				if distance < nearest {
					nearest = distance
				}
			}
			if nearest > farthest || (nearest == farthest && (candidate < 0 || point.trackID < points[candidate].trackID)) {
				candidate, farthest = index, nearest
			}
		}
		if candidate < 0 || farthest < clusterDistanceFloor {
			break
		}
		medoids = append(medoids, candidate)
	}

	groups := make([][]evidencePoint, len(medoids))
	var totalWeight float64
	for _, point := range points {
		best, bestSimilarity := 0, -2.0
		for group, medoid := range medoids {
			similarity := vectorSimilarity(point.vectors, points[medoid].vectors)
			if similarity > bestSimilarity {
				best, bestSimilarity = group, similarity
			}
		}
		groups[best] = append(groups[best], point)
		totalWeight += point.weight
	}
	clusters := make([]core.TasteCluster, 0, len(groups))
	for _, group := range groups {
		var weight float64
		ids := make([]string, 0, len(group))
		for _, point := range group {
			weight += point.weight
			ids = append(ids, point.trackID)
		}
		sort.Strings(ids)
		idSum := sha256.Sum256([]byte(fmt.Sprintf("%s|%v", ProfileAlgorithmVersion, ids)))
		clusters = append(clusters, core.TasteCluster{
			ID: hex.EncodeToString(idSum[:8]), Weight: weight / totalWeight,
			Affinity: centroid(group, dim), EvidenceTrackIDs: ids,
		})
	}
	sort.SliceStable(clusters, func(i, j int) bool {
		if clusters[i].Weight != clusters[j].Weight {
			return clusters[i].Weight > clusters[j].Weight
		}
		return clusters[i].ID < clusters[j].ID
	})
	return clusters
}

func aggregatePoints(points []evidencePoint) []evidencePoint {
	byID := make(map[string]evidencePoint, len(points))
	for _, point := range points {
		if existing, ok := byID[point.trackID]; ok {
			existing.weight += point.weight
			byID[point.trackID] = existing
		} else {
			byID[point.trackID] = point
		}
	}
	out := make([]evidencePoint, 0, len(byID))
	for _, point := range byID {
		out = append(out, point)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].trackID < out[j].trackID })
	return out
}

func snapshotID(profile core.TasteProfile, events []core.FeedbackEvent) string {
	requestID, sessionID := "", ""
	if profile.RequestEvidence > 0 {
		requestID, sessionID = profile.RequestID, profile.SessionID
	}
	payload := struct {
		Version   int                  `json:"version"`
		Algorithm string               `json:"algorithm"`
		Catalog   string               `json:"catalog"`
		RequestID string               `json:"requestId"`
		SessionID string               `json:"sessionId"`
		Events    []core.FeedbackEvent `json:"events"`
	}{profile.Version, profile.AlgorithmVersion, profile.CatalogVersion, requestID, sessionID, events}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func normalize(values []float32) {
	var sum float64
	for _, value := range values {
		sum += float64(value) * float64(value)
	}
	if sum == 0 {
		return
	}
	norm := float32(math.Sqrt(sum))
	for index := range values {
		values[index] /= norm
	}
}

func vectorSimilarity(a, b ports.Vectors) float64 {
	return (cosine(a.Audio, b.Audio) + cosine(a.Track, b.Track)) / 2
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, aa, bb float64
	for index := range a {
		dot += float64(a[index]) * float64(b[index])
		aa += float64(a[index]) * float64(a[index])
		bb += float64(b[index]) * float64(b[index])
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / math.Sqrt(aa*bb)
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
