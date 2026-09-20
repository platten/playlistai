// Package localcatalog exposes one immutable, pinned local-library pack for
// metadata lookup, path resolution, and exact MERT retrieval. It deliberately
// does not implement ports.Catalog: library MERT vectors are not Deej-AI audio
// or co-occurrence vectors.
package localcatalog

import (
	"encoding/json"
	"errors"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
)

const (
	MetadataChannel = "library_metadata"
	MERTChannel     = "library_mert"
	CLAPChannel     = "library_clap"
	ClusterChannel  = "library_cluster"
	DSPChannel      = "library_dsp_percentile"
)

var (
	ErrClosed            = errors.New("localcatalog: catalog is closed")
	ErrInvalidID         = errors.New("localcatalog: invalid namespaced track ID")
	ErrIncompatibleSpace = errors.New("localcatalog: incompatible MERT representation")
	ErrNoVector          = errors.New("localcatalog: query track has no MERT vector")
)

// Provenance identifies the immutable evidence generation behind a result.
// Source distinguishes personal local_library and public shared_pack data.
type Provenance struct {
	ProfileGeneration  string `json:"profileGeneration,omitempty"`
	Source             string `json:"source"`
	SourceID           string `json:"sourceId"`
	PackID             string `json:"packId"`
	PackSHA256         string `json:"packSha256"`
	CorpusGeneration   string `json:"corpusGeneration"`
	MetadataGeneration string `json:"metadataGeneration"`
	MERTGeneration     string `json:"mertGeneration,omitempty"`
	CLAPGeneration     string `json:"clapGeneration,omitempty"`
}

// Track is local-library metadata. ID is namespace-qualified; LocalID is the
// stable ID stored in the pack. No Deej-AI or Spotify vector is synthesized.
type Track struct {
	ID                   string                        `json:"id"`
	LocalID              string                        `json:"localId"`
	Artist               string                        `json:"artist"`
	Title                string                        `json:"title"`
	NormalizedArtist     string                        `json:"normalizedArtist,omitempty"`
	NormalizedTitle      string                        `json:"normalizedTitle,omitempty"`
	SourceIdentity       string                        `json:"sourceIdentity,omitempty"`
	RecordingIdentity    string                        `json:"recordingIdentity,omitempty"`
	ISRC                 string                        `json:"isrc,omitempty"`
	MusicBrainzRecording string                        `json:"musicBrainzRecording,omitempty"`
	AcoustID             string                        `json:"acoustId,omitempty"`
	AudioFingerprint     *librarypack.AudioFingerprint `json:"audioFingerprint,omitempty"`
	DurationMilliseconds int64                         `json:"durationMilliseconds,omitempty"`
	DurationProvenance   string                        `json:"durationProvenance,omitempty"`
	DurationReliable     bool                          `json:"durationReliable"`
	Cluster              *int                          `json:"cluster,omitempty"`
	ClusterScore         float64                       `json:"clusterScore,omitempty"`
	AlternativeCluster   *int                          `json:"alternativeCluster,omitempty"`
	AlternativeScore     float64                       `json:"alternativeScore,omitempty"`
	AlbumArtist          string                        `json:"albumArtist,omitempty"`
	Album                string                        `json:"album,omitempty"`
	Capabilities         []string                      `json:"capabilities"`
	Missingness          json.RawMessage               `json:"missingness"`
	Failure              string                        `json:"failure,omitempty"`
	Unsupported          string                        `json:"unsupported,omitempty"`
	Provenance           Provenance                    `json:"provenance"`
}

// Evidence preserves a channel-native score and its immutable representation.
// Score is a cosine for MERT and a metadata match score for metadata search;
// the two values are not calibrated or directly interchangeable.
type Evidence struct {
	Channel     string                   `json:"channel"`
	Rank        int                      `json:"rank"`
	Score       float64                  `json:"score"`
	QueryID     string                   `json:"queryId,omitempty"`
	Provenance  Provenance               `json:"provenance"`
	VectorSpace *librarypack.VectorSpace `json:"vectorSpace,omitempty"`
}

type Hit struct {
	Track    Track    `json:"track"`
	Evidence Evidence `json:"evidence"`
}

type Candidate struct {
	Track    Track      `json:"track"`
	Evidence []Evidence `json:"evidence"`
}

type MetadataQuery struct {
	Criterion  *core.MusicalCriterion
	Text       string
	Limit      int
	ExcludeIDs map[string]struct{}
}

type NeighborQuery struct {
	// Exactly one of SeedID or Vector must be provided. SeedID is namespaced.
	SeedID string
	Vector []float32
	// Space may be omitted for a SeedID. An external Vector must provide the
	// complete representation contract, which must exactly match the pack.
	Space *librarypack.VectorSpace
	// CLAPModel is required for external vectors targeting an explicit v7
	// paired runtime. It is ignored by MERT retrieval.
	CLAPModel  *core.AudioModelIdentity
	Limit      int
	ExcludeIDs map[string]struct{}
}

type Query struct {
	Metadata *MetadataQuery
	MERT     *NeighborQuery
	CLAP     *NeighborQuery
}

type QueryResult struct {
	PackID     string      `json:"packId"`
	Candidates []Candidate `json:"candidates"`
}

type PathState string

const (
	PathNoEvidence PathState = "no_path_evidence"
	PathUnmapped   PathState = "unmapped"
	PathOffline    PathState = "offline"
	PathMissing    PathState = "missing"
	PathUnsafe     PathState = "unsafe"
	PathAvailable  PathState = "available"
)

type PathResolution struct {
	TrackID      string    `json:"trackId"`
	RootAlias    string    `json:"rootAlias,omitempty"`
	RelativePath string    `json:"relativePath,omitempty"`
	Path         string    `json:"path,omitempty"`
	State        PathState `json:"state"`
	Detail       string    `json:"detail,omitempty"`
}
