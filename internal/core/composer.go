package core

// ComposerCredit is a publisher's explicit composition/music credit, not an
// inference from the performer or title. Track association uses the metadata
// index's provisional artist/title match, not a canonical recording identity.
// Scope is track, release_tracks (explicit positions), or release_unknown;
// release_unknown is contextual evidence, not a confirmed per-track credit.
type ComposerCredit struct {
	ArtistID      int64  `json:"artistId,omitempty"` // Discogs identity; absent when not supplied
	Name          string `json:"name"`
	NameVariation string `json:"nameVariation,omitempty"`
	Role          string `json:"role"` // original role, including bracket annotations
	Scope         string `json:"scope"`
	Tracks        string `json:"tracks,omitempty"` // original release-level position expression
	TrackPosition string `json:"trackPosition,omitempty"`
	Source        string `json:"source,omitempty"`        // actual release/master URL
	SourceVersion string `json:"sourceVersion,omitempty"` // credit schema + dump date
}
