package taste

import (
	"fmt"
	"math"
	"sort"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type libraryEvidenceGroup struct {
	source                                               core.LibraryEvidenceSource
	dimension                                            int
	positive, negative, requestPositive, requestNegative []evidencePoint
	ids                                                  map[string]bool
}

func validLibraryVector(vector core.LibraryVector) bool {
	if vector.Source.SpaceID == "" || len(vector.Values) == 0 {
		return false
	}
	var norm float64
	for _, value := range vector.Values {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
		norm += float64(value) * float64(value)
	}
	return norm > 0
}

func addLibraryPoint(groups map[string]*libraryEvidenceGroup, vector core.LibraryVector, event core.FeedbackEvent, weight float64, polarity int) {
	// Generation and dimension are retained even when an upstream contract is
	// malformed, so no accidental cross-generation averaging is possible.
	key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d", vector.Source.PackID, vector.Source.SpaceID, vector.Source.Generation, vector.Source.Scope, len(vector.Values))
	group := groups[key]
	if group == nil {
		group = &libraryEvidenceGroup{source: vector.Source, dimension: len(vector.Values), ids: map[string]bool{}}
		groups[key] = group
	}
	values := append([]float32(nil), vector.Values...)
	normalize(values)
	// Reuse deterministic clustering internally; both inputs contain the same
	// library vector, and no resulting values enter the Deej-AI affinities.
	point := evidencePoint{trackID: event.TrackID, weight: weight, vectors: ports.Vectors{Audio: values, Track: values}}
	group.ids[event.TrackID] = true
	if event.Scope == core.FeedbackScopeRequest {
		if polarity > 0 {
			group.requestPositive = append(group.requestPositive, point)
		} else {
			group.requestNegative = append(group.requestNegative, point)
		}
	} else if polarity > 0 {
		group.positive = append(group.positive, point)
	} else {
		group.negative = append(group.negative, point)
	}
}

func libraryTaste(groups map[string]*libraryEvidenceGroup) []core.LibraryTaste {
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var result []core.LibraryTaste
	for _, key := range keys {
		g := groups[key]
		profile := core.LibraryTaste{Source: g.source, Positive: centroid(g.positive, g.dimension).Audio, Negative: centroid(g.negative, g.dimension).Audio, RequestPositive: centroid(g.requestPositive, g.dimension).Audio, RequestNegative: centroid(g.requestNegative, g.dimension).Audio}
		for _, cluster := range buildClusters(g.positive, g.dimension) {
			profile.Clusters = append(profile.Clusters, cluster.Affinity.Audio)
		}
		for id := range g.ids {
			profile.EvidenceTrackIDs = append(profile.EvidenceTrackIDs, id)
		}
		sort.Strings(profile.EvidenceTrackIDs)
		result = append(result, profile)
	}
	return result
}
