package core

// EnrichedTrack is a TrackRef augmented with cross-service metadata resolved by
// an Enricher (MusicBrainz). Fields are zero when no confident match was found;
// callers must treat that as normal, not an error.
type EnrichedTrack struct {
	Ref                 TrackRef `json:"ref"`
	Matched             bool     `json:"matched"`
	ISRC                string   `json:"isrc"`     // primary ISRC ("" if none)
	AllISRCs            []string `json:"allIsrcs"` // every ISRC the match carried
	Album               string   `json:"album"`
	Year                int      `json:"year"` // matched release-edition year; never verified as an original recording year
	AllArtists          []string `json:"allArtists"`
	ArtistIDs           []string `json:"artistIds"`
	RecordingID         string   `json:"recordingId"`
	ReleaseID           string   `json:"releaseId"`
	ReleaseEditionDate  string   `json:"releaseEditionDate"`
	OriginalReleaseDate string   `json:"originalReleaseDate"`
	MatchScore          int      `json:"matchScore"` // provider search score, 0..100; low = review
}
