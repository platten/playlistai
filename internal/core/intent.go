package core

// Mode selects the recommendation strategy.
type Mode string

const (
	// ModeSimilar walks outward from a single seed (upstream make_playlist).
	ModeSimilar Mode = "similar"
	// ModeJourney interpolates between two or more seeds (upstream join_the_dots).
	ModeJourney Mode = "journey"
)

// Intent field bounds and defaults. Count's ceiling matches the upstream
// Settings.js cap; Lookback's matches the "Keep on" knob.
const (
	DefaultCount      = 20
	DefaultCreativity = 0.5
	DefaultNoise      = 0.0
	DefaultLookback   = 3

	MinCount    = 1
	MaxCount    = 100
	MinLookback = 1
	MaxLookback = 10
)

// IntentSeeds carries either raw search phrases (resolved against the catalog by
// the RecommendationEngine) or track ids the UI already resolved.
type IntentSeeds struct {
	Queries  []string `json:"queries"`
	TrackIDs []string `json:"trackIds"`
}

// IntentConstraints are the only catalog-side filters the Deej-AI dataset can
// support — it has no year, genre, BPM, duration, or explicit metadata.
type IntentConstraints struct {
	ArtistsExclude           []string `json:"artistsExclude"`
	NoRepeatArtistBackToBack bool     `json:"noRepeatArtistBackToBack"`
	ExcludeSeedArtists       bool     `json:"excludeSeedArtists"`
}

// MusicIntent is the entire output of the LLM. It never names or ranks output
// tracks; RecommendationEngine performs all selection.
//
// Creativity, Noise, Lookback, and Count are seeded by the parser but are live
// UI controls: changing any of them re-runs RecommendationEngine.Build with the
// same resolved seeds and no re-parse.
type MusicIntent struct {
	Version     int               `json:"version"`
	Seeds       IntentSeeds       `json:"seeds"`
	Count       int               `json:"count"`
	Mode        Mode              `json:"mode"`
	Creativity  float64           `json:"creativity"` // 0..1 blend of the two embedding spaces
	Noise       float64           `json:"noise"`      // 0..1 "drunk" — std of Gaussian added to the query vector
	Lookback    int               `json:"lookback"`
	Constraints IntentConstraints `json:"constraints"`
	// Seed makes a walk reproducible. 0 lets the engine pick one (and echo it
	// back on Playlist.Seed); "regenerate" in the UI supplies a fresh value.
	// Only matters when Noise > 0.
	Seed         int64  `json:"seed"`
	NotesForUser string `json:"notesForUser"` // shown in UI, never used for selection
}

// Normalized returns a copy with every field clamped to a valid range and
// defaults applied. The engine never trusts a raw parser result.
func (m MusicIntent) Normalized() MusicIntent {
	out := m
	if out.Version == 0 {
		out.Version = 1
	}

	out.Count = clampInt(orDefaultInt(out.Count, DefaultCount), MinCount, MaxCount)
	out.Lookback = clampInt(orDefaultInt(out.Lookback, DefaultLookback), MinLookback, MaxLookback)
	out.Creativity = clampFloat(out.Creativity, 0, 1)
	out.Noise = clampFloat(out.Noise, 0, 1)

	if out.Mode != ModeSimilar && out.Mode != ModeJourney {
		if len(out.Seeds.Queries)+len(out.Seeds.TrackIDs) >= 2 {
			out.Mode = ModeJourney
		} else {
			out.Mode = ModeSimilar
		}
	}

	out.Seeds.Queries = nonEmpty(out.Seeds.Queries)
	out.Seeds.TrackIDs = nonEmpty(out.Seeds.TrackIDs)
	out.Constraints.ArtistsExclude = nonEmpty(out.Constraints.ArtistsExclude)
	return out
}

func orDefaultInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func nonEmpty(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := in[:0:0]
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
