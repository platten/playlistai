package core

// StepReason records why a track landed in a playlist, for the UI's
// per-pick explanation.
type StepReason struct {
	TrackID string `json:"trackId"`
	Kind    string `json:"kind"` // "nearest" | "noise-jump" | "interp" | "dedup-skip" | "fallback"
	Detail  string `json:"detail"`
}

// Playlist is the output of RecommendationEngine.Build. It is deterministic
// given (intent, catalog, Seed).
type Playlist struct {
	Tracks    []TrackRef   `json:"tracks"`
	Mode      Mode         `json:"mode"`
	Seed      int64        `json:"seed"` // RNG seed used; replay-able
	Rationale []StepReason `json:"rationale"`
	Intent    MusicIntent  `json:"intent"`
}

// IDs returns the track ids in order.
func (p Playlist) IDs() []string {
	out := make([]string, len(p.Tracks))
	for i, t := range p.Tracks {
		out[i] = t.ID
	}
	return out
}
