package core

// StepReason records why a track landed in a playlist, for the UI's
// per-pick explanation.
type StepReason struct {
	TrackID  string              `json:"trackId"`
	Kind     string              `json:"kind"` // "required" | "ranked" | "exploration" | legacy kinds
	Detail   string              `json:"detail"`
	Sources  []RetrievalEvidence `json:"sources"`
	Evidence []ComponentEvidence `json:"evidence"`
}

type RetrievalEvidence struct {
	Channel     string  `json:"channel"`
	QueryID     string  `json:"queryId"`
	Rank        int     `json:"rank"`
	Score       float64 `json:"score"` // channel-native score; never a probability
	QueryWeight float64 `json:"queryWeight"`
}

type ComponentEvidence struct {
	Component string  `json:"component"`
	Score     float64 `json:"score"`
	Weight    float64 `json:"weight"`
	Available bool    `json:"available"`
	Detail    string  `json:"detail"`
}

// Candidate is the union record passed from retrieval through ranking. Every
// score has an availability bit so missing profile features are not confused
// with a measured zero.
type Candidate struct {
	Track     TrackRef            `json:"track"`
	Sources   []RetrievalEvidence `json:"sources"`
	Scores    CandidateScores     `json:"scores"`
	Available CandidateFeatures   `json:"available"`
}

type CandidateScores struct {
	RetrievalFusion       float64 `json:"retrievalFusion"` // max-normalized weighted RRF, not a probability
	AudioSeedAffinity     float64 `json:"audioSeedAffinity"`
	CooccurrenceAffinity  float64 `json:"cooccurrenceAffinity"`
	ListenerAffinity      float64 `json:"listenerAffinity"`
	NegativeMatch         float64 `json:"negativeMatch"`
	RecentExposure        float64 `json:"recentExposure"`
	Novelty               float64 `json:"novelty"`
	Total                 float64 `json:"total"`
	SelectionRelevance    float64 `json:"selectionRelevance"`
	EmbeddingRedundancy   float64 `json:"embeddingRedundancy"`
	ArtistConcentration   float64 `json:"artistConcentration"`
	AlbumConcentration    float64 `json:"albumConcentration"`
	MMR                   float64 `json:"mmr"`
	SemanticMatch         float64 `json:"semanticMatch"`
	SemanticNegativeMatch float64 `json:"semanticNegativeMatch"`
}

type CandidateFeatures struct {
	RetrievalFusion       bool `json:"retrievalFusion"`
	AudioSeedAffinity     bool `json:"audioSeedAffinity"`
	CooccurrenceAffinity  bool `json:"cooccurrenceAffinity"`
	ListenerAffinity      bool `json:"listenerAffinity"`
	NegativeMatch         bool `json:"negativeMatch"`
	RecentExposure        bool `json:"recentExposure"`
	Novelty               bool `json:"novelty"`
	SelectionRelevance    bool `json:"selectionRelevance"`
	EmbeddingRedundancy   bool `json:"embeddingRedundancy"`
	ArtistConcentration   bool `json:"artistConcentration"`
	AlbumConcentration    bool `json:"albumConcentration"`
	MMR                   bool `json:"mmr"`
	SemanticMatch         bool `json:"semanticMatch"`
	SemanticNegativeMatch bool `json:"semanticNegativeMatch"`
}

// PlaylistNotice explains why a valid playlist is shorter than requested.
// Codes are stable for bridge/UI consumers; Detail is for people.
type PlaylistNotice struct {
	Code      string `json:"code"`
	Detail    string `json:"detail"`
	Requested int    `json:"requested"`
	Actual    int    `json:"actual"`
}

// Playlist is the output of RecommendationEngine.Build. Reproduction also
// requires the catalog, algorithm, profile snapshot, and generation context
// recorded by the bridge alongside this value.
type Playlist struct {
	Tracks    []TrackRef       `json:"tracks"`
	Mode      Mode             `json:"mode"`
	Seed      RNGSeed          `json:"seed"` // lossless full-width RNG seed
	Rationale []StepReason     `json:"rationale"`
	Intent    MusicIntent      `json:"intent"`
	Notices   []PlaylistNotice `json:"notices"`
}

// IDs returns the track ids in order.
func (p Playlist) IDs() []string {
	out := make([]string, len(p.Tracks))
	for i, t := range p.Tracks {
		out[i] = t.ID
	}
	return out
}
