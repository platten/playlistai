package evaluation

import "github.com/platten/playlistai/internal/core"

const AutomaticFeaturesVersion = "automatic-features/v1"

// AutomaticFeatureRequest is a frozen request, not a recording's target label.
// The extractor never forwards corpus Labels to a model or metadata source.
type AutomaticFeatureRequest struct {
	Facet  string           `json:"facet"`
	Value  string           `json:"value"`
	Intent core.MusicIntent `json:"intent"`
}

type AutomaticFeatureQuery struct {
	Request AutomaticFeatureRequest  `json:"request"`
	Queries []core.AudioClauseVector `json:"queries"`
}

type AutomaticFeatures struct {
	Version              string                  `json:"version"`
	CorpusSHA256         string                  `json:"corpusSHA256"`
	Split                string                  `json:"split"`
	Model                core.AudioModelIdentity `json:"model"`
	DecoderID            string                  `json:"decoderId"`
	Sampling             string                  `json:"sampling"`
	MetadataSourceSHA256 string                  `json:"metadataSourceSHA256,omitempty"`
	MetadataSourceURL    string                  `json:"metadataSourceURL,omitempty"`
	FrozenPolicySHA256   string                  `json:"frozenPolicySHA256,omitempty"`
	Queries              []AutomaticFeatureQuery `json:"queries"`
	Tracks               []AutomaticFeatureTrack `json:"tracks"`
	HealthMilliseconds   int64                   `json:"healthMilliseconds"`
}

type AutomaticFeatureTrack struct {
	ID              string                    `json:"id"`
	ArtistID        string                    `json:"artistId"`
	AudioSHA256     string                    `json:"audioSHA256"`
	DurationSeconds float64                   `json:"durationSeconds"`
	CoveredSeconds  float64                   `json:"coveredSeconds"`
	Segments        []core.AudioSegment       `json:"segments"`
	Pooled          []float32                 `json:"pooled"`
	Annotations     []core.MetadataAnnotation `json:"annotations,omitempty"`
	BytesFetched    int64                     `json:"bytesFetched"`
	Milliseconds    int64                     `json:"milliseconds"`
	Error           string                    `json:"error,omitempty"`
}
