package core

// EnrichedTrack is a TrackRef augmented with cross-service metadata resolved by
// an Enricher. Recording-match fields remain zero when no confident online match
// was found; local source-scoped composer credits can still be available.
type EnrichedTrack struct {
	FullRecordingDuration *RecordingDuration       `json:"fullRecordingDuration,omitempty"`
	Acoustic              *AcousticCharacteristics `json:"acoustic,omitempty"`
	ComposerCredits       []ComposerCredit         `json:"composerCredits,omitempty"`
	CompositionStartYear  int                      `json:"compositionStartYear"`
	CompositionEndYear    int                      `json:"compositionEndYear"`
	WorkID                string                   `json:"workId"`
	IdentityStatus        ResolutionStatus         `json:"identityStatus"`
	Alternatives          []RecordingAlternative   `json:"alternatives"`
	GenreTags             []AttributedGenreTag     `json:"genreTags"`
	Ref                   TrackRef                 `json:"ref"`
	Matched               bool                     `json:"matched"`
	ISRC                  string                   `json:"isrc"`     // primary ISRC ("" if none)
	AllISRCs              []string                 `json:"allIsrcs"` // every ISRC the match carried
	Album                 string                   `json:"album"`
	Year                  int                      `json:"year"` // matched release-edition year; never verified as an original recording year
	AllArtists            []string                 `json:"allArtists"`
	ArtistIDs             []string                 `json:"artistIds"`
	RecordingID           string                   `json:"recordingId"`
	ReleaseID             string                   `json:"releaseId"`
	ReleaseEditionDate    string                   `json:"releaseEditionDate"`
	OriginalReleaseDate   string                   `json:"originalReleaseDate"`
	MatchScore            int                      `json:"matchScore"` // provider search score, 0..100; low = review
}

type RecordingAlternative struct {
	RecordingID string   `json:"recordingId"`
	Title       string   `json:"title"`
	Artists     []string `json:"artists"`
	ISRCs       []string `json:"isrcs"`
	Score       int      `json:"score"`
}

type AttributedGenreTag struct {
	Name     string `json:"name"`
	Votes    int    `json:"votes"`
	Source   string `json:"source"`
	EntityID string `json:"entityId"`
	Facet    string `json:"facet"`
}
